package monitor

import (
	"net/url"
	"sort"
	"time"
)

const creditsPerUSD = 25.0

type whamDailyUsageResponse struct {
	Data    []OfficialUsageDay `json:"data"`
	GroupBy string             `json:"group_by"`
}

type OfficialUsage struct {
	Days          []OfficialUsageDay `json:"days"`
	TotalCredits  float64            `json:"total_credits"`
	TotalTokens   int64              `json:"total_tokens"`
	TotalTurns    int                `json:"total_turns"`
	CreditsPerUSD float64            `json:"credits_per_usd"`
}

type OfficialUsageDay struct {
	Date    string                `json:"date"`
	Totals  OfficialUsageCounts   `json:"totals"`
	Clients []OfficialUsageClient `json:"clients,omitempty"`
	Models  []OfficialUsageModel  `json:"models,omitempty"`
}

type OfficialUsageCounts struct {
	Users                   int     `json:"users"`
	Threads                 int     `json:"threads"`
	Turns                   int     `json:"turns"`
	Credits                 float64 `json:"credits"`
	UncachedTextInputTokens int64   `json:"uncached_text_input_tokens,omitempty"`
	CachedTextInputTokens   int64   `json:"cached_text_input_tokens,omitempty"`
	TextOutputTokens        int64   `json:"text_output_tokens,omitempty"`
	TextTotalTokens         int64   `json:"text_total_tokens,omitempty"`
}

type OfficialUsageClient struct {
	ClientID string `json:"client_id"`
	OfficialUsageCounts
}

type OfficialUsageModel struct {
	Model string `json:"model"`
	OfficialUsageCounts
}

func buildDailyUsageURL(now time.Time) string {
	end := now.UTC()
	start := end.AddDate(0, 0, -6)
	query := url.Values{}
	query.Set("start_date", start.Format("2006-01-02"))
	query.Set("end_date", end.Format("2006-01-02"))
	query.Set("group_by", "day")
	query.Set("workspace_user", "true")
	return dailyUsageURL + "?" + query.Encode()
}

func summarizeDailyUsage(response whamDailyUsageResponse) *OfficialUsage {
	days := append([]OfficialUsageDay(nil), response.Data...)
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
	status := &OfficialUsage{Days: days, CreditsPerUSD: creditsPerUSD}
	for _, day := range days {
		status.TotalCredits += day.Totals.Credits
		status.TotalTokens += day.Totals.TextTotalTokens
		status.TotalTurns += day.Totals.Turns
	}
	return status
}
