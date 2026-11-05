package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// poster publishes a scheduled message. plugin.API satisfies it.
type poster interface {
	CreatePost(post *model.Post) (*model.Post, *model.AppError)
}

func (p *Plugin) runScheduler() {
	defer close(p.doneCh)

	interval := time.Duration(p.getConfiguration().PollIntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run once at startup so freshly-due messages are sent without waiting an interval.
	p.sendDueMessages()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			// Re-read config in case PollIntervalSeconds changed.
			newInterval := time.Duration(p.getConfiguration().PollIntervalSeconds) * time.Second
			if newInterval != interval {
				interval = newInterval
				ticker.Reset(interval)
			}
			p.sendDueMessages()
		}
	}
}

// completedRetentionMs — completed recurring series stay around for a week
// so users can confirm "yes the series finished" in the list view.
const completedRetentionMs int64 = 7 * 24 * 60 * 60 * 1000

func (p *Plugin) sendDueMessages() {
	msgs, err := listAllPendingMessages(p.kv)
	if err != nil {
		p.logError("scheduler: failed to list messages", "err", err.Error())
		return
	}
	now := time.Now().UTC().UnixMilli()
	maxAttempts := p.getConfiguration().MaxAttempts

	for _, msg := range msgs {
		// GC completed series after the retention window.
		if msg.Status == StatusCompleted && msg.effectiveFireAt() < now-completedRetentionMs {
			if err := deleteMessage(p.kv, msg.UserID, msg.ID); err != nil {
				p.logWarn("scheduler: gc of completed message failed", "id", msg.ID, "err", err.Error())
			}
			continue
		}
		if msg.Status != StatusPending {
			continue
		}
		if msg.effectiveFireAt() > now {
			continue
		}
		p.dispatch(msg, maxAttempts)
	}
}

func (p *Plugin) dispatch(msg *ScheduledMessage, maxAttempts int) {
	got, err := acquireSendLock(p.kv, msg.ID)
	if err != nil {
		p.logWarn("scheduler: lock error", "id", msg.ID, "err", err.Error())
		return
	}
	if !got {
		// Another node owns this dispatch.
		return
	}
	defer releaseSendLock(p.kv, msg.ID)

	// Re-load after locking — another node may have already processed this.
	current, err := loadMessage(p.kv, msg.UserID, msg.ID)
	if err != nil || current == nil || current.Status != StatusPending {
		return
	}
	// A series stays pending after a successful send; SendAt moves to the next
	// occurrence. Do not post that record again until it is due.
	if current.effectiveFireAt() > time.Now().UTC().UnixMilli() {
		return
	}

	if err := userActive(p.gate, current.UserID); err != nil {
		if errors.Is(err, errUserGone) {
			if delErr := deleteMessage(p.kv, current.UserID, current.ID); delErr != nil {
				p.logError("scheduler: failed to delete message for gone user", "id", current.ID, "err", delErr.Error())
			}
			return
		}
		p.failDispatch(current, maxAttempts, err.Error())
		return
	}

	body, sentIdx := pickMessageBody(current)
	targets := current.Targets()
	if len(targets) == 0 {
		p.logError("scheduler: message has no target channels, dropping", "id", current.ID)
		_ = deleteMessage(p.kv, current.UserID, current.ID)
		return
	}

	var failures []postFailure
	successes := 0
	for _, channelID := range targets {
		if err := checkCanPost(p.gate, current.UserID, channelID); err != nil {
			failures = append(failures, postFailure{channelID: channelID, err: err.Error()})
			continue
		}
		post := &model.Post{
			ChannelId: channelID,
			UserId:    current.UserID,
			Message:   body,
		}
		if _, appErr := p.post.CreatePost(post); appErr != nil {
			failures = append(failures, postFailure{channelID: channelID, err: appErr.Error()})
			continue
		}
		successes++
	}

	// All targets failed → treat as a full dispatch failure and use the
	// existing retry path. The retry will hit every channel again, which is
	// safe because none received the message this round.
	if successes == 0 {
		p.failDispatch(current, maxAttempts, joinFailures(failures))
		return
	}

	// At least one channel received the message. Log any per-target failures
	// individually and treat the dispatch as complete — retrying would
	// duplicate posts in the channels that already succeeded.
	for _, f := range failures {
		p.logError("scheduler: per-target send failed (not retrying)",
			"id", current.ID, "channel_id", f.channelID, "err", f.err)
	}

	if current.Repeat == "" {
		if err := deleteMessage(p.kv, current.UserID, current.ID); err != nil {
			p.logError("scheduler: failed to delete sent message", "id", current.ID, "err", err.Error())
			// Delivery already happened. StatusSent is not dispatched, so a
			// failed delete cannot send the same one-shot message again.
			current.Status = StatusSent
			current.FireAt = 0
			current.LastError = "delivered; delete failed"
			if saveErr := saveMessage(p.kv, current); saveErr != nil {
				p.logError("scheduler: failed to persist sent status after delete failure", "id", current.ID, "err", saveErr.Error())
			}
		}
		return
	}
	p.advanceRecurring(current, sentIdx)
}

