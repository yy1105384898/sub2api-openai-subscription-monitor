package monitor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	pluginv1 "github.com/yy1105384898/sub2api-openai-subscription-monitor/pluginapi/v1"
)

func (p *Plugin) Forward(stream pluginv1.TransportPlugin_ForwardServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return sendForwardError(stream, "INVALID_REQUEST", "首帧必须是请求元数据", false)
	}
	req, bodyWriter, err := buildForwardRequest(stream.Context(), start)
	if err != nil {
		return sendForwardError(stream, "INVALID_REQUEST", err.Error(), false)
	}
	client, err := newHTTPClient(start.GetProxyUrl(), 0)
	if err != nil {
		if bodyWriter != nil {
			_ = bodyWriter.CloseWithError(err)
		}
		return sendForwardError(stream, "INVALID_PROXY", "账号代理地址无效", false)
	}
	type responseResult struct {
		response *http.Response
		err      error
	}
	responseCh := make(chan responseResult, 1)
	go func() {
		response, requestErr := client.Do(req)
		responseCh <- responseResult{response: response, err: requestErr}
	}()
	if bodyWriter != nil {
		if err := receiveRequestBody(stream, bodyWriter); err != nil {
			return sendForwardError(stream, "REQUEST_BODY_ERROR", "读取请求体失败", true)
		}
	} else {
		if err := consumeBodyEnd(stream); err != nil {
			return sendForwardError(stream, "REQUEST_BODY_ERROR", "请求帧不完整", true)
		}
	}
	result := <-responseCh
	if result.err != nil {
		return sendForwardError(stream, "UPSTREAM_ERROR", "连接上游失败", true)
	}
	defer result.response.Body.Close()
	return sendHTTPResponse(stream, result.response)
}

func buildForwardRequest(ctx context.Context, start *pluginv1.ForwardRequestStart) (*http.Request, *io.PipeWriter, error) {
	if start.GetMethod() == "" || start.GetUrl() == "" {
		return nil, nil, errors.New("请求方法或 URL 为空")
	}
	var body io.Reader
	var writer *io.PipeWriter
	if start.GetHasBody() {
		reader, pipeWriter := io.Pipe()
		body, writer = reader, pipeWriter
	}
	req, err := http.NewRequestWithContext(ctx, start.GetMethod(), start.GetUrl(), body)
	if err != nil {
		return nil, writer, errors.New("请求 URL 无效")
	}
	copyHeaders(req.Header, start.GetHeaders())
	for key, wrapped := range start.GetHeaders() {
		if wrapped == nil || strings.EqualFold(key, "content-length") {
			continue
		}
		req.Header.Del(key)
		for _, value := range wrapped.GetValues() {
			req.Header.Add(key, value)
		}
	}
	req.Host = start.GetHost()
	req.ContentLength = start.GetContentLength()
	return req, writer, nil
}

func receiveRequestBody(stream pluginv1.TransportPlugin_ForwardServer, writer *io.PipeWriter) error {
	defer writer.Close()
	for {
		frame, err := stream.Recv()
		if err != nil {
			_ = writer.CloseWithError(err)
			return err
		}
		if chunk := frame.GetBodyChunk(); len(chunk) > 0 {
			if _, err := writer.Write(chunk); err != nil {
				return err
			}
			continue
		}
		if frame.GetBodyEnd() {
			return nil
		}
		return errors.New("unexpected request frame")
	}
}

func consumeBodyEnd(stream pluginv1.TransportPlugin_ForwardServer) error {
	frame, err := stream.Recv()
	if err != nil {
		return err
	}
	if !frame.GetBodyEnd() {
		return errors.New("missing body_end")
	}
	return nil
}

func sendHTTPResponse(stream pluginv1.TransportPlugin_ForwardServer, response *http.Response) error {
	if err := stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_Start{Start: &pluginv1.ForwardResponseStart{
		StatusCode:    int32(response.StatusCode),
		Status:        response.Status,
		Protocol:      response.Proto,
		ProtocolMajor: int32(response.ProtoMajor),
		ProtocolMinor: int32(response.ProtoMinor),
		Headers:       toPluginHeaders(response.Header),
		ContentLength: response.ContentLength,
	}}}); err != nil {
		return err
	}
	buffer := make([]byte, 32*1024)
	var received int64
	started := time.Now()
	for {
		count, err := response.Body.Read(buffer)
		if count > 0 {
			received += int64(count)
			chunk := append([]byte(nil), buffer[:count]...)
			if sendErr := stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_BodyChunk{BodyChunk: chunk}}); sendErr != nil {
				return sendErr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return sendForwardError(stream, "UPSTREAM_BODY_ERROR", "读取上游响应失败", true)
		}
	}
	return stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_End{End: &pluginv1.ForwardResponseEnd{BytesReceived: received, DurationMs: time.Since(started).Milliseconds()}}})
}

func toPluginHeaders(headers http.Header) map[string]*pluginv1.HeaderValues {
	result := make(map[string]*pluginv1.HeaderValues, len(headers))
	for key, values := range headers {
		result[key] = &pluginv1.HeaderValues{Values: append([]string(nil), values...)}
	}
	return result
}

func sendForwardError(stream pluginv1.TransportPlugin_ForwardServer, code, message string, sent bool) error {
	return stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_Error{Error: &pluginv1.ForwardResponseError{Code: code, Message: message, RequestSent: sent}}})
}
