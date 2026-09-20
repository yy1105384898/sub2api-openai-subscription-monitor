package monitor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	pluginv1 "github.com/yy1105384898/sub2api-openai-subscription-monitor/pluginapi/v1"
)

func newHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, errors.New("invalid proxy URL")
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

func getJSON(ctx context.Context, client *http.Client, identity *pluginv1.ResolveOutboundIdentityResponse, endpoint string, target any) error {
	return getJSONWithHeaders(ctx, client, identity, endpoint, nil, target)
}

func getJSONWithHeaders(ctx context.Context, client *http.Client, identity *pluginv1.ResolveOutboundIdentityResponse, endpoint string, headers map[string]string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("创建官方订阅请求失败")
	}
	copyHeaders(req.Header, identity.GetHeaders())
	req.Header.Set("Authorization", "Bearer "+identity.GetToken())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://chatgpt.com")
	req.Header.Set("Referer", "https://chatgpt.com/")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("连接 OpenAI 官方服务失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("OpenAI 官方服务返回 HTTP %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024))
	if err := decoder.Decode(target); err != nil {
		return errors.New("OpenAI 官方服务返回内容无法解析")
	}
	return nil
}

func copyHeaders(dst http.Header, source map[string]*pluginv1.HeaderValues) {
	for key, wrapped := range source {
		if wrapped == nil || strings.EqualFold(key, "authorization") || strings.EqualFold(key, "content-length") {
			continue
		}
		for _, value := range wrapped.GetValues() {
			dst.Add(key, value)
		}
	}
}

func headerValue(headers map[string]*pluginv1.HeaderValues, name string) string {
	for key, wrapped := range headers {
		if strings.EqualFold(key, name) && wrapped != nil && len(wrapped.Values) > 0 {
			return strings.TrimSpace(wrapped.Values[0])
		}
	}
	return ""
}

func parseJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims
}

func selectAccount(root map[string]any, preferredID string) (string, map[string]any) {
	accounts, _ := root["accounts"].(map[string]any)
	if preferredID != "" {
		if raw, ok := accounts[preferredID]; ok {
			if account, ok := raw.(map[string]any); ok {
				return preferredID, account
			}
		}
		for key, raw := range accounts {
			account, _ := raw.(map[string]any)
			if nestedString(account, "account", "account_id") == preferredID {
				return preferredID, account
			}
			_ = key
		}
	}
	var fallbackID string
	var fallback map[string]any
	for key, raw := range accounts {
		account, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := firstNonEmpty(nestedString(account, "account", "account_id"), key)
		if nestedBool(account, "account", "is_default") {
			return id, account
		}
		plan := firstNonEmpty(nestedString(account, "account", "plan_type"), nestedString(account, "entitlement", "subscription_plan"))
		if fallback == nil || !strings.EqualFold(plan, "free") {
			fallbackID, fallback = id, account
		}
	}
	return fallbackID, fallback
}

func nestedString(root map[string]any, keys ...string) string {
	var current any = root
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	value, _ := current.(string)
	return strings.TrimSpace(value)
}

func nestedBool(root map[string]any, keys ...string) bool {
	var current any = root
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current = object[key]
	}
	value, _ := current.(bool)
	return value
}

func stringValue(root map[string]any, key string) string {
	value, _ := root[key].(string)
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func maskEmail(email string) string {
	parts := strings.Split(strings.TrimSpace(email), "@")
	if len(parts) != 2 || parts[0] == "" {
		return ""
	}
	name := []rune(parts[0])
	visible := string(name[:1])
	if len(name) > 1 {
		visible += "***"
	}
	return visible + "@" + parts[1]
}

func maskID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 11 && value[4:7] == "..." {
		return value
	}
	if len(value) <= 8 {
		return value
	}
	return value[:4] + "..." + value[len(value)-4:]
}
