package main

import (
	"fmt"
	"math/rand"
	"strings"

	"github.com/pkg/errors"
)

// Random sources, indirected through package vars so tests can stub them
// for deterministic shuffles. math/rand's top-level funcs are auto-seeded
// (Go ≥1.20) and safe for concurrent use.
var (
	randIntn   = func(n int) int { return rand.Intn(n) }
	randInt63n = func(n int64) int64 { return rand.Int63n(n) }
)

// maxWindowMs caps the random window to one day. Longer windows would push
// past the next occurrence on a daily schedule, which we don't want.
const maxWindowMs int64 = 24 * 60 * 60 * 1000

// validateExtras checks the time-window and multi-message rotation fields.
// Called alongside validateRecurrence by the API handlers.
func validateExtras(repeat string, windowMs int64, messages []string) error {
	if windowMs < 0 {
		return errors.New("window_ms must be >= 0")
	}
	if windowMs > maxWindowMs {
		return fmt.Errorf("window_ms must be <= %d (24h)", maxWindowMs)
	}
	if len(messages) > 0 {
		if repeat == RepeatNone {
			return errors.New("multiple messages require a repeating schedule")
		}
		if len(messages) < 2 {
			return errors.New("provide at least 2 messages, or use the single-message field")
		}
		for i, m := range messages {
			if strings.TrimSpace(m) == "" {
				return fmt.Errorf("message %d is empty", i+1)
			}
		}
	}
	return nil
}

// randomFireAt returns sendAt + a random offset in [0, windowMs) when
// windowMs > 0, or 0 otherwise (signalling "no random window — use SendAt").
func randomFireAt(sendAt, windowMs int64) int64 {
	if windowMs <= 0 {
		return 0
	}
	return sendAt + randInt63n(windowMs)
}

// newShuffledCycle returns a random permutation of [0..n). If avoid is
// non-nil and in range and the freshly-shuffled cycle would start with that
// index, cycle[0] and cycle[1] are swapped (when n >= 2) so a new cycle
// doesn't immediately repeat the most-recently-sent message.
func newShuffledCycle(n int, avoid *int) []int {
	if n <= 0 {
		return nil
	}
	cycle := make([]int, n)
	for i := range cycle {
		cycle[i] = i
	}
	// Fisher–Yates.
	for i := n - 1; i > 0; i-- {
		j := randIntn(i + 1)
		cycle[i], cycle[j] = cycle[j], cycle[i]
	}
	if avoid != nil && n >= 2 && *avoid >= 0 && *avoid < n && cycle[0] == *avoid {
		cycle[0], cycle[1] = cycle[1], cycle[0]
	}
	return cycle
}

// pickMessageBody decides which message to send for a scheduled message and
// returns the body plus the rotation index used (-1 when there's no rotation).
// For rotating schedules it ensures msg.MessageCycle is valid, regenerating
// it (and resetting MessageCyclePos) whenever the cycle is exhausted, empty,
// or contains indices that are no longer valid (e.g. after an edit shrank
// the Messages list). Mutates msg in place; callers must persist the result.
func pickMessageBody(msg *ScheduledMessage) (string, int) {
	if len(msg.Messages) == 0 {
		return msg.Message, -1
	}
	if needsNewCycle(msg) {
		msg.MessageCycle = newShuffledCycle(len(msg.Messages), msg.LastSentIndex)
		msg.MessageCyclePos = 0
	}
	idx := msg.MessageCycle[msg.MessageCyclePos]
	return msg.Messages[idx], idx
}

func needsNewCycle(msg *ScheduledMessage) bool {
	if len(msg.MessageCycle) == 0 {
		return true
	}
	if msg.MessageCyclePos < 0 || msg.MessageCyclePos >= len(msg.MessageCycle) {
		return true
	}
	for _, i := range msg.MessageCycle {
		if i < 0 || i >= len(msg.Messages) {
			return true
		}
	}
	return false
}

// trimMessages strips whitespace from each entry and drops any empties. A
// nil/empty input returns nil so downstream "len == 0" checks Just Work.
func trimMessages(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, m := range in {
		t := strings.TrimSpace(m)
		if t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// equalStrings reports whether two string slices contain the same values in
// the same order. Used by the API to detect rotation-list changes on update.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
