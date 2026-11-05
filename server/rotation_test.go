package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withDeterministicRand swaps the package-level random hooks for the duration
// of a test. intnFn / int63nFn can encode any wanted sequence.
func withDeterministicRand(t *testing.T, intnFn func(int) int, int63nFn func(int64) int64) {
	t.Helper()
	prevIntn := randIntn
	prevInt63n := randInt63n
	if intnFn != nil {
		randIntn = intnFn
	}
	if int63nFn != nil {
		randInt63n = int63nFn
	}
	t.Cleanup(func() {
		randIntn = prevIntn
		randInt63n = prevInt63n
	})
}

func TestEffectiveFireAt(t *testing.T) {
	m := &ScheduledMessage{SendAt: 1000}
	assert.Equal(t, int64(1000), m.effectiveFireAt())
	m.FireAt = 1500
	assert.Equal(t, int64(1500), m.effectiveFireAt())
}

func TestNewShuffledCycleIsPermutation(t *testing.T) {
	// Fixed shuffle so the result is reproducible.
	withDeterministicRand(t, func(n int) int { return 0 }, nil)
	c := newShuffledCycle(5, nil)
	require.Len(t, c, 5)
	seen := map[int]bool{}
	for _, v := range c {
		seen[v] = true
	}
	assert.Len(t, seen, 5, "every index must appear exactly once")
}

func TestNewShuffledCycleHandlesEdgeSizes(t *testing.T) {
	assert.Nil(t, newShuffledCycle(0, nil))
	assert.Equal(t, []int{0}, newShuffledCycle(1, nil))
}

func TestNewShuffledCycleAvoidsBoundaryRepeat(t *testing.T) {
	// Pin shuffle to identity so cycle starts as [0,1,2,3].
	withDeterministicRand(t, func(n int) int { return n - 1 }, nil)
	avoid := 0
	c := newShuffledCycle(4, &avoid)
	require.NotEmpty(t, c)
	assert.NotEqual(t, avoid, c[0], "swap should have moved the avoided index off the front")
}

func TestNewShuffledCycleAvoidNoOpWhenSizeOne(t *testing.T) {
	withDeterministicRand(t, func(n int) int { return 0 }, nil)
	avoid := 0
	// With n=1 the swap is impossible and we must just return [0].
	c := newShuffledCycle(1, &avoid)
	assert.Equal(t, []int{0}, c)
}

func TestPickMessageBodySingleMessage(t *testing.T) {
	msg := &ScheduledMessage{Message: "only"}
	body, idx := pickMessageBody(msg)
	assert.Equal(t, "only", body)
	assert.Equal(t, -1, idx)
}

func TestPickMessageBodyBuildsCycleWhenMissing(t *testing.T) {
	withDeterministicRand(t, func(n int) int { return 0 }, nil)
	msg := &ScheduledMessage{Messages: []string{"a", "b", "c"}}
	body, idx := pickMessageBody(msg)
	assert.NotEmpty(t, body)
	assert.GreaterOrEqual(t, idx, 0)
	assert.Len(t, msg.MessageCycle, 3)
	assert.Equal(t, 0, msg.MessageCyclePos)
}

func TestPickMessageBodyRebuildsAfterEditShrunkList(t *testing.T) {
	withDeterministicRand(t, func(n int) int { return 0 }, nil)
	msg := &ScheduledMessage{
		Messages:        []string{"a", "b"},
		MessageCycle:    []int{0, 1, 2}, // stale: contains 2 which is now out of range
		MessageCyclePos: 0,
	}
	_, _ = pickMessageBody(msg)
	for _, i := range msg.MessageCycle {
		assert.Less(t, i, len(msg.Messages))
		assert.GreaterOrEqual(t, i, 0)
	}
}

