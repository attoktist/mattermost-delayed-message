package main

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// otherChannelID is a second model.IsValidId value. validChannelID lives in access_test.go.
const otherChannelID = "bbbbbbbbbbbbbbbbbbbbbbbbbb"

// fakePoster records every CreatePost argument. The scheduler must pass a post
// with only ChannelId, UserId, and Message set.
type fakePoster struct {
	mu    sync.Mutex
	posts []*model.Post
	err   *model.AppError
	errBy map[string]*model.AppError
}

func (f *fakePoster) CreatePost(post *model.Post) (*model.Post, *model.AppError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, post)
	if f.errBy != nil {
		if appErr, ok := f.errBy[post.ChannelId]; ok && appErr != nil {
			return nil, appErr
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return &model.Post{Id: "post"}, nil
}

func (f *fakePoster) snapshot() []*model.Post {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*model.Post, len(f.posts))
	copy(out, f.posts)
	return out
}

// schedGate is a postGate with a per-channel permission map.
// A nil allow map permits every channel. A nil user with no error is a live user.
type schedGate struct {
	user    *model.User
	userErr *model.AppError
	allow   map[string]bool
}

func (g *schedGate) GetChannel(string) (*model.Channel, *model.AppError) {
	return &model.Channel{}, nil
}

func (g *schedGate) HasPermissionToChannel(_, channelID string, _ *model.Permission) bool {
	if g.allow == nil {
		return true
	}
	return g.allow[channelID]
}

func (g *schedGate) GetUser(string) (*model.User, *model.AppError) {
	if g.user == nil && g.userErr == nil {
		return &model.User{}, nil
	}
	return g.user, g.userErr
}

func (g *schedGate) GetConfig() *model.Config { return nil }

var (
	_ poster   = (*fakePoster)(nil)
	_ postGate = (*schedGate)(nil)
)

func schedPlugin(gate postGate, post *fakePoster) (*Plugin, *fakeKV, *fakePoster) {
	if gate == nil {
		gate = &schedGate{}
	}
	if post == nil {
		post = &fakePoster{}
	}
	kv := newFakeKV()
	p := &Plugin{gate: gate, kv: kv, post: post}
	p.setConfiguration(&configuration{PollIntervalSeconds: 30, MaxAttempts: 3, DefaultTimezone: "UTC"})
	return p, kv, post
}

func mustSave(t *testing.T, kv *fakeKV, msg *ScheduledMessage) {
	t.Helper()
	require.NoError(t, saveMessage(kv, msg))
}

func loadSaved(t *testing.T, kv *fakeKV, userID, msgID string) *ScheduledMessage {
	t.Helper()
	got, err := loadMessage(kv, userID, msgID)
	require.NoError(t, err)
	return got
}

func duePending(id string, channels []string, message string) *ScheduledMessage {
	return &ScheduledMessage{
		ID:         id,
		UserID:     "u1",
		ChannelIDs: channels,
		Message:    message,
		SendAt:     time.Now().Add(-time.Second).UnixMilli(),
		Status:     StatusPending,
	}
}

// failingKV fails one message-key delete and the next N message-key sets.
// Lock keys are untouched, so the send lock still releases.
type failingKV struct {
	*fakeKV
	deleteErr *model.AppError
	failSets  int
}

func (f *failingKV) KVDelete(key string) *model.AppError {
	if f.deleteErr != nil && strings.HasPrefix(key, keyPrefix) {
		err := f.deleteErr
		f.deleteErr = nil
		return err
	}
	return f.fakeKV.KVDelete(key)
}

func (f *failingKV) KVSet(key string, value []byte) *model.AppError {
	if f.failSets > 0 && strings.HasPrefix(key, keyPrefix) {
		f.failSets--
		return model.NewAppError("KVSet", "kv.write_failed", nil, "", 500)
	}
	return f.fakeKV.KVSet(key, value)
}

