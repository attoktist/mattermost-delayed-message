package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}

func TestNextOccurrenceDaily(t *testing.T) {
	syd := mustLoad(t, "Australia/Sydney")
	start := time.Date(2026, 5, 1, 9, 0, 0, 0, syd)
	got, err := nextOccurrence(start, RepeatDaily, "Australia/Sydney")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 5, 2, 9, 0, 0, 0, syd).UTC(), got)
}

func TestNextOccurrenceWeekly(t *testing.T) {
	syd := mustLoad(t, "Australia/Sydney")
	start := time.Date(2026, 5, 4, 9, 0, 0, 0, syd) // Mon
	got, err := nextOccurrence(start, RepeatWeekly, "Australia/Sydney")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 5, 11, 9, 0, 0, 0, syd).UTC(), got)
	assert.Equal(t, time.Monday, got.In(syd).Weekday())
}

func TestNextOccurrenceFortnightly(t *testing.T) {
	utc := time.UTC
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, utc)
	got, err := nextOccurrence(start, RepeatFortnightly, "UTC")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 1, 15, 0, 0, 0, 0, utc), got)
}

func TestNextOccurrenceWeekdays(t *testing.T) {
	syd := mustLoad(t, "Australia/Sydney")
	cases := []struct {
		name     string
		start    time.Time
		expected time.Time
	}{
		{"Mon→Tue", time.Date(2026, 5, 4, 9, 0, 0, 0, syd), time.Date(2026, 5, 5, 9, 0, 0, 0, syd)},
		{"Wed→Thu", time.Date(2026, 5, 6, 9, 0, 0, 0, syd), time.Date(2026, 5, 7, 9, 0, 0, 0, syd)},
		{"Fri→Mon", time.Date(2026, 5, 8, 9, 0, 0, 0, syd), time.Date(2026, 5, 11, 9, 0, 0, 0, syd)},
		{"Sat→Mon", time.Date(2026, 5, 9, 9, 0, 0, 0, syd), time.Date(2026, 5, 11, 9, 0, 0, 0, syd)},
		{"Sun→Mon", time.Date(2026, 5, 10, 9, 0, 0, 0, syd), time.Date(2026, 5, 11, 9, 0, 0, 0, syd)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := nextOccurrence(c.start, RepeatWeekdays, "Australia/Sydney")
			require.NoError(t, err)
			assert.Equal(t, c.expected.UTC(), got)
		})
	}
}

func TestNextOccurrenceYearly(t *testing.T) {
	utc := time.UTC
	cases := []struct {
		name     string
		start    time.Time
		expected time.Time
	}{
		{"plain", time.Date(2026, 6, 1, 9, 0, 0, 0, utc), time.Date(2027, 6, 1, 9, 0, 0, 0, utc)},
		// Feb 29 in a leap year → next year (non-leap) clamps to Feb 28.
		{"leap_to_nonleap", time.Date(2028, 2, 29, 9, 0, 0, 0, utc), time.Date(2029, 2, 28, 9, 0, 0, 0, utc)},
		// Feb 28 in a non-leap year stays Feb 28.
		{"nonleap_to_leap", time.Date(2027, 2, 28, 9, 0, 0, 0, utc), time.Date(2028, 2, 28, 9, 0, 0, 0, utc)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := nextOccurrence(c.start, RepeatYearly, "UTC")
			require.NoError(t, err)
			assert.Equal(t, c.expected, got)
		})
	}
}

func TestNextOccurrenceMonthlyClamp(t *testing.T) {
	utc := time.UTC
	cases := []struct {
		name     string
		start    time.Time
		expected time.Time
	}{
		// Jan 31 → Feb 28 (2026 not a leap year), then back to month-end behaviour.
		{"Jan31→Feb28", time.Date(2026, 1, 31, 9, 0, 0, 0, utc), time.Date(2026, 2, 28, 9, 0, 0, 0, utc)},
		{"Feb28→Mar28", time.Date(2026, 2, 28, 9, 0, 0, 0, utc), time.Date(2026, 3, 28, 9, 0, 0, 0, utc)},
		{"Mar31→Apr30", time.Date(2026, 3, 31, 9, 0, 0, 0, utc), time.Date(2026, 4, 30, 9, 0, 0, 0, utc)},
		{"Apr30→May30", time.Date(2026, 4, 30, 9, 0, 0, 0, utc), time.Date(2026, 5, 30, 9, 0, 0, 0, utc)},
		{"Jan31_leap→Feb29", time.Date(2028, 1, 31, 9, 0, 0, 0, utc), time.Date(2028, 2, 29, 9, 0, 0, 0, utc)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := nextOccurrence(c.start, RepeatMonthly, "UTC")
			require.NoError(t, err)
			assert.Equal(t, c.expected, got)
		})
	}
}