// failDispatch is the source all-targets-failed path. A non-gone userActive
// error lands here too: nothing was posted, so the same retry budget applies.
func (p *Plugin) failDispatch(current *ScheduledMessage, maxAttempts int, appErrMsg string) {
	current.Attempts++
	current.LastError = appErrMsg
	if current.Attempts >= maxAttempts {
		if current.Repeat != "" {
			// Recurring: skip this occurrence rather than killing the whole series.
			p.skipFailedOccurrence(current, appErrMsg)
			return
		}
		current.Status = StatusFailed
		p.logError("scheduler: giving up on message",
			"id", current.ID, "attempts", current.Attempts, "err", appErrMsg)
	} else {
		p.logWarn("scheduler: send failed, will retry",
			"id", current.ID, "attempts", current.Attempts, "err", appErrMsg)
	}
	if saveErr := saveMessage(p.kv, current); saveErr != nil {
		p.logError("scheduler: failed to persist failure state", "err", saveErr.Error())
	}
}

// advanceRecurring rolls a successfully-sent recurring message to its next
// occurrence, or marks it completed if the series has ended. When sentIdx
// is non-negative the rotation pos and LastSentIndex are updated for the
// just-sent message.
func (p *Plugin) advanceRecurring(msg *ScheduledMessage, sentIdx int) {
	msg.Occurrences++
	if sentIdx >= 0 {
		idx := sentIdx
		msg.LastSentIndex = &idx
		msg.MessageCyclePos++
	}
	next, err := nextOccurrence(time.UnixMilli(msg.SendAt), msg.Repeat, msg.Timezone)
	if err != nil {
		// Bad recurrence (shouldn't happen — validated at create time) — log & delete.
		p.logError("scheduler: invalid recurrence, dropping", "id", msg.ID, "err", err.Error())
		_ = deleteMessage(p.kv, msg.UserID, msg.ID)
		return
	}
	if seriesEnded(msg, next) {
		msg.Status = StatusCompleted
		msg.Attempts = 0
		msg.LastError = ""
		msg.FireAt = 0
		p.persistAdvanced(msg, "scheduler: failed to mark series completed")
		return
	}
	msg.SendAt = next.UnixMilli()
	msg.FireAt = randomFireAt(msg.SendAt, msg.WindowMs)
	msg.Attempts = 0
	msg.LastError = ""
	p.persistAdvanced(msg, "scheduler: failed to roll forward recurring message")
}

// persistAdvanced writes a record whose SendAt or status was already moved in
// memory. One extra save covers a single KV blip. The in-memory advance is
// not rolled back: restoring the previous SendAt would deliver it again.
func (p *Plugin) persistAdvanced(msg *ScheduledMessage, failLog string) {
	err := saveMessage(p.kv, msg)
	if err == nil {
		return
	}
	p.logError(failLog, "id", msg.ID, "err", err.Error())
	if err = saveMessage(p.kv, msg); err != nil {
		p.logError(failLog, "id", msg.ID, "err", err.Error())
	}
}

// postFailure records a single per-channel CreatePost or permission failure
// during a multi-target dispatch.
type postFailure struct {
	channelID string
	err       string
}

// joinFailures collapses per-channel failure errors into a single string for
// persistence in LastError. Order is preserved so users can map errors back to
// their target order. Each entry is "channelID: err".
func joinFailures(failures []postFailure) string {
	parts := make([]string, 0, len(failures))
	for _, f := range failures {
		parts = append(parts, fmt.Sprintf("%s: %s", f.channelID, f.err))
	}
	return strings.Join(parts, "; ")
}

// skipFailedOccurrence advances a recurring message past an occurrence that
// hit max attempts. The occurrence count is *not* incremented (it didn't send).
func (p *Plugin) skipFailedOccurrence(msg *ScheduledMessage, lastErr string) {
	p.logError("scheduler: skipping failed recurring occurrence",
		"id", msg.ID, "attempts", msg.Attempts, "err", lastErr)
	next, err := nextOccurrence(time.UnixMilli(msg.SendAt), msg.Repeat, msg.Timezone)
	if err != nil {
		p.logError("scheduler: invalid recurrence on skip, dropping", "id", msg.ID, "err", err.Error())
		_ = deleteMessage(p.kv, msg.UserID, msg.ID)
		return
	}
	if seriesEnded(msg, next) {
		msg.Status = StatusCompleted
		msg.FireAt = 0
		if err := saveMessage(p.kv, msg); err != nil {
			p.logError("scheduler: failed to complete after skip", "id", msg.ID, "err", err.Error())
		}
		return
	}
	msg.SendAt = next.UnixMilli()
	msg.FireAt = randomFireAt(msg.SendAt, msg.WindowMs)
	msg.Attempts = 0
	msg.LastError = "skipped: " + lastErr
	if err := saveMessage(p.kv, msg); err != nil {
		p.logError("scheduler: failed to skip occurrence", "id", msg.ID, "err", err.Error())
	}
}

func (p *Plugin) logError(msg string, keyValuePairs ...any) {
	if p.API != nil {
		p.API.LogError(msg, keyValuePairs...)
	}
}

func (p *Plugin) logWarn(msg string, keyValuePairs ...any) {
	if p.API != nil {
		p.API.LogWarn(msg, keyValuePairs...)
	}
}
