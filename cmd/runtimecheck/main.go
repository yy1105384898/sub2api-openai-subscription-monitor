package main

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	hcplugin "github.com/hashicorp/go-plugin"
	pluginv1 "github.com/yy1105384898/sub2api-openai-subscription-monitor/pluginapi/v1"
)

type packageManifest struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Runtimes map[string]struct {
		Path string `json:"path"`
	} `json:"runtimes"`
	Files map[string]string `json:"files"`
}

type packageSignature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"`
}

func main() {
	packagePath := flag.String("package", "", "s2plugin package")
	publicKeyPath := flag.String("public-key", "", "publisher public key file")
	flag.Parse()
	if *packagePath == "" || *publicKeyPath == "" {
		fatal(errors.New("-package and -public-key are required"))
	}
	temporary, err := os.MkdirTemp("", "s2plugin-check-")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(temporary)
	manifest, binary, err := verifyAndExtract(*packagePath, *publicKeyPath, temporary)
	if err != nil {
		fatal(err)
	}
	client := hcplugin.NewClient(&hcplugin.ClientConfig{
		HandshakeConfig:  pluginv1.HandshakeConfig,
		Plugins:          pluginv1.ClientPluginMap(),
		Cmd:              command(binary),
		AllowedProtocols: []hcplugin.Protocol{hcplugin.ProtocolGRPC},
		StartTimeout:     10 * time.Second,
		Logger:           hclog.NewNullLogger(),
	})
	defer client.Kill()
	rpcClient, err := client.Client()
	if err != nil {
		fatal(err)
	}
	dispensed, err := rpcClient.Dispense(pluginv1.TransportPluginName)
	if err != nil {
		fatal(err)
	}
	transport := dispensed.(*pluginv1.TransportClient).TransportPluginClient
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := transport.GetInfo(ctx, &pluginv1.GetInfoRequest{})
	if err != nil || info.GetPluginId() != manifest.ID || info.GetPluginVersion() != manifest.Version {
		fatal(fmt.Errorf("runtime identity mismatch: %v", err))
	}
	validated, err := transport.ValidateConfig(ctx, &pluginv1.ValidateConfigRequest{ConfigJson: []byte(`{}`)})
	if err != nil || !validated.GetValid() {
		fatal(fmt.Errorf("config validation failed: %v", err))
	}
	if _, err := transport.ApplyConfig(ctx, &pluginv1.ApplyConfigRequest{ConfigJson: validated.GetNormalizedConfigJson()}); err != nil {
		fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		writer.Header().Set("X-Runtime-Check", "ok")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write(append([]byte("forwarded:"), body...))
	}))
	defer server.Close()
	stream, err := transport.Forward(ctx)
	if err != nil {
		fatal(err)
	}
	payload := []byte("probe")
	if err := stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_Start{Start: &pluginv1.ForwardRequestStart{Method: http.MethodPost, Url: server.URL, Headers: map[string]*pluginv1.HeaderValues{"Content-Type": {Values: []string{"text/plain"}}}, ContentLength: int64(len(payload)), HasBody: true}}}); err != nil {
		fatal(err)
	}
	if err := stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_BodyChunk{BodyChunk: payload}}); err != nil {
		fatal(err)
	}
	if err := stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_BodyEnd{BodyEnd: true}}); err != nil {
		fatal(err)
	}
	if err := stream.CloseSend(); err != nil {
		fatal(err)
	}
	var status int32
	var responseBody []byte
	for {
		frame, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			fatal(recvErr)
		}
		if frame.GetError() != nil {
			fatal(errors.New(frame.GetError().GetMessage()))
		}
		if frame.GetStart() != nil {
			status = frame.GetStart().GetStatusCode()
		}
		responseBody = append(responseBody, frame.GetBodyChunk()...)
		if frame.GetEnd() != nil {
			break
		}
	}
	if status != http.StatusCreated || string(responseBody) != "forwarded:probe" {
		fatal(fmt.Errorf("forward test failed: status=%d body=%q", status, responseBody))
	}
	fmt.Println("package signature, runtime handshake, config and forwarding verified")
}

func verifyAndExtract(packagePath, publicKeyPath, directory string) (packageManifest, string, error) {
	archive, err := zip.OpenReader(packagePath)
	if err != nil {
		return packageManifest{}, "", err
	}
	defer archive.Close()
	contents := map[string][]byte{}
	for _, file := range archive.File {
		reader, openErr := file.Open()
		if openErr != nil {
			return packageManifest{}, "", openErr
		}
		data, readErr := io.ReadAll(reader)
		reader.Close()
		if readErr != nil {
			return packageManifest{}, "", readErr
		}
		contents[file.Name] = data
	}
	var manifest packageManifest
	if err := json.Unmarshal(contents["manifest.json"], &manifest); err != nil {
		return manifest, "", err
	}
	var signature packageSignature
	if err := json.Unmarshal(contents["signature.json"], &signature); err != nil {
		return manifest, "", err
	}
	publicLine, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return manifest, "", err
	}
	parts := splitOnce(string(publicLine), '=')
	if len(parts) != 2 || parts[0] != signature.KeyID {
		return manifest, "", errors.New("publisher key id mismatch")
	}
	publicKey, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return manifest, "", err
	}
	signatureBytes, err := base64.StdEncoding.DecodeString(signature.Signature)
	if err != nil || !ed25519.Verify(publicKey, contents["manifest.json"], signatureBytes) {
		return manifest, "", errors.New("signature verification failed")
	}
	for path, expected := range manifest.Files {
		data, ok := contents[path]
		if !ok {
			return manifest, "", fmt.Errorf("missing file %s", path)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != expected {
			return manifest, "", fmt.Errorf("hash mismatch %s", path)
		}
	}
	runtimeKey := runtime.GOOS + "-" + runtime.GOARCH
	runtimeEntry, ok := manifest.Runtimes[runtimeKey]
	if !ok {
		return manifest, "", fmt.Errorf("runtime %s missing", runtimeKey)
	}
	binary := filepath.Join(directory, filepath.Base(runtimeEntry.Path))
	if err := os.WriteFile(binary, contents[runtimeEntry.Path], 0o755); err != nil {
		return manifest, "", err
	}
	return manifest, binary, nil
}

func splitOnce(value string, separator byte) []string {
	for index := range value {
		if value[index] == separator {
			return []string{value[:index], trimSpace(value[index+1:])}
		}
	}
	return nil
}

func trimSpace(value string) string {
	for len(value) > 0 && (value[len(value)-1] == '\n' || value[len(value)-1] == '\r' || value[len(value)-1] == ' ') {
		value = value[:len(value)-1]
	}
	return value
}

func command(path string) *exec.Cmd { return exec.Command(path) }

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