func TestDispatchOneShotDeleteFailureDoesNotResend(t *testing.T) {
	p, kv, post := schedPlugin(nil, nil)
	msg := duePending("m1", []string{validChannelID}, "hi")
	msg.FireAt = msg.SendAt
	mustSave(t, kv, msg)
	p.kv = &failingKV{
		fakeKV:    kv,
		deleteErr: model.NewAppError("KVDelete", "kv.delete_failed", nil, "", 500),
	}

	p.dispatch(msg, 3)
	p.dispatch(msg, 3)

	require.Len(t, post.snapshot(), 1)
	saved := loadSaved(t, kv, "u1", "m1")
	require.Equal(t, StatusSent, saved.Status)
	require.Equal(t, int64(0), saved.FireAt)
	require.Equal(t, "delivered; delete failed", saved.LastError)
}

func TestDispatchSeriesSaveRetryDoesNotResend(t *testing.T) {
	p, kv, post := schedPlugin(nil, nil)
	start := time.Now().UTC().Add(-time.Second)
	msg := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelIDs: []string{validChannelID}, Message: "stand-up",
		SendAt: start.UnixMilli(), Status: StatusPending,
		Repeat: RepeatDaily, Timezone: "UTC",
	}
	mustSave(t, kv, msg)
	p.kv = &failingKV{fakeKV: kv, failSets: 1}

	p.dispatch(msg, 3)
	p.dispatch(msg, 3)

	require.Len(t, post.snapshot(), 1)
	saved := loadSaved(t, kv, "u1", "m1")
	next, err := nextOccurrence(start, RepeatDaily, "UTC")
	require.NoError(t, err)
	require.Equal(t, next.UnixMilli(), saved.SendAt)
	require.NotEqual(t, start.UnixMilli(), saved.SendAt)
	require.Equal(t, StatusPending, saved.Status)
	require.Equal(t, 1, saved.Occurrences)
}

func TestDispatchDeletesGoneUser(t *testing.T) {
	gate := &schedGate{user: &model.User{DeleteAt: 1}}
	p, kv, post := schedPlugin(gate, nil)
	mustSave(t, kv, duePending("m1", []string{validChannelID}, "hi"))

	p.dispatch(&ScheduledMessage{ID: "m1", UserID: "u1", Status: StatusPending}, 3)

	require.Empty(t, post.snapshot())
	require.Nil(t, loadSaved(t, kv, "u1", "m1"))
	lock, appErr := kv.KVGet("lock_m1")
	require.Nil(t, appErr)
	require.Nil(t, lock)
}

func TestDispatchSkipsChannelWithoutPermission(t *testing.T) {
	gate := &schedGate{allow: map[string]bool{otherChannelID: true}}
	p, kv, post := schedPlugin(gate, nil)
	msg := duePending("m1", []string{validChannelID, otherChannelID}, "body-text")
	mustSave(t, kv, msg)

	p.dispatch(msg, 3)

	posts := post.snapshot()
	require.Len(t, posts, 1)
	require.Equal(t, "u1", posts[0].UserId)
	require.Equal(t, otherChannelID, posts[0].ChannelId)
	require.Equal(t, "body-text", posts[0].Message)
	require.Nil(t, posts[0].Props)
	require.Nil(t, loadSaved(t, kv, "u1", "m1"))
}

func TestDispatchRetriesWhenAllTargetsFail(t *testing.T) {
	gate := &schedGate{allow: map[string]bool{}}
	p, kv, post := schedPlugin(gate, nil)
	msg := duePending("m1", []string{validChannelID, otherChannelID}, "hi")
	mustSave(t, kv, msg)

	p.dispatch(msg, 3)

	require.Empty(t, post.snapshot())
	saved := loadSaved(t, kv, "u1", "m1")
	require.NotNil(t, saved)
	require.Equal(t, 1, saved.Attempts)
	require.Equal(t, StatusPending, saved.Status)
	require.Contains(t, saved.LastError, validChannelID)
	require.Contains(t, saved.LastError, otherChannelID)
}

