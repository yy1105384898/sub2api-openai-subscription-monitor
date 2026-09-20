package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const keyID = "yangyang-openai-monitor-v1"

type manifest struct {
	SchemaVersion int                     `json:"schema_version"`
	ID            string                  `json:"id"`
	Name          string                  `json:"name"`
	Version       string                  `json:"version"`
	Description   string                  `json:"description"`
	Author        string                  `json:"author"`
	Requires      manifestRequires        `json:"requires"`
	Capabilities  []manifestCapability    `json:"capabilities"`
	Runtimes      map[string]manifestPath `json:"runtimes"`
	UI            manifestUI              `json:"ui"`
	Files         map[string]string       `json:"files"`
}

type manifestRequires struct {
	Sub2API                   string   `json:"sub2api"`
	RecommendedSub2APIVersion string   `json:"recommended_sub2api_version"`
	TestedSub2APIVersions     []string `json:"tested_sub2api_versions"`
	PluginProtocol            int      `json:"plugin_protocol"`
	TransportAPI              int      `json:"transport_api"`
	UIBridge                  int      `json:"ui_bridge"`
}

type manifestCapability struct {
	ID          string `json:"id"`
	Platform    string `json:"platform"`
	AccountType string `json:"account_type"`
}

type manifestPath struct {
	Path string `json:"path"`
}

type manifestUI struct {
	Entrypoint string `json:"entrypoint"`
}

func main() {
	binary := flag.String("binary", "", "linux-amd64 plugin binary")
	windowsBinary := flag.String("windows-binary", "", "optional windows-amd64 plugin binary")
	uiDir := flag.String("ui", "ui", "UI directory")
	output := flag.String("output", "dist/openai-subscription-monitor.s2plugin", "output package")
	keyDir := flag.String("key-dir", ".keys", "publisher key directory")
	version := flag.String("version", "0.2.1", "plugin version")
	flag.Parse()
	if *binary == "" {
		fatal("-binary is required")
	}
	privateKey, err := loadOrCreateKey(*keyDir)
	if err != nil {
		fatal(err.Error())
	}
	files := map[string][]byte{}
	runtimeData, err := os.ReadFile(*binary)
	if err != nil {
		fatal(err.Error())
	}
	files["runtimes/linux-amd64/plugin"] = runtimeData
	if *windowsBinary != "" {
		data, readErr := os.ReadFile(*windowsBinary)
		if readErr != nil {
			fatal(readErr.Error())
		}
		files["runtimes/windows-amd64/plugin.exe"] = data
	}
	err = filepath.WalkDir(*uiDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		rel, err := filepath.Rel(*uiDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files["ui/"+filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		fatal(err.Error())
	}
	hashes := make(map[string]string, len(files))
	for path, data := range files {
		sum := sha256.Sum256(data)
		hashes[path] = hex.EncodeToString(sum[:])
	}
	m := manifest{
		SchemaVersion: 1,
		ID:            "yangyang.openai.subscription-monitor",
		Name:          "OpenAI 订阅监控",
		Version:       *version,
		Description:   "自动读取 Sub2API 中已登录的 OpenAI OAuth 账号，监控套餐、续费、到期时间和 Codex 用量。",
		Author:        "yangyang",
		Requires: manifestRequires{
			Sub2API:                   ">=2.7.4 <2.8.0",
			RecommendedSub2APIVersion: "2.7.4",
			TestedSub2APIVersions:     []string{"2.7.4"},
			PluginProtocol:            1,
			TransportAPI:              1,
			UIBridge:                  1,
		},
		Capabilities: []manifestCapability{{ID: "openai.oauth.outbound_transport.v1", Platform: "openai", AccountType: "oauth"}},
		Runtimes: map[string]manifestPath{
			"linux-amd64": {Path: "runtimes/linux-amd64/plugin"},
		},
		UI:    manifestUI{Entrypoint: "ui/index.html"},
		Files: hashes,
	}
	if *windowsBinary != "" {
		m.Runtimes["windows-amd64"] = manifestPath{Path: "runtimes/windows-amd64/plugin.exe"}
	}
	manifestData, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fatal(err.Error())
	}
	manifestData = append(manifestData, '\n')
	signature := ed25519.Sign(privateKey, manifestData)
	signatureData, _ := json.MarshalIndent(map[string]string{
		"algorithm": "ed25519",
		"key_id":    keyID,
		"signature": base64.StdEncoding.EncodeToString(signature),
	}, "", "  ")
	signatureData = append(signatureData, '\n')
	if err := writePackage(*output, manifestData, signatureData, files); err != nil {
		fatal(err.Error())
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fatal(err.Error())
	}
	publicPath := filepath.Join(filepath.Dir(*output), "publisher-public-key.txt")
	publicLine := keyID + "=" + base64.StdEncoding.EncodeToString(publicKey) + "\n"
	if err := os.WriteFile(publicPath, []byte(publicLine), 0o644); err != nil {
		fatal(err.Error())
	}
	fmt.Printf("created %s\npublisher key id: %s\npublic key: %s\n", *output, keyID, publicPath)
}

func loadOrCreateKey(directory string) (ed25519.PrivateKey, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "publisher-private.key")
	if data, err := os.ReadFile(path); err == nil {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(decoded) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("invalid publisher private key")
		}
		return ed25519.PrivateKey(decoded), nil
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(privateKey)
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		return nil, err
	}
	return privateKey, nil
}

func writePackage(output string, manifestData, signatureData []byte, files map[string][]byte) error {
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	handle, err := os.Create(output)
	if err != nil {
		return err
	}
	defer handle.Close()
	archive := zip.NewWriter(handle)
	defer archive.Close()
	if err := addFile(archive, "manifest.json", manifestData, 0o644); err != nil {
		return err
	}
	if err := addFile(archive, "signature.json", signatureData, 0o644); err != nil {
		return err
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		mode := fs.FileMode(0o644)
		if strings.HasPrefix(path, "runtimes/") {
			mode = 0o755
		}
		if err := addFile(archive, path, files[path], mode); err != nil {
			return err
		}
	}
	return nil
}

func addFile(archive *zip.Writer, name string, data []byte, mode fs.FileMode) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(mode)
	writer, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