func TestValidateExtrasRejectsBadInputs(t *testing.T) {
	cases := []struct {
		name    string
		repeat  string
		window  int64
		msgs    []string
		wantErr string
	}{
		{"negative window", "daily", -1, nil, "window_ms"},
		{"window too large", "daily", maxWindowMs + 1, nil, "24h"},
		{"messages without repeat", "", 0, []string{"a", "b"}, "repeating schedule"},
		{"single-entry messages", "daily", 0, []string{"a"}, "at least 2"},
		{"empty message in list", "daily", 0, []string{"a", "   "}, "is empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateExtras(c.repeat, c.window, c.msgs)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
		})
	}
}

func TestValidateExtrasAcceptsValid(t *testing.T) {
	require.NoError(t, validateExtras("", 0, nil))
	require.NoError(t, validateExtras("daily", 30*60*1000, nil))
	require.NoError(t, validateExtras("daily", 0, []string{"a", "b"}))
	require.NoError(t, validateExtras("weekly", maxWindowMs, []string{"a", "b", "c"}))
}

func TestValidateExtrasRejectsThirdMessageWithoutRepeat(t *testing.T) {
	err := validateExtras("", 0, []string{"a", "b"})
	require.Error(t, err)
}

func TestWindowCap(t *testing.T) {
	require.NoError(t, validateExtras("daily", 24*time.Hour.Milliseconds(), nil))
	require.Error(t, validateExtras("daily", 24*time.Hour.Milliseconds()+1, nil))
}

func TestEqualStrings(t *testing.T) {
	assert.True(t, equalStrings(nil, nil))
	assert.True(t, equalStrings([]string{}, []string{}))
	assert.True(t, equalStrings([]string{"a", "b"}, []string{"a", "b"}))
	assert.False(t, equalStrings([]string{"a"}, []string{"a", "b"}))
	assert.False(t, equalStrings([]string{"a", "b"}, []string{"b", "a"}))
}

func TestTrimMessages(t *testing.T) {
	assert.Nil(t, trimMessages(nil))
	assert.Nil(t, trimMessages([]string{"", "   "}))
	assert.Equal(t, []string{"a", "b"}, trimMessages([]string{"  a  ", "", "b"}))
}

func TestRandomFireAt(t *testing.T) {
	withDeterministicRand(t, nil, func(n int64) int64 { return n / 2 })
	got := randomFireAt(1000, 200)
	assert.Equal(t, int64(1100), got)

	// Zero window means "no window" — signal with FireAt = 0.
	assert.Equal(t, int64(0), randomFireAt(1000, 0))
	assert.Equal(t, int64(0), randomFireAt(1000, -1))
}

func TestRoundTripWithAllNewFields(t *testing.T) {
	idx := 2
	orig := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelID: "c1", Message: "primary",
		SendAt: 1_000_000, CreatedAt: 999_999, Status: StatusPending,
		Repeat: RepeatDaily, Timezone: "Australia/Sydney",
		WindowMs: 30 * 60 * 1000, FireAt: 1_500_000,
		Messages: []string{"a", "b", "c"}, MessageCycle: []int{2, 0, 1},
		MessageCyclePos: 1, LastSentIndex: &idx,
	}
	data, err := json.Marshal(orig)
	require.NoError(t, err)

	var got ScheduledMessage
	require.NoError(t, json.Unmarshal(data, &got))
	require.NotNil(t, got.LastSentIndex)
	assert.Equal(t, idx, *got.LastSentIndex)
	assert.Equal(t, orig.MessageCycle, got.MessageCycle)
	assert.Equal(t, orig.MessageCyclePos, got.MessageCyclePos)
	assert.Equal(t, orig.WindowMs, got.WindowMs)
	assert.Equal(t, orig.FireAt, got.FireAt)
	assert.Equal(t, orig.Messages, got.Messages)
}

func TestRoundTripNilLastSentIndexOmitted(t *testing.T) {
	orig := &ScheduledMessage{ID: "m1", UserID: "u1", Message: "x", SendAt: 1}
	data, err := json.Marshal(orig)
	require.NoError(t, err)
	// omitempty pointer should drop the key entirely so it's nil after round-trip.
	assert.NotContains(t, string(data), "last_sent_index")

	var got ScheduledMessage
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Nil(t, got.LastSentIndex)
}