func TestDispatchRetriesWhenUserLookupFails(t *testing.T) {
	gate := &schedGate{userErr: &model.AppError{StatusCode: 500, Message: "boom"}}
	p, kv, post := schedPlugin(gate, nil)
	mustSave(t, kv, duePending("m1", []string{validChannelID}, "hi"))

	p.dispatch(&ScheduledMessage{ID: "m1", UserID: "u1", Status: StatusPending}, 3)

	require.Empty(t, post.snapshot())
	saved := loadSaved(t, kv, "u1", "m1")
	require.NotNil(t, saved)
	require.Equal(t, 1, saved.Attempts)
	require.Equal(t, StatusPending, saved.Status)
	require.NotEmpty(t, saved.LastError)
}

func TestDispatchDoesNotCopyProps(t *testing.T) {
	p, kv, post := schedPlugin(nil, nil)
	// Keys the scheduled-message struct does not store must not leak into Post.
	raw := []byte(`{"id":"m1","user_id":"u1","channel_ids":["` + validChannelID + `"],"message":"hello","status":"pending","send_at":1,"props":{"from_plugin":"x"},"type":"custom","file_ids":["f1"],"pending_post_id":"pend"}`)
	require.Nil(t, kv.KVSet(messageKey("u1", "m1"), raw))

	p.dispatch(&ScheduledMessage{ID: "m1", UserID: "u1", Status: StatusPending}, 3)

	posts := post.snapshot()
	require.Len(t, posts, 1)
	got := posts[0]
	require.Equal(t, validChannelID, got.ChannelId)
	require.Equal(t, "u1", got.UserId)
	require.Equal(t, "hello", got.Message)
	require.Nil(t, got.Props)
	require.Empty(t, got.Type)
	require.Empty(t, got.FileIds)
	require.Empty(t, got.PendingPostId)
	require.Nil(t, loadSaved(t, kv, "u1", "m1"))
}

func TestDispatchSkipsWhenLockUnavailable(t *testing.T) {
	p, kv, post := schedPlugin(nil, nil)
	msg := duePending("m1", []string{validChannelID}, "hi")
	mustSave(t, kv, msg)
	got, err := acquireSendLock(kv, "m1")
	require.NoError(t, err)
	require.True(t, got)

	p.dispatch(msg, 3)

	require.Empty(t, post.snapshot())
	require.NotNil(t, loadSaved(t, kv, "u1", "m1"))
	lock, appErr := kv.KVGet("lock_m1")
	require.Nil(t, appErr)
	require.Equal(t, []byte("1"), lock)
}

func TestDispatchSkipsWhenStatusChanged(t *testing.T) {
	p, kv, post := schedPlugin(nil, nil)
	msg := duePending("m1", []string{validChannelID}, "hi")
	msg.Status = StatusFailed
	mustSave(t, kv, msg)

	p.dispatch(&ScheduledMessage{ID: "m1", UserID: "u1", Status: StatusPending}, 3)

	require.Empty(t, post.snapshot())
	saved := loadSaved(t, kv, "u1", "m1")
	require.Equal(t, StatusFailed, saved.Status)
}

func TestDispatchMarksFailedAtMaxAttempts(t *testing.T) {
	p, kv, post := schedPlugin(nil, &fakePoster{
		err: model.NewAppError("CreatePost", "boom", nil, "channel deleted", 500),
	})
	msg := duePending("m1", []string{validChannelID}, "hi")
	msg.Attempts = 2
	mustSave(t, kv, msg)

	p.dispatch(msg, 3)

	require.Len(t, post.snapshot(), 1)
	saved := loadSaved(t, kv, "u1", "m1")
	require.Equal(t, 3, saved.Attempts)
	require.Equal(t, StatusFailed, saved.Status)
	require.NotEmpty(t, saved.LastError)
}

func TestDispatchDropsMessageWithNoTargets(t *testing.T) {
	p, kv, post := schedPlugin(nil, nil)
	mustSave(t, kv, duePending("m1", nil, "hi"))

	p.dispatch(&ScheduledMessage{ID: "m1", UserID: "u1", Status: StatusPending}, 3)

	require.Empty(t, post.snapshot())
	require.Nil(t, loadSaved(t, kv, "u1", "m1"))
}

