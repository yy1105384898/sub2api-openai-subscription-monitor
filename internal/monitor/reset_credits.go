package monitor

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

type resetCreditPayload struct {
	ExpiresAt       string `json:"expires_at,omitempty"`
	ExpiresAtCamel  string `json:"expiresAt,omitempty"`
	ConsumableUntil string `json:"consumable_until,omitempty"`
	ResetType       string `json:"reset_type,omitempty"`
	ResetTypeCamel  string `json:"resetType,omitempty"`
	Status          string `json:"status,omitempty"`
}

type resetCreditsEnvelope struct {
	AvailableCount        json.RawMessage `json:"available_count,omitempty"`
	AvailableCountCamel   json.RawMessage `json:"availableCount,omitempty"`
	Credits               json.RawMessage `json:"credits,omitempty"`
	RateLimitResetCredits json.RawMessage `json:"rate_limit_reset_credits,omitempty"`
	Items                 json.RawMessage `json:"items,omitempty"`
	Data                  json.RawMessage `json:"data,omitempty"`
}

func parseResetCredits(body []byte) (*ResetCreditsStatus, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, nil
	}
	var raw []*resetCreditPayload
	var count *int
	var present bool
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &raw); err != nil {
			return nil, err
		}
		present = true
	} else {
		var envelope resetCreditsEnvelope
		if err := json.Unmarshal(trimmed, &envelope); err != nil {
			return nil, err
		}
		count = parseResetCreditCount(envelope.AvailableCount, envelope.AvailableCountCamel)
		var err error
		raw, present, err = firstResetCreditList(envelope.Credits, envelope.RateLimitResetCredits, envelope.Items, envelope.Data)
		if err != nil {
			return nil, err
		}
	}
	if count == nil && !present {
		return nil, nil
	}
	credits := make([]ResetCreditDetail, 0, len(raw))
	available := 0
	for _, item := range raw {
		if item == nil {
			continue
		}
		resetType := firstNonEmpty(item.ResetType, item.ResetTypeCamel)
		if resetType != "" && !strings.EqualFold(resetType, "codex_rate_limits") {
			continue
		}
		if status := strings.TrimSpace(item.Status); status != "" && !strings.EqualFold(status, "available") {
			continue
		}
		available++
		if expiresAt := firstNonEmpty(item.ConsumableUntil, item.ExpiresAt, item.ExpiresAtCamel); expiresAt != "" {
			credits = append(credits, ResetCreditDetail{ExpiresAt: expiresAt})
		}
	}
	if count != nil {
		available = *count
	}
	return &ResetCreditsStatus{AvailableCount: available, Credits: credits}, nil
}

func parseResetCreditCount(values ...json.RawMessage) *int {
	for _, value := range values {
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			continue
		}
		var count int
		if trimmed[0] == '"' {
			var text string
			if json.Unmarshal(trimmed, &text) != nil {
				continue
			}
			parsed, err := strconv.Atoi(strings.TrimSpace(text))
			if err != nil {
				continue
			}
			count = parsed
		} else if json.Unmarshal(trimmed, &count) != nil {
			continue
		}
		if count >= 0 {
			return &count
		}
	}
	return nil
}

func firstResetCreditList(values ...json.RawMessage) ([]*resetCreditPayload, bool, error) {
	for _, value := range values {
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			continue
		}
		var credits []*resetCreditPayload
		if err := json.Unmarshal(trimmed, &credits); err != nil {
			return nil, false, err
		}
		return credits, true, nil
	}
	return nil, false, nil
}

func mergeResetCredits(current, details *ResetCreditsStatus) *ResetCreditsStatus {
	if details == nil {
		return current
	}
	if current == nil {
		return details
	}
	current.AvailableCount = details.AvailableCount
	if len(details.Credits) > 0 || details.AvailableCount == 0 {
		current.Credits = details.Credits
	}
	return current
}
