package monitor

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseResetCredits(t *testing.T) {
	status, err := parseResetCredits([]byte(`{"availableCount":"2","credits":[{"id":"secret","expires_at":"2026-10-01T00:00:00Z"},{"expiresAt":"2026-11-01T00:00:00Z"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if status == nil || status.AvailableCount != 2 || len(status.Credits) != 2 {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.Credits[1].ExpiresAt != "2026-11-01T00:00:00Z" {
		t.Fatalf("unexpected expiry: %+v", status.Credits)
	}
	encoded, err := json.Marshal(status)
	if err != nil || strings.Contains(string(encoded), "secret") {
		t.Fatalf("reset credit ID leaked into snapshot: %s", encoded)
	}
}

func TestParseResetCreditsFiltersUnavailableAndOtherTypes(t *testing.T) {
	status, err := parseResetCredits([]byte(`[{"status":"consumed","expires_at":"2026-10-01T00:00:00Z"},{"reset_type":"other","expires_at":"2026-10-02T00:00:00Z"},{"reset_type":"codex_rate_limits","status":"available","expires_at":"2026-10-03T00:00:00Z"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if status == nil || status.AvailableCount != 1 || len(status.Credits) != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestUsageResponseIncludesOfficialCreditsAndResetCounts(t *testing.T) {
	var usage usageResponse
	err := json.Unmarshal([]byte(`{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":42,"limit_window_seconds":18000},"secondary_window":{"used_percent":18,"limit_window_seconds":604800}},"credits":{"has_credits":true,"balance":"12.50","approx_local_messages":[10,20]},"rate_limit_reset_credits":{"available_count":3,"applicable_available_count":1}}`), &usage)
	if err != nil {
		t.Fatal(err)
	}
	if usage.RateLimit == nil || usage.RateLimit.PrimaryWindow == nil || usage.RateLimit.PrimaryWindow.LimitWindowSeconds != 18000 {
		t.Fatalf("unexpected rate limit: %+v", usage.RateLimit)
	}
	if usage.Credits == nil || usage.Credits.Balance == nil || *usage.Credits.Balance != "12.50" {
		t.Fatalf("unexpected credits: %+v", usage.Credits)
	}
	if usage.RateLimitResetCredits == nil || usage.RateLimitResetCredits.AvailableCount != 3 || usage.RateLimitResetCredits.ApplicableAvailableCount != 1 {
		t.Fatalf("unexpected reset credits: %+v", usage.RateLimitResetCredits)
	}
}