func TestDispatchPartialSuccessAdvancesSeries(t *testing.T) {
	gate := &schedGate{allow: map[string]bool{otherChannelID: true}}
	p, kv, post := schedPlugin(gate, nil)
	start := time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC)
	msg := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelIDs: []string{validChannelID, otherChannelID},
		Message: "hi", SendAt: start.UnixMilli(), Status: StatusPending,
		Repeat: RepeatDaily, Timezone: "UTC",
	}
	mustSave(t, kv, msg)

	p.dispatch(msg, 3)

	require.Len(t, post.snapshot(), 1)
	saved := loadSaved(t, kv, "u1", "m1")
	require.Equal(t, StatusPending, saved.Status)
	require.Equal(t, 1, saved.Occurrences)
	require.Equal(t, 0, saved.Attempts)
	require.Equal(t, start.AddDate(0, 0, 1).UnixMilli(), saved.SendAt)
	require.Empty(t, saved.LastError)
}

func TestDispatchRecurringRollsForward(t *testing.T) {
	p, kv, post := schedPlugin(nil, nil)
	syd := mustLoad(t, "Australia/Sydney")
	sendAt := time.Date(2026, 5, 4, 9, 0, 0, 0, syd)
	msg := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelIDs: []string{validChannelID}, Message: "stand-up",
		SendAt: sendAt.UnixMilli(), Status: StatusPending,
		Repeat: RepeatWeekly, Timezone: "Australia/Sydney", EndsMode: EndsNever,
	}
	mustSave(t, kv, msg)

	p.dispatch(msg, 3)

	require.Len(t, post.snapshot(), 1)
	saved := loadSaved(t, kv, "u1", "m1")
	expectedNext := time.Date(2026, 5, 11, 9, 0, 0, 0, syd).UTC().UnixMilli()
	require.Equal(t, expectedNext, saved.SendAt)
	require.Equal(t, 1, saved.Occurrences)
	require.Equal(t, StatusPending, saved.Status)
	require.Equal(t, 0, saved.Attempts)
	require.Empty(t, saved.LastError)
}

func TestDispatchRecurringEndsAfterN(t *testing.T) {
	p, kv, post := schedPlugin(nil, nil)
	msg := duePending("m1", []string{validChannelID}, "x")
	msg.Repeat = RepeatDaily
	msg.Timezone = "UTC"
	msg.EndsMode = EndsAfter
	msg.EndsAfter = 3
	msg.Occurrences = 2
	mustSave(t, kv, msg)

	p.dispatch(msg, 3)

	require.Len(t, post.snapshot(), 1)
	saved := loadSaved(t, kv, "u1", "m1")
	require.Equal(t, StatusCompleted, saved.Status)
	require.Equal(t, 3, saved.Occurrences)
	require.Equal(t, int64(0), saved.FireAt)
}

func TestDispatchRecurringPermFailureSkipsOccurrence(t *testing.T) {
	p, kv, post := schedPlugin(nil, &fakePoster{
		err: model.NewAppError("CreatePost", "channel deleted", nil, "", 500),
	})
	msg := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelIDs: []string{validChannelID}, Message: "x",
		SendAt:   time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC).UnixMilli(),
		Status:   StatusPending,
		Attempts: 1,
		Repeat:   RepeatDaily,
		Timezone: "UTC",
	}
	mustSave(t, kv, msg)

	p.dispatch(msg, 2)

	require.Len(t, post.snapshot(), 1)
	saved := loadSaved(t, kv, "u1", "m1")
	require.Equal(t, StatusPending, saved.Status)
	require.Equal(t, time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC).UnixMilli(), saved.SendAt)
	require.Equal(t, 0, saved.Attempts)
	require.Equal(t, 0, saved.Occurrences)
	require.Contains(t, saved.LastError, "skipped: ")
}

