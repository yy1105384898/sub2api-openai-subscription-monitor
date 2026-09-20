package monitor

import (
	"strings"
	"testing"
	"time"
)

func TestBuildDailyUsageURL(t *testing.T) {
	got := buildDailyUsageURL(time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC))
	for _, part := range []string{"start_date=2026-09-14", "end_date=2026-09-20", "group_by=day", "workspace_user=true"} {
		if !strings.Contains(got, part) {
			t.Fatalf("URL %q missing %q", got, part)
		}
	}
}

func TestSummarizeDailyUsage(t *testing.T) {
	status := summarizeDailyUsage(whamDailyUsageResponse{Data: []OfficialUsageDay{
		{Date: "2026-09-20", Totals: OfficialUsageCounts{Credits: 25, TextTotalTokens: 200, Turns: 2}},
		{Date: "2026-09-19", Totals: OfficialUsageCounts{Credits: 12.5, TextTotalTokens: 100, Turns: 1}},
	}})
	if status.TotalCredits != 37.5 || status.TotalTokens != 300 || status.TotalTurns != 3 || status.Days[0].Date != "2026-09-19" {
		t.Fatalf("unexpected status: %+v", status)
	}
}