// Australia/Sydney: AEDT (UTC+11) → AEST (UTC+10) on first Sunday in April.
// 2026: DST ends Sunday 5 April 2026 at 03:00 local (rewind to 02:00).
// A weekly Monday 09:00 series spanning that Sunday should remain at 09:00
// Sydney local — meaning the UTC delta is 7×24h + 1h, *not* exactly a week.
func TestNextOccurrenceWeeklyDSTBackwardSydney(t *testing.T) {
	syd := mustLoad(t, "Australia/Sydney")
	// 2026-03-30 is a Monday, before DST ends. The week after (2026-04-06) is after DST ends.
	start := time.Date(2026, 3, 30, 9, 0, 0, 0, syd)
	got, err := nextOccurrence(start, RepeatWeekly, "Australia/Sydney")
	require.NoError(t, err)

	// Local 09:00 Sydney is preserved.
	gotLocal := got.In(syd)
	assert.Equal(t, 9, gotLocal.Hour())
	assert.Equal(t, 0, gotLocal.Minute())
	assert.Equal(t, time.Monday, gotLocal.Weekday())
	assert.Equal(t, time.Date(2026, 4, 6, 9, 0, 0, 0, syd).UTC(), got)

	// UTC delta is *not* exactly 7×24h — it's one hour longer because the
	// Sydney clock fell back one hour during DST end.
	delta := got.Sub(start)
	assert.Equal(t, 7*24*time.Hour+time.Hour, delta, "expected 7×24h + 1h across DST-end")
}

// America/New_York: DST ends first Sunday of November (clocks back). 2026-11-01.
// A weekly Monday 09:00 series spanning that Sunday: 7×24h + 1h delta.
func TestNextOccurrenceWeeklyDSTBackwardNYC(t *testing.T) {
	nyc := mustLoad(t, "America/New_York")
	start := time.Date(2026, 10, 26, 9, 0, 0, 0, nyc) // Mon, EDT
	got, err := nextOccurrence(start, RepeatWeekly, "America/New_York")
	require.NoError(t, err)

	gotLocal := got.In(nyc)
	assert.Equal(t, 9, gotLocal.Hour())
	assert.Equal(t, time.Monday, gotLocal.Weekday())
	assert.Equal(t, 7*24*time.Hour+time.Hour, got.Sub(start))
}

// America/New_York: DST starts second Sunday of March (clocks forward).
// Weekly across that Sunday: delta is 7×24h - 1h.
func TestNextOccurrenceWeeklyDSTForwardNYC(t *testing.T) {
	nyc := mustLoad(t, "America/New_York")
	start := time.Date(2026, 3, 2, 9, 0, 0, 0, nyc) // Mon before 2026-03-08 DST start
	got, err := nextOccurrence(start, RepeatWeekly, "America/New_York")
	require.NoError(t, err)

	gotLocal := got.In(nyc)
	assert.Equal(t, 9, gotLocal.Hour())
	assert.Equal(t, time.Monday, gotLocal.Weekday())
	assert.Equal(t, 7*24*time.Hour-time.Hour, got.Sub(start))
}

func TestSeriesEnded(t *testing.T) {
	cases := []struct {
		name string
		msg  *ScheduledMessage
		next time.Time
		want bool
	}{
		{"never", &ScheduledMessage{EndsMode: EndsNever}, time.Now(), false},
		{"after_below", &ScheduledMessage{EndsMode: EndsAfter, EndsAfter: 3, Occurrences: 1}, time.Now(), false},
		{"after_equal", &ScheduledMessage{EndsMode: EndsAfter, EndsAfter: 3, Occurrences: 3}, time.Now(), true},
		{"after_above", &ScheduledMessage{EndsMode: EndsAfter, EndsAfter: 3, Occurrences: 5}, time.Now(), true},
		{"on_before", &ScheduledMessage{EndsMode: EndsOn, EndsAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).UnixMilli()}, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), false},
		{"on_after", &ScheduledMessage{EndsMode: EndsOn, EndsAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).UnixMilli()}, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, seriesEnded(c.msg, c.next))
		})
	}
}

func TestValidateRecurrence(t *testing.T) {
	require.NoError(t, validateRecurrence(RepeatNone, "", 0, false))
	require.NoError(t, validateRecurrence(RepeatWeekly, EndsNever, 0, false))
	require.NoError(t, validateRecurrence(RepeatDaily, EndsAfter, 5, false))
	require.NoError(t, validateRecurrence(RepeatMonthly, EndsOn, 0, true))

	require.Error(t, validateRecurrence("hourly", "", 0, false), "unknown repeat")
	require.Error(t, validateRecurrence(RepeatNone, EndsAfter, 5, false), "ends fields without repeat")
	require.Error(t, validateRecurrence(RepeatDaily, EndsAfter, 0, false), "ends_after must be >= 1")
	require.Error(t, validateRecurrence(RepeatDaily, EndsOn, 0, false), "ends_on without ends_at")
	require.Error(t, validateRecurrence(RepeatDaily, "weird", 0, false), "invalid ends_mode")
}

func TestValidateSendAtBoundary(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	require.NoError(t, validateSendAt(now.Add(30*time.Second), now))
	require.Error(t, validateSendAt(now.Add(29*time.Second), now))
}