func TestDispatchPicksRotatingMessage(t *testing.T) {
	withDeterministicRand(t, func(n int) int { return n - 1 }, func(int64) int64 { return 0 })
	p, kv, post := schedPlugin(nil, nil)
	msg := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelIDs: []string{validChannelID}, Message: "alpha",
		Messages: []string{"alpha", "beta", "gamma"},
		SendAt:   time.Now().Add(-time.Second).UnixMilli(),
		Status:   StatusPending, Repeat: RepeatDaily, Timezone: "UTC",
	}
	mustSave(t, kv, msg)

	p.dispatch(msg, 3)

	posts := post.snapshot()
	require.Len(t, posts, 1)
	require.Equal(t, "alpha", posts[0].Message)
	saved := loadSaved(t, kv, "u1", "m1")
	require.Equal(t, 1, saved.MessageCyclePos)
	require.NotNil(t, saved.LastSentIndex)
	require.Equal(t, 0, *saved.LastSentIndex)
	require.Equal(t, 1, saved.Occurrences)
}

func TestRotationCyclesAllBeforeRepeating(t *testing.T) {
	withDeterministicRand(t, func(n int) int { return n - 1 }, func(int64) int64 { return 0 })
	p, kv, post := schedPlugin(nil, nil)
	msg := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelIDs: []string{validChannelID}, Message: "alpha",
		Messages: []string{"alpha", "beta", "gamma"},
		SendAt:   time.Now().Add(-time.Second).UnixMilli(),
		Status:   StatusPending, Repeat: RepeatDaily, Timezone: "UTC",
	}
	mustSave(t, kv, msg)

	for i := 0; i < 6; i++ {
		if i > 0 {
			saved := loadSaved(t, kv, "u1", "m1")
			saved.SendAt = time.Now().Add(-time.Second).UnixMilli()
			saved.FireAt = 0
			mustSave(t, kv, saved)
		}
		p.dispatch(msg, 3)
	}

	posts := post.snapshot()
	require.Len(t, posts, 6)
	sent := make([]string, len(posts))
	for i, post := range posts {
		sent[i] = post.Message
	}
	cycle1 := map[string]struct{}{sent[0]: {}, sent[1]: {}, sent[2]: {}}
	cycle2 := map[string]struct{}{sent[3]: {}, sent[4]: {}, sent[5]: {}}
	require.Len(t, cycle1, 3)
	require.Len(t, cycle2, 3)
	for i := 1; i < len(sent); i++ {
		require.NotEqual(t, sent[i-1], sent[i])
	}
}

func TestAdvanceRecurringRerandomisesFireAtWithinWindow(t *testing.T) {
	withDeterministicRand(t, func(int) int { return 0 }, func(n int64) int64 { return n / 4 })
	p, kv, _ := schedPlugin(nil, nil)
	start := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
	msg := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelIDs: []string{validChannelID}, Message: "hello",
		SendAt: start, Status: StatusPending, Repeat: RepeatDaily, Timezone: "UTC",
		WindowMs: 60 * 60 * 1000, FireAt: start + 30*60*1000,
	}

	p.advanceRecurring(msg, -1)

	saved := loadSaved(t, kv, "u1", "m1")
	expectedNext := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC).UnixMilli()
	require.Equal(t, expectedNext, saved.SendAt)
	require.Equal(t, expectedNext+(60*60*1000)/4, saved.FireAt)
	require.GreaterOrEqual(t, saved.FireAt-saved.SendAt, int64(0))
	require.Less(t, saved.FireAt-saved.SendAt, msg.WindowMs)
}

func TestAdvanceRecurringClearsFireAtWhenSeriesEnds(t *testing.T) {
	p, kv, _ := schedPlugin(nil, nil)
	start := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
	msg := &ScheduledMessage{
		ID: "m1", UserID: "u1", Message: "hi",
		SendAt: start, Status: StatusPending,
		Repeat: RepeatDaily, Timezone: "UTC",
		EndsMode: EndsAfter, EndsAfter: 1, Occurrences: 0,
		WindowMs: 60 * 60 * 1000, FireAt: start + 100,
	}

	p.advanceRecurring(msg, -1)

	saved := loadSaved(t, kv, "u1", "m1")
	require.Equal(t, StatusCompleted, saved.Status)
	require.Equal(t, int64(0), saved.FireAt)
	require.Equal(t, 1, saved.Occurrences)
}

