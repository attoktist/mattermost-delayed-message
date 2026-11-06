package main

import (
	"time"

	"github.com/pkg/errors"
)

type configuration struct {
	PollIntervalSeconds int
	MaxAttempts         int
	DefaultTimezone     string
}

func normalizeConfiguration(cfg *configuration) *configuration {
	out := *cfg
	if out.PollIntervalSeconds < minPollIntervalSeconds {
		out.PollIntervalSeconds = minPollIntervalSeconds
	}
	if out.MaxAttempts < 1 {
		out.MaxAttempts = 1
	} else if out.MaxAttempts > maxConfiguredAttempts {
		out.MaxAttempts = maxConfiguredAttempts
	}
	if out.DefaultTimezone == "" {
		out.DefaultTimezone = "UTC"
	} else if _, err := time.LoadLocation(out.DefaultTimezone); err != nil {
		out.DefaultTimezone = "UTC"
	}
	return &out
}

func (p *Plugin) OnConfigurationChange() error {
	cfg := new(configuration)
	if err := p.API.LoadPluginConfiguration(cfg); err != nil {
		return errors.Wrap(err, "failed to load plugin configuration")
	}
	rawTZ := cfg.DefaultTimezone
	normalized := normalizeConfiguration(cfg)
	if normalized.DefaultTimezone != rawTZ {
		p.API.LogWarn("часовой пояс по умолчанию заменён на UTC", "timezone", rawTZ)
	}
	p.setConfiguration(normalized)
	return nil
}
