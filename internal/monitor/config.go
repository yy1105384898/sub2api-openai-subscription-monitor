package monitor

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

type Config struct {
	IntervalMinutes       int  `json:"interval_minutes"`
	RequestTimeoutSeconds int  `json:"request_timeout_seconds"`
	MaxConcurrency        int  `json:"max_concurrency"`
	IncludeUsage          bool `json:"include_usage"`
	MaskAccountIdentity   bool `json:"mask_account_identity"`
}

func defaultConfig() Config {
	return Config{
		IntervalMinutes:       30,
		RequestTimeoutSeconds: 20,
		MaxConcurrency:        3,
		IncludeUsage:          true,
		MaskAccountIdentity:   true,
	}
}

func parseConfig(raw []byte) (Config, []byte, error) {
	cfg := defaultConfig()
	if len(bytes.TrimSpace(raw)) != 0 && string(bytes.TrimSpace(raw)) != "{}" {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, nil, err
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return Config{}, nil, errors.New("配置只能包含一个 JSON 对象")
		}
	}
	if cfg.IntervalMinutes < 5 || cfg.IntervalMinutes > 1440 {
		return Config{}, nil, errors.New("interval_minutes 必须在 5 到 1440 之间")
	}
	if cfg.RequestTimeoutSeconds < 5 || cfg.RequestTimeoutSeconds > 60 {
		return Config{}, nil, errors.New("request_timeout_seconds 必须在 5 到 60 之间")
	}
	if cfg.MaxConcurrency < 1 || cfg.MaxConcurrency > 10 {
		return Config{}, nil, errors.New("max_concurrency 必须在 1 到 10 之间")
	}
	normalized, err := json.Marshal(cfg)
	return cfg, normalized, err
}