func TestSendDueMessagesGCsCompletedAfterRetention(t *testing.T) {
	p, kv, post := schedPlugin(nil, nil)
	old := &ScheduledMessage{
		ID: "old", UserID: "u1", Status: StatusCompleted,
		SendAt: time.Now().Add(-8 * 24 * time.Hour).UnixMilli(),
	}
	fresh := &ScheduledMessage{
		ID: "fresh", UserID: "u1", Status: StatusCompleted,
		SendAt: time.Now().Add(-time.Hour).UnixMilli(),
	}
	mustSave(t, kv, old)
	mustSave(t, kv, fresh)

	p.sendDueMessages()

	require.Empty(t, post.snapshot())
	require.Nil(t, loadSaved(t, kv, "u1", "old"))
	require.NotNil(t, loadSaved(t, kv, "u1", "fresh"))
}

func TestSendDueMessagesRespectsFireAt(t *testing.T) {
	p, kv, post := schedPlugin(nil, nil)
	now := time.Now().UTC()
	msg := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelIDs: []string{validChannelID}, Message: "later",
		SendAt:   now.Add(-time.Hour).UnixMilli(),
		WindowMs: int64(2 * time.Hour / time.Millisecond),
		FireAt:   now.Add(time.Hour).UnixMilli(),
		Status:   StatusPending,
	}
	mustSave(t, kv, msg)

	p.sendDueMessages()

	require.Empty(t, post.snapshot())
	require.NotNil(t, loadSaved(t, kv, "u1", "m1"))
	lock, appErr := kv.KVGet("lock_m1")
	require.Nil(t, appErr)
	require.Nil(t, lock)
}

func TestSendDueMessagesOnlyDispatchesDueAndPending(t *testing.T) {
	p, kv, post := schedPlugin(nil, nil)
	now := time.Now().UTC()
	due := duePending("due", []string{validChannelID}, "go")
	future := duePending("future", []string{validChannelID}, "later")
	future.SendAt = now.Add(time.Hour).UnixMilli()
	failed := duePending("failed", []string{validChannelID}, "no")
	failed.SendAt = now.Add(-time.Hour).UnixMilli()
	failed.Status = StatusFailed
	mustSave(t, kv, due)
	mustSave(t, kv, future)
	mustSave(t, kv, failed)

	p.sendDueMessages()

	posts := post.snapshot()
	require.Len(t, posts, 1)
	require.Equal(t, "go", posts[0].Message)
	require.Nil(t, loadSaved(t, kv, "u1", "due"))
	require.Equal(t, "later", loadSaved(t, kv, "u1", "future").Message)
	require.Equal(t, StatusFailed, loadSaved(t, kv, "u1", "failed").Status)
}

func TestSchedulerDoesNotMutateConfiguration(t *testing.T) {
	p, _, _ := schedPlugin(nil, nil)
	cfg := p.getConfiguration()
	snapshot := *cfg
	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})

	go p.runScheduler()
	require.NoError(t, stopWithin(p, 2*time.Second))

	require.Same(t, cfg, p.getConfiguration())
	require.Equal(t, snapshot, *cfg)
}

func TestRunSchedulerSendsBeforeLoop(t *testing.T) {
	p, kv, post := schedPlugin(nil, nil)
	mustSave(t, kv, duePending("m1", []string{validChannelID}, "now"))
	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})

	go p.runScheduler()

	require.Eventually(t, func() bool {
		return len(post.snapshot()) == 1
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, "now", post.snapshot()[0].Message)
	require.Nil(t, post.snapshot()[0].Props)
	require.NoError(t, stopWithin(p, 2*time.Second))
	require.Nil(t, loadSaved(t, kv, "u1", "m1"))
}

func stopWithin(p *Plugin, d time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- p.OnDeactivate() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		return errStopTimeout
	}
}

var errStopTimeout = errors.New("OnDeactivate did not return")
