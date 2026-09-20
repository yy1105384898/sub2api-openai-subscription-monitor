package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	hcplugin "github.com/hashicorp/go-plugin"
	pluginv1 "github.com/yy1105384898/sub2api-openai-subscription-monitor/pluginapi/v1"
)

const (
	pluginID         = "yangyang.openai.subscription-monitor"
	capabilityID     = "openai.oauth.outbound_transport.v1"
	accountsCheckURL = "https://chatgpt.com/backend-api/accounts/check/v4-2023-04-27"
	subscriptionsURL = "https://chatgpt.com/backend-api/subscriptions"
	usageURL         = "https://chatgpt.com/backend-api/wham/usage"
	resetCreditsURL  = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	dailyUsageURL    = "https://chatgpt.com/backend-api/wham/analytics/daily-workspace-usage-counts"
	stateNamespace   = "monitor"
	stateKey         = "snapshot"
)

type Plugin struct {
	pluginv1.UnimplementedTransportPluginServer

	version string
	broker  *hcplugin.GRPCBroker

	mu        sync.RWMutex
	config    Config
	host      pluginv1.HostServiceClient
	hostConn  io.Closer
	status    Status
	runCancel context.CancelFunc
	refreshMu sync.Mutex
}

type Status struct {
	Running       bool            `json:"running"`
	LastRefreshAt string          `json:"last_refresh_at,omitempty"`
	NextRefreshAt string          `json:"next_refresh_at,omitempty"`
	Total         int             `json:"total"`
	Paid          int             `json:"paid"`
	ExpiringSoon  int             `json:"expiring_soon"`
	Failed        int             `json:"failed"`
	Message       string          `json:"message,omitempty"`
	Accounts      []AccountStatus `json:"accounts"`
}

type AccountStatus struct {
	HostAccountID int64               `json:"host_account_id"`
	AccountID     string              `json:"account_id,omitempty"`
	Email         string              `json:"email,omitempty"`
	PlanType      string              `json:"plan_type,omitempty"`
	ActiveUntil   string              `json:"active_until,omitempty"`
	WillRenew     *bool               `json:"will_renew,omitempty"`
	Usage         *UsageStatus        `json:"usage,omitempty"`
	Credits       *CreditsStatus      `json:"credits,omitempty"`
	ResetCredits  *ResetCreditsStatus `json:"reset_credits,omitempty"`
	OfficialUsage *OfficialUsage      `json:"official_usage,omitempty"`
	CheckedAt     string              `json:"checked_at"`
	Error         string              `json:"error,omitempty"`
}

type UsageStatus struct {
	Allowed         bool         `json:"allowed"`
	LimitReached    bool         `json:"limit_reached"`
	PrimaryWindow   *UsageWindow `json:"primary_window,omitempty"`
	SecondaryWindow *UsageWindow `json:"secondary_window,omitempty"`
}

type UsageWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAt            int64   `json:"reset_at"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
}

type ResetCreditsStatus struct {
	AvailableCount           int                 `json:"available_count"`
	ApplicableAvailableCount int                 `json:"applicable_available_count,omitempty"`
	Credits                  []ResetCreditDetail `json:"credits,omitempty"`
}

type ResetCreditDetail struct {
	ExpiresAt string `json:"expires_at,omitempty"`
}

type CreditsStatus struct {
	HasCredits          bool    `json:"has_credits"`
	Unlimited           bool    `json:"unlimited"`
	OverageLimitReached bool    `json:"overage_limit_reached"`
	Balance             *string `json:"balance,omitempty"`
	ApproxLocalMessages []int   `json:"approx_local_messages,omitempty"`
	ApproxCloudMessages []int   `json:"approx_cloud_messages,omitempty"`
}

type subscriptionResponse struct {
	PlanType    string `json:"plan_type"`
	ActiveUntil string `json:"active_until"`
	WillRenew   bool   `json:"will_renew"`
	ID          string `json:"id"`
}

type usageResponse struct {
	AccountID             string              `json:"account_id"`
	Email                 string              `json:"email"`
	PlanType              string              `json:"plan_type"`
	RateLimit             *UsageStatus        `json:"rate_limit"`
	Credits               *CreditsStatus      `json:"credits,omitempty"`
	RateLimitResetCredits *ResetCreditsStatus `json:"rate_limit_reset_credits,omitempty"`
}

func New(version string) *Plugin {
	return &Plugin{
		version: version,
		config:  defaultConfig(),
		status: Status{
			Message:  "等待宿主账号目录连接",
			Accounts: []AccountStatus{},
		},
	}
}

func (p *Plugin) SetHostBroker(broker *hcplugin.GRPCBroker) {
	p.mu.Lock()
	p.broker = broker
	p.mu.Unlock()
}

func (p *Plugin) GetInfo(context.Context, *pluginv1.GetInfoRequest) (*pluginv1.GetInfoResponse, error) {
	return &pluginv1.GetInfoResponse{
		PluginId:            pluginID,
		PluginVersion:       p.version,
		ProtocolVersion:     pluginv1.ProtocolVersion,
		TransportApiVersion: pluginv1.TransportAPIVersion,
		Capabilities:        []string{capabilityID},
	}, nil
}

func (p *Plugin) Health(context.Context, *pluginv1.HealthRequest) (*pluginv1.HealthResponse, error) {
	p.mu.RLock()
	status := p.status
	p.mu.RUnlock()
	data, _ := json.Marshal(status)
	return &pluginv1.HealthResponse{Healthy: true, Message: "订阅监控运行正常", StatusJson: string(data)}, nil
}

func (p *Plugin) ValidateConfig(_ context.Context, req *pluginv1.ValidateConfigRequest) (*pluginv1.ValidateConfigResponse, error) {
	_, normalized, err := parseConfig(req.GetConfigJson())
	if err != nil {
		return &pluginv1.ValidateConfigResponse{Valid: false, Message: err.Error()}, nil
	}
	return &pluginv1.ValidateConfigResponse{Valid: true, Message: "配置有效", NormalizedConfigJson: normalized}, nil
}

func (p *Plugin) ApplyConfig(_ context.Context, req *pluginv1.ApplyConfigRequest) (*pluginv1.ApplyConfigResponse, error) {
	cfg, _, err := parseConfig(req.GetConfigJson())
	if err != nil {
		return &pluginv1.ApplyConfigResponse{Applied: false, Message: err.Error()}, nil
	}
	p.mu.Lock()
	p.config = cfg
	hasHost := p.host != nil
	p.mu.Unlock()
	if hasHost {
		p.restartLoop()
	}
	return &pluginv1.ApplyConfigResponse{Applied: true, Message: "配置已应用"}, nil
}

func (p *Plugin) TestConfig(ctx context.Context, req *pluginv1.TestConfigRequest) (*pluginv1.TestConfigResponse, error) {
	if _, _, err := parseConfig(req.GetConfigJson()); err != nil {
		return &pluginv1.TestConfigResponse{Success: false, Message: err.Error()}, nil
	}
	started := time.Now()
	if err := p.refresh(ctx); err != nil {
		return &pluginv1.TestConfigResponse{Success: false, Message: err.Error(), LatencyMs: time.Since(started).Milliseconds()}, nil
	}
	statusJSON := p.statusJSON()
	return &pluginv1.TestConfigResponse{Success: true, Message: "账号订阅信息刷新成功", LatencyMs: time.Since(started).Milliseconds(), StatusJson: statusJSON}, nil
}

func (p *Plugin) InitHostServices(ctx context.Context, req *pluginv1.InitHostServicesRequest) (*pluginv1.InitHostServicesResponse, error) {
	if req.GetHostServiceApiVersion() != pluginv1.HostServiceAPIVersion {
		return &pluginv1.InitHostServicesResponse{Ready: false, Message: "宿主服务版本不兼容"}, nil
	}
	p.mu.RLock()
	broker := p.broker
	p.mu.RUnlock()
	if broker == nil {
		return &pluginv1.InitHostServicesResponse{Ready: false, Message: "宿主 broker 不可用"}, nil
	}
	conn, err := broker.Dial(req.GetHostServiceId())
	if err != nil {
		return &pluginv1.InitHostServicesResponse{Ready: false, Message: "连接宿主服务失败"}, nil
	}
	p.mu.Lock()
	if p.hostConn != nil {
		_ = p.hostConn.Close()
	}
	p.hostConn = conn
	p.host = pluginv1.NewHostServiceClient(conn)
	p.status.Message = "宿主账号目录已连接"
	p.mu.Unlock()
	p.loadSnapshot(ctx)
	p.restartLoop()
	return &pluginv1.InitHostServicesResponse{Ready: true, Message: "订阅监控已连接宿主账号目录"}, nil
}

func (p *Plugin) restartLoop() {
	p.mu.Lock()
	if p.runCancel != nil {
		p.runCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.runCancel = cancel
	p.status.Running = true
	cfg := p.config
	p.mu.Unlock()
	go func() {
		_ = p.refresh(ctx)
		ticker := time.NewTicker(time.Duration(cfg.IntervalMinutes) * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = p.refresh(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (p *Plugin) refresh(ctx context.Context) error {
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	p.mu.RLock()
	host := p.host
	cfg := p.config
	p.mu.RUnlock()
	if host == nil {
		return errors.New("宿主账号目录尚未连接")
	}
	listCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	listed, err := host.ListAccounts(listCtx, &pluginv1.ListAccountsRequest{Platform: "openai", AccountType: "oauth"})
	if err != nil {
		return fmt.Errorf("读取 OpenAI OAuth 账号失败: %w", err)
	}
	ids := listed.GetAccountIds()
	results := make([]AccountStatus, len(ids))
	sem := make(chan struct{}, cfg.MaxConcurrency)
	var wg sync.WaitGroup
	for index, accountID := range ids {
		index, accountID := index, accountID
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[index] = AccountStatus{HostAccountID: accountID, CheckedAt: time.Now().UTC().Format(time.RFC3339), Error: "刷新已取消"}
				return
			}
			results[index] = p.checkAccount(ctx, host, cfg, accountID)
		}()
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].HostAccountID < results[j].HostAccountID })
	now := time.Now().UTC()
	status := summarize(results, now, time.Duration(cfg.IntervalMinutes)*time.Minute)
	p.mu.Lock()
	p.status = status
	p.mu.Unlock()
	p.saveSnapshot(context.Background(), host, status)
	return nil
}

func (p *Plugin) checkAccount(ctx context.Context, host pluginv1.HostServiceClient, cfg Config, accountID int64) AccountStatus {
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	result := AccountStatus{HostAccountID: accountID, CheckedAt: checkedAt}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Duration(cfg.RequestTimeoutSeconds)*time.Second)
	defer cancel()
	identity, err := host.ResolveOutboundIdentity(requestCtx, &pluginv1.ResolveOutboundIdentityRequest{AccountId: accountID})
	if err != nil || !identity.GetFound() {
		result.Error = "无法取得有效 OAuth 身份"
		return result
	}
	client, err := newHTTPClient(identity.GetProxyUrl(), time.Duration(cfg.RequestTimeoutSeconds)*time.Second)
	if err != nil {
		result.Error = "账号代理配置无效"
		return result
	}
	claims := parseJWTClaims(identity.GetToken())
	preferredID := firstNonEmpty(headerValue(identity.GetHeaders(), "ChatGPT-Account-Id"), nestedString(claims, "https://api.openai.com/auth", "chatgpt_account_id"))
	var check map[string]any
	if err := getJSON(requestCtx, client, identity, accountsCheckURL, &check); err != nil {
		result.Error = err.Error()
		return result
	}
	selectedID, selected := selectAccount(check, preferredID)
	result.AccountID = displayID(selectedID, cfg.MaskAccountIdentity)
	result.Email = displayEmail(firstNonEmpty(nestedString(selected, "account", "email"), stringValue(selected, "email"), nestedString(claims, "https://api.openai.com/profile", "email")), cfg.MaskAccountIdentity)
	result.PlanType = firstNonEmpty(nestedString(selected, "account", "plan_type"), nestedString(selected, "entitlement", "subscription_plan"), nestedString(claims, "https://api.openai.com/auth", "chatgpt_plan_type"))
	result.ActiveUntil = nestedString(selected, "entitlement", "expires_at")
	if selectedID != "" {
		var subscription subscriptionResponse
		u := subscriptionsURL + "?account_id=" + url.QueryEscape(selectedID)
		if err := getJSON(requestCtx, client, identity, u, &subscription); err == nil {
			result.PlanType = firstNonEmpty(subscription.PlanType, result.PlanType)
			result.ActiveUntil = firstNonEmpty(subscription.ActiveUntil, result.ActiveUntil)
			result.WillRenew = &subscription.WillRenew
		}
	}
	if cfg.IncludeUsage {
		var usage usageResponse
		quotaHeaders := map[string]string{"OpenAI-Beta": "codex-1", "OAI-Language": "zh-CN", "Originator": "Codex Desktop"}
		if err := getJSONWithHeaders(requestCtx, client, identity, usageURL, quotaHeaders, &usage); err == nil {
			result.PlanType = firstNonEmpty(usage.PlanType, result.PlanType)
			result.Email = firstNonEmpty(result.Email, displayEmail(usage.Email, cfg.MaskAccountIdentity))
			result.AccountID = firstNonEmpty(result.AccountID, displayID(usage.AccountID, cfg.MaskAccountIdentity))
			result.Usage = usage.RateLimit
			result.Credits = usage.Credits
			result.ResetCredits = usage.RateLimitResetCredits
		}
		var resetPayload json.RawMessage
		if err := getJSONWithHeaders(requestCtx, client, identity, resetCreditsURL, quotaHeaders, &resetPayload); err == nil {
			if credits, parseErr := parseResetCredits(resetPayload); parseErr == nil {
				result.ResetCredits = mergeResetCredits(result.ResetCredits, credits)
			}
		}
		var daily whamDailyUsageResponse
		if err := getJSONWithHeaders(requestCtx, client, identity, buildDailyUsageURL(time.Now().UTC()), quotaHeaders, &daily); err == nil {
			result.OfficialUsage = summarizeDailyUsage(daily)
		}
	}
	if result.PlanType == "" {
		result.PlanType = "unknown"
	}
	return result
}

func displayEmail(value string, masked bool) string {
	if masked {
		return maskEmail(value)
	}
	return strings.TrimSpace(value)
}

func displayID(value string, masked bool) string {
	if masked {
		return maskID(value)
	}
	return strings.TrimSpace(value)
}

func summarize(accounts []AccountStatus, now time.Time, interval time.Duration) Status {
	status := Status{
		Running:       true,
		LastRefreshAt: now.Format(time.RFC3339),
		NextRefreshAt: now.Add(interval).Format(time.RFC3339),
		Total:         len(accounts),
		Accounts:      accounts,
		Message:       "监控快照已更新",
	}
	for _, account := range accounts {
		if account.Error != "" {
			status.Failed++
			continue
		}
		if !strings.EqualFold(account.PlanType, "free") && !strings.EqualFold(account.PlanType, "unknown") {
			status.Paid++
		}
		if expires, err := time.Parse(time.RFC3339, account.ActiveUntil); err == nil && expires.After(now) && expires.Before(now.Add(7*24*time.Hour)) {
			status.ExpiringSoon++
		}
	}
	return status
}

func (p *Plugin) statusJSON() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	data, _ := json.Marshal(p.status)
	return string(data)
}

func (p *Plugin) saveSnapshot(ctx context.Context, host pluginv1.HostServiceClient, status Status) {
	data, err := json.Marshal(status)
	if err != nil || len(data) > 256*1024 {
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _ = host.KVSet(writeCtx, &pluginv1.KVSetRequest{Namespace: stateNamespace, Key: stateKey, Value: data})
}

func (p *Plugin) loadSnapshot(ctx context.Context) {
	p.mu.RLock()
	host := p.host
	p.mu.RUnlock()
	if host == nil {
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	response, err := host.KVGet(readCtx, &pluginv1.KVGetRequest{Namespace: stateNamespace, Key: stateKey})
	if err != nil || !response.GetFound() {
		return
	}
	var status Status
	if json.Unmarshal(response.GetValue(), &status) == nil {
		p.mu.Lock()
		status.Running = true
		p.status = status
		p.mu.Unlock()
	}
}
