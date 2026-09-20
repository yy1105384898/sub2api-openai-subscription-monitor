package monitor

import (
	"encoding/json"
	"testing"
)

func TestParseConfigDefaults(t *testing.T) {
	cfg, normalized, err := parseConfig([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IntervalMinutes != 30 || cfg.RequestTimeoutSeconds != 20 || cfg.MaxConcurrency != 3 || !cfg.IncludeUsage || !cfg.MaskAccountIdentity {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if !json.Valid(normalized) {
		t.Fatal("normalized config is not valid JSON")
	}
}

func TestParseConfigRejectsUnknownAndUnsafeValues(t *testing.T) {
	for _, raw := range []string{
		`{"interval_minutes":1,"request_timeout_seconds":20,"max_concurrency":3,"include_usage":true}`,
		`{"interval_minutes":30,"request_timeout_seconds":20,"max_concurrency":3,"include_usage":true,"token":"secret"}`,
		`{} {}`,
	} {
		if _, _, err := parseConfig([]byte(raw)); err == nil {
			t.Fatalf("expected error for %s", raw)
		}
	}
}

func TestParseConfigAllowsDisablingIdentityMask(t *testing.T) {
	cfg, _, err := parseConfig([]byte(`{"interval_minutes":30,"request_timeout_seconds":20,"max_concurrency":3,"include_usage":true,"mask_account_identity":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaskAccountIdentity {
		t.Fatal("identity mask should be disabled")
	}
}

func TestMasking(t *testing.T) {
	if got := maskEmail("alice@example.com"); got != "a***@example.com" {
		t.Fatalf("maskEmail = %q", got)
	}
	if got := maskID("acct-1234567890"); got != "acct...7890" {
		t.Fatalf("maskID = %q", got)
	}
}

func TestIdentityDisplayCanBeUnmasked(t *testing.T) {
	if got := displayEmail(" alice@example.com ", false); got != "alice@example.com" {
		t.Fatalf("displayEmail = %q", got)
	}
	if got := displayID(" acct-1234567890 ", false); got != "acct-1234567890" {
		t.Fatalf("displayID = %q", got)
	}
}

func TestSelectAccountPrefersExactID(t *testing.T) {
	root := map[string]any{"accounts": map[string]any{
		"free": map[string]any{"account": map[string]any{"account_id": "a-free", "plan_type": "free"}},
		"paid": map[string]any{"account": map[string]any{"account_id": "a-paid", "plan_type": "plus"}},
	}}
	id, account := selectAccount(root, "a-paid")
	if id != "a-paid" || nestedString(account, "account", "plan_type") != "plus" {
		t.Fatalf("selected %q: %#v", id, account)
	}
}
