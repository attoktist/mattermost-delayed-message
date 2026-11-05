package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPollIntervalFloor(t *testing.T) {
	got := normalizeConfiguration(&configuration{
		PollIntervalSeconds: 0,
		MaxAttempts:         0,
		DefaultTimezone:     "Not/AZone",
	})
	require.Equal(t, &configuration{
		PollIntervalSeconds: 5,
		MaxAttempts:         1,
		DefaultTimezone:     "UTC",
	}, got)

	got = normalizeConfiguration(&configuration{
		PollIntervalSeconds: 4,
		MaxAttempts:         11,
		DefaultTimezone:     "Australia/Sydney",
	})
	require.Equal(t, &configuration{
		PollIntervalSeconds: 5,
		MaxAttempts:         10,
		DefaultTimezone:     "Australia/Sydney",
	}, got)

	got = normalizeConfiguration(&configuration{
		PollIntervalSeconds: 30,
		MaxAttempts:         3,
		DefaultTimezone:     "",
	})
	require.Equal(t, &configuration{
		PollIntervalSeconds: 30,
		MaxAttempts:         3,
		DefaultTimezone:     "UTC",
	}, got)

	got = normalizeConfiguration(&configuration{
		PollIntervalSeconds: 5,
		MaxAttempts:         1,
		DefaultTimezone:     "UTC",
	})
	require.Equal(t, &configuration{
		PollIntervalSeconds: 5,
		MaxAttempts:         1,
		DefaultTimezone:     "UTC",
	}, got)

	got = normalizeConfiguration(&configuration{
		PollIntervalSeconds: 6,
		MaxAttempts:         10,
		DefaultTimezone:     "UTC",
	})
	require.Equal(t, &configuration{
		PollIntervalSeconds: 6,
		MaxAttempts:         10,
		DefaultTimezone:     "UTC",
	}, got)
}

func TestPollIntervalNegative(t *testing.T) {
	got := normalizeConfiguration(&configuration{
		PollIntervalSeconds: -1,
		MaxAttempts:         -1,
		DefaultTimezone:     "UTC",
	})
	require.Equal(t, 5, got.PollIntervalSeconds)
	require.Equal(t, 1, got.MaxAttempts)
	require.Equal(t, "UTC", got.DefaultTimezone)
}

func TestGetConfigurationNilSnapshot(t *testing.T) {
	p := &Plugin{}
	require.Equal(t, &configuration{
		PollIntervalSeconds: 30,
		MaxAttempts:         3,
		DefaultTimezone:     "UTC",
	}, p.getConfiguration())

	p.setConfiguration(&configuration{
		PollIntervalSeconds: 15,
		MaxAttempts:         4,
		DefaultTimezone:     "Europe/Moscow",
	})
	require.Equal(t, &configuration{
		PollIntervalSeconds: 15,
		MaxAttempts:         4,
		DefaultTimezone:     "Europe/Moscow",
	}, p.getConfiguration())
}
