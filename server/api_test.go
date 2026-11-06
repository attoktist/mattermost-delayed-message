package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// apiFake is the stand-in for plugin.API on the HTTP path. Tests assign it
// to Plugin.gate and Plugin.kv. CreatePost is counted so a create that
// publishes immediately fails the test; the handler has no poster.
type apiFake struct {
	*fakeKV
	*fakeGate
	kvOps       int
	createPosts int
	lastPost    *model.Post
	status      *model.Status
	statusErr   *model.AppError
	kvSetFail   *model.AppError // non-nil → KVSet returns this (for 500 body tests)
}

func (f *apiFake) KVSet(key string, value []byte) *model.AppError {
	f.kvOps++
	if f.kvSetFail != nil {
		return f.kvSetFail
	}
	return f.fakeKV.KVSet(key, value)
}

func (f *apiFake) KVGet(key string) ([]byte, *model.AppError) {
	f.kvOps++
	return f.fakeKV.KVGet(key)
}

func (f *apiFake) KVDelete(key string) *model.AppError {
	f.kvOps++
	return f.fakeKV.KVDelete(key)
}

func (f *apiFake) KVList(page, count int) ([]string, *model.AppError) {
	f.kvOps++
	return f.fakeKV.KVList(page, count)
}

func (f *apiFake) KVSetWithOptions(key string, value []byte, options model.PluginKVSetOptions) (bool, *model.AppError) {
	f.kvOps++
	return f.fakeKV.KVSetWithOptions(key, value, options)
}

func (f *apiFake) LogWarn(msg string, keyValuePairs ...interface{}) {
	f.kvOps++
	f.fakeKV.LogWarn(msg, keyValuePairs...)
}

func (f *apiFake) CreatePost(post *model.Post) (*model.Post, *model.AppError) {
	f.createPosts++
	if post != nil {
		copied := *post
		f.lastPost = &copied
	}
	return &model.Post{}, nil
}

func (f *apiFake) GetUserStatus(string) (*model.Status, *model.AppError) {
	return f.status, f.statusErr
}

var (
	_ kvAPI    = (*apiFake)(nil)
	_ postGate = (*apiFake)(nil)
)

func newAPIPlugin(channel *model.Channel, allow bool) (*Plugin, *apiFake) {
	fake := &apiFake{
		fakeKV:   newFakeKV(),
		fakeGate: &fakeGate{allow: allow, channel: channel},
	}
	return &Plugin{gate: fake, kv: fake}, fake
}

func futureSendAt() string {
	return time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
}

func callAPI(p *Plugin, method, path, userID string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if userID != "" {
		req.Header.Set("Mattermost-User-Id", userID)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(nil, rec, req)
	return rec
}

func callJSON(t *testing.T, p *Plugin, method, path, userID string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return callAPI(p, method, path, userID, bytes.NewReader(data))
}

func createBody(channelIDs []string, message string, extra map[string]any) map[string]any {
	body := map[string]any{
		"channel_ids": channelIDs,
		"message":     message,
		"send_at":     futureSendAt(),
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func seedPending(t *testing.T, kv kvAPI, userID, channelID string, n, occurrences int) string {
	t.Helper()
	var id string
	sendAt := time.Now().UTC().Add(time.Hour).UnixMilli()
	for i := 0; i < n; i++ {
		id = model.NewId()
		require.NoError(t, saveMessage(kv, &ScheduledMessage{
			ID:          id,
			UserID:      userID,
			ChannelIDs:  []string{channelID},
			Message:     "seed",
			SendAt:      sendAt,
			CreatedAt:   42,
			Status:      StatusPending,
			Occurrences: occurrences,
		}))
	}
	return id
}

func TestCreateIgnoresBodyUserID(t *testing.T) {
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{}, true)
	rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody(
		[]string{channelID},
		"hello",
		map[string]any{"user_id": "user-b"},
	))
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Zero(t, fake.createPosts)

	stored, err := listMessagesForUser(fake, "user-a")
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.Equal(t, "user-a", stored[0].UserID)
	require.Equal(t, []string{channelID}, stored[0].ChannelIDs)
	require.Equal(t, StatusPending, stored[0].Status)

	other, err := listMessagesForUser(fake, "user-b")
	require.NoError(t, err)
	require.Empty(t, other)
}

func TestChannelCountLimit(t *testing.T) {
	t.Run("ten channels", func(t *testing.T) {
		ids := make([]string, maxChannelsPerMessage)
		for i := range ids {
			ids[i] = model.NewId()
		}
		p, fake := newAPIPlugin(&model.Channel{}, true)
		rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody(ids, "hello", nil))
		require.Equal(t, http.StatusCreated, rec.Code)

		stored, err := listMessagesForUser(fake, "user-a")
		require.NoError(t, err)
		require.Len(t, stored, 1)
		require.Equal(t, ids, stored[0].ChannelIDs)
	})

	t.Run("eleven channels", func(t *testing.T) {
		ids := make([]string, maxChannelsPerMessage+1)
		for i := range ids {
			ids[i] = model.NewId()
		}
		p, fake := newAPIPlugin(&model.Channel{}, true)
		rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody(ids, "hello", nil))
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Empty(t, fake.data)
	})

	t.Run("duplicate counts once", func(t *testing.T) {
		id := model.NewId()
		p, fake := newAPIPlugin(&model.Channel{}, true)
		rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody([]string{id, id}, "hello", nil))
		require.Equal(t, http.StatusCreated, rec.Code)

		stored, err := listMessagesForUser(fake, "user-a")
		require.NoError(t, err)
		require.Len(t, stored, 1)
		require.Equal(t, []string{id}, stored[0].ChannelIDs)
	})
}

func TestMessageSizeLimit(t *testing.T) {
	// maxPostSize always returns PostMessageMaxRunesV2. The boundary is
	// runes, so a multi-byte rune must not be measured with len(string).
	const limit = 16383

	t.Run("limit runes", func(t *testing.T) {
		p, fake := newAPIPlugin(&model.Channel{}, true)
		message := strings.Repeat("я", limit)
		require.Equal(t, limit, utf8.RuneCountInString(message))
		rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody([]string{model.NewId()}, message, nil))
		require.Equal(t, http.StatusCreated, rec.Code)

		stored, err := listMessagesForUser(fake, "user-a")
		require.NoError(t, err)
		require.Len(t, stored, 1)
		require.Equal(t, limit, utf8.RuneCountInString(stored[0].Message))
	})

	t.Run("one rune over", func(t *testing.T) {
		p, fake := newAPIPlugin(&model.Channel{}, true)
		message := strings.Repeat("я", limit+1)
		rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody([]string{model.NewId()}, message, nil))
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Empty(t, fake.data)
	})

	t.Run("rotation limit runes", func(t *testing.T) {
		p, fake := newAPIPlugin(&model.Channel{}, true)
		rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody(nil, "", map[string]any{
			"channel_ids": []string{model.NewId()},
			"messages":    []string{strings.Repeat("я", limit), "second"},
			"repeat":      RepeatDaily,
		}))
		require.Equal(t, http.StatusCreated, rec.Code)

		stored, err := listMessagesForUser(fake, "user-a")
		require.NoError(t, err)
		require.Len(t, stored, 1)
		require.Equal(t, limit, utf8.RuneCountInString(stored[0].Messages[0]))
	})

	t.Run("rotation one rune over", func(t *testing.T) {
		p, fake := newAPIPlugin(&model.Channel{}, true)
		rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody(nil, "", map[string]any{
			"channel_ids": []string{model.NewId()},
			"messages":    []string{strings.Repeat("я", limit+1), "second"},
			"repeat":      RepeatDaily,
		}))
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Empty(t, fake.data)
	})
}

func TestPendingQuota(t *testing.T) {
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{}, true)
	id := seedPending(t, fake, "user-a", channelID, maxPendingPerUser, 5)
	require.Len(t, fake.data, maxPendingPerUser)

	rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody([]string{channelID}, "one more", nil))
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Len(t, fake.data, maxPendingPerUser)

	rec = callJSON(t, p, http.MethodPatch, "/api/update", "user-a", createBody([]string{channelID}, "updated", map[string]any{
		"id": id,
	}))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, fake.data, maxPendingPerUser)

	stored, err := loadMessage(fake, "user-a", id)
	require.NoError(t, err)
	require.Equal(t, "updated", stored.Message)
	require.Equal(t, 5, stored.Occurrences)
}

func TestCreateForbidden(t *testing.T) {
	body := func() map[string]any {
		return createBody([]string{model.NewId()}, "hello", nil)
	}

	t.Run("permission", func(t *testing.T) {
		p, fake := newAPIPlugin(&model.Channel{}, false)
		rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", body())
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.Equal(t, "нельзя писать в канал", strings.TrimSpace(rec.Body.String()))
		require.Empty(t, fake.data)
	})

	t.Run("archived", func(t *testing.T) {
		p, fake := newAPIPlugin(&model.Channel{DeleteAt: 1}, true)
		rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", body())
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.Equal(t, "нельзя писать в канал", strings.TrimSpace(rec.Body.String()))
		require.Empty(t, fake.data)
	})
}

func TestCreateBadID(t *testing.T) {
	p, fake := newAPIPlugin(&model.Channel{}, true)
	rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody([]string{"x"}, "hello", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, fake.data)
	require.Zero(t, fake.channelGets)
}

func TestBodyTooLarge(t *testing.T) {
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{}, true)
	head := `{"channel_ids":["` + channelID + `"],"message":"`
	tail := `","send_at":"` + futureSendAt() + `"}`
	fillerLen := maxHTTPBodyBytes + 1 - len(head) - len(tail)
	require.Greater(t, fillerLen, 0)
	body := head + strings.Repeat("a", fillerLen) + tail
	require.Equal(t, maxHTTPBodyBytes+1, len(body))
	require.True(t, json.Valid([]byte(body)))

	rec := callAPI(p, http.MethodPost, "/api/create", "user-a", bytes.NewReader([]byte(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Zero(t, fake.channelGets)
	require.Empty(t, fake.data)
}

func TestUpdateAndCancelScoped(t *testing.T) {
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{}, true)
	otherID := model.NewId()
	require.NoError(t, saveMessage(fake, &ScheduledMessage{
		ID:          otherID,
		UserID:      "user-b",
		ChannelIDs:  []string{channelID},
		Message:     "secret",
		SendAt:      time.Now().UTC().Add(time.Hour).UnixMilli(),
		Status:      StatusPending,
		Occurrences: 2,
	}))

	rec := callJSON(t, p, http.MethodDelete, "/api/cancel", "user-a", map[string]any{"id": otherID})
	require.Equal(t, http.StatusNotFound, rec.Code)
	still, err := loadMessage(fake, "user-b", otherID)
	require.NoError(t, err)
	require.NotNil(t, still)
	require.Equal(t, "secret", still.Message)

	rec = callJSON(t, p, http.MethodPatch, "/api/update", "user-a", createBody([]string{channelID}, "hacked", map[string]any{
		"id": otherID,
	}))
	require.Equal(t, http.StatusNotFound, rec.Code)
	still, err = loadMessage(fake, "user-b", otherID)
	require.NoError(t, err)
	require.Equal(t, "secret", still.Message)
	require.Equal(t, 2, still.Occurrences)

	ownID := model.NewId()
	require.NoError(t, saveMessage(fake, &ScheduledMessage{
		ID:          ownID,
		UserID:      "user-a",
		ChannelIDs:  []string{channelID},
		Message:     "old",
		SendAt:      time.Now().UTC().Add(time.Hour).UnixMilli(),
		CreatedAt:   42,
		Status:      StatusPending,
		Attempts:    1,
		Occurrences: 4,
	}))
	rec = callJSON(t, p, http.MethodPatch, "/api/update", "user-a", createBody([]string{channelID}, "new text", map[string]any{
		"id": ownID,
	}))
	require.Equal(t, http.StatusOK, rec.Code)
	stored, err := loadMessage(fake, "user-a", ownID)
	require.NoError(t, err)
	require.Equal(t, "new text", stored.Message)
	require.Equal(t, 4, stored.Occurrences)
	require.Equal(t, StatusPending, stored.Status)
	require.Equal(t, int64(42), stored.CreatedAt)
	require.Equal(t, 1, stored.Attempts)

	rec = callJSON(t, p, http.MethodDelete, "/api/cancel", "user-a", map[string]any{"id": ownID})
	require.Equal(t, http.StatusNoContent, rec.Code)
	stored, err = loadMessage(fake, "user-a", ownID)
	require.NoError(t, err)
	require.Nil(t, stored)
	still, err = loadMessage(fake, "user-b", otherID)
	require.NoError(t, err)
	require.Equal(t, "secret", still.Message)
}

func TestUnauthorizedWithoutUserHeader(t *testing.T) {
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{}, true)
	rec := callJSON(t, p, http.MethodPost, "/api/create", "", createBody([]string{channelID}, "hello", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "нет доступа", strings.TrimSpace(rec.Body.String()))
	require.Zero(t, fake.kvOps)
	require.Zero(t, fake.channelGets)
}

func TestUpdateRejectsNonPendingBody(t *testing.T) {
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{}, true)
	id := model.NewId()
	require.NoError(t, saveMessage(fake, &ScheduledMessage{
		ID:         id,
		UserID:     "user-a",
		ChannelIDs: []string{channelID},
		Message:    "old",
		SendAt:     time.Now().UTC().Add(time.Hour).UnixMilli(),
		Status:     StatusFailed,
	}))
	rec := callJSON(t, p, http.MethodPatch, "/api/update", "user-a", createBody([]string{channelID}, "new text", map[string]any{
		"id": id,
	}))
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "править можно только ожидающее сообщение", strings.TrimSpace(rec.Body.String()))
}

func TestCreateKVSaveFailsBody(t *testing.T) {
	p, fake := newAPIPlugin(&model.Channel{}, true)
	fake.kvSetFail = model.NewAppError("KVSet", "test", nil, "kv down", http.StatusInternalServerError)
	rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody([]string{model.NewId()}, "hello", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "не удалось сохранить", strings.TrimSpace(rec.Body.String()))
}

func TestRotationMessageCap(t *testing.T) {
	texts := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = strings.Repeat("m", i+1)
		}
		return out
	}

	t.Run("twenty", func(t *testing.T) {
		p, fake := newAPIPlugin(&model.Channel{}, true)
		rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody(nil, "", map[string]any{
			"channel_ids": []string{model.NewId()},
			"messages":    texts(maxRotationMessages),
			"repeat":      RepeatDaily,
		}))
		require.Equal(t, http.StatusCreated, rec.Code)
		stored, err := listMessagesForUser(fake, "user-a")
		require.NoError(t, err)
		require.Len(t, stored, 1)
		require.Len(t, stored[0].Messages, maxRotationMessages)
	})

	t.Run("twenty one", func(t *testing.T) {
		p, fake := newAPIPlugin(&model.Channel{}, true)
		rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody(nil, "", map[string]any{
			"channel_ids": []string{model.NewId()},
			"messages":    texts(maxRotationMessages + 1),
			"repeat":      RepeatDaily,
		}))
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Empty(t, fake.data)
	})
}

func TestEmptyTimezoneParsesInDefaultZone(t *testing.T) {
	loc, err := time.LoadLocation("Australia/Sydney")
	require.NoError(t, err)
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{}, true)
	p.setConfiguration(&configuration{
		PollIntervalSeconds: 30,
		MaxAttempts:         3,
		DefaultTimezone:     "Australia/Sydney",
	})

	orig := scheduleNow
	scheduleNow = func() time.Time { return time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { scheduleNow = orig })

	rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody([]string{channelID}, "hello", map[string]any{
		"send_at":   "2026-06-01 09:30",
		"repeat":    RepeatDaily,
		"ends_mode": EndsOn,
		"ends_on":   "2026-06-01",
	}))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	stored, err := listMessagesForUser(fake, "user-a")
	require.NoError(t, err)
	require.Len(t, stored, 1)

	wantSend := time.Date(2026, 6, 1, 9, 30, 0, 0, loc)
	wantEnd := time.Date(2026, 6, 1, 23, 59, 0, 0, loc)
	utcSend := time.Date(2026, 6, 1, 9, 30, 0, 0, time.UTC)
	require.Equal(t, wantSend.UnixMilli(), stored[0].SendAt)
	require.NotEqual(t, utcSend.UnixMilli(), stored[0].SendAt)
	require.Equal(t, "Australia/Sydney", stored[0].Timezone)
	require.Equal(t, wantEnd.UnixMilli(), stored[0].EndsAt)
	require.Equal(t, "Australia/Sydney", p.getConfiguration().DefaultTimezone)
}

func TestEmptyTimezoneUsesAutomaticUserZone(t *testing.T) {
	loc, err := time.LoadLocation("Australia/Sydney")
	require.NoError(t, err)
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{}, true)
	fake.user = &model.User{Timezone: model.StringMap{
		"useAutomaticTimezone": "true",
		"automaticTimezone":    "Australia/Sydney",
		"manualTimezone":       "Europe/Moscow",
	}}
	p.setConfiguration(&configuration{
		PollIntervalSeconds: 30,
		MaxAttempts:         3,
		DefaultTimezone:     "UTC",
	})

	orig := scheduleNow
	scheduleNow = func() time.Time { return time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { scheduleNow = orig })

	rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", createBody([]string{channelID}, "hello", map[string]any{
		"timezone":  "",
		"send_at":   "2026-06-01 09:30",
		"repeat":    RepeatDaily,
		"ends_mode": EndsOn,
		"ends_on":   "2026-06-01",
	}))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	stored, err := listMessagesForUser(fake, "user-a")
	require.NoError(t, err)
	require.Len(t, stored, 1)

	wantSend := time.Date(2026, 6, 1, 9, 30, 0, 0, loc)
	wantEnd := time.Date(2026, 6, 1, 23, 59, 0, 0, loc)
	require.Equal(t, wantSend.UnixMilli(), stored[0].SendAt)
	require.Equal(t, "Australia/Sydney", stored[0].Timezone)
	require.Equal(t, wantEnd.UnixMilli(), stored[0].EndsAt)
	require.Equal(t, "UTC", p.getConfiguration().DefaultTimezone)

	rec = callJSON(t, p, http.MethodPatch, "/api/update", "user-a", createBody([]string{channelID}, "hello", map[string]any{
		"id":        stored[0].ID,
		"timezone":  "",
		"send_at":   "2026-06-02 09:30",
		"repeat":    RepeatDaily,
		"ends_mode": EndsOn,
		"ends_on":   "2026-06-02",
	}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	updated, err := loadMessage(fake, "user-a", stored[0].ID)
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 6, 2, 9, 30, 0, 0, loc).UnixMilli(), updated.SendAt)
	require.Equal(t, time.Date(2026, 6, 2, 23, 59, 0, 0, loc).UnixMilli(), updated.EndsAt)
	require.Equal(t, "Australia/Sydney", updated.Timezone)
	require.Equal(t, "UTC", p.getConfiguration().DefaultTimezone)
}

func TestCreatePresenceIgnoresClientPeerID(t *testing.T) {
	userA := model.NewId()
	userB := model.NewId()
	stranger := model.NewId()
	require.NotEqual(t, userB, stranger)
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{
		Type: model.ChannelTypeDirect,
		Name: model.GetDMNameFromIds(userA, userB),
	}, true)
	p.post = fake

	rec := callJSON(t, p, http.MethodPost, "/api/create", userA, map[string]any{
		"channel_ids": []string{channelID},
		"message":     "ping",
		"trigger":     TriggerOnline,
		"peer_id":     stranger,
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Zero(t, fake.createPosts)

	stored, err := listMessagesForUser(fake, userA)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.Equal(t, userA, stored[0].UserID)
	require.Equal(t, userB, stored[0].PeerID)
	require.NotEqual(t, stranger, stored[0].PeerID)
	require.Equal(t, TriggerOnline, stored[0].Trigger)
	require.Zero(t, stored[0].SendAt)
	require.Empty(t, stored[0].Repeat)
}

func TestCreatePresenceRejectsPublicChannel(t *testing.T) {
	p, fake := newAPIPlugin(&model.Channel{Type: model.ChannelTypeOpen, Name: "town-square"}, true)
	rec := callJSON(t, p, http.MethodPost, "/api/create", model.NewId(), map[string]any{
		"channel_ids": []string{model.NewId()},
		"message":     "ping",
		"trigger":     TriggerOnline,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Empty(t, fake.data)
}

func TestCreatePresenceCountsTowardQuota(t *testing.T) {
	userA := model.NewId()
	userB := model.NewId()
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{
		Type: model.ChannelTypeDirect,
		Name: model.GetDMNameFromIds(userA, userB),
	}, true)
	seedPending(t, fake, userA, channelID, maxPendingPerUser, 0)
	require.Len(t, fake.data, maxPendingPerUser)

	rec := callJSON(t, p, http.MethodPost, "/api/create", userA, map[string]any{
		"channel_ids": []string{channelID},
		"message":     "one more",
		"trigger":     TriggerOnline,
	})
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Len(t, fake.data, maxPendingPerUser)
}

func TestSendNowIgnoresPresence(t *testing.T) {
	t.Run("offline peer still sends", func(t *testing.T) {
		userA := model.NewId()
		peer := model.NewId()
		channelID := model.NewId()
		p, fake := newAPIPlugin(&model.Channel{}, true)
		fake.user = &model.User{}
		fake.status = &model.Status{Status: model.StatusOffline}
		p.post = fake
		p.status = fake
		id := model.NewId()
		require.NoError(t, saveMessage(fake, &ScheduledMessage{
			ID:         id,
			UserID:     userA,
			ChannelIDs: []string{channelID},
			Message:    "ping",
			PeerID:     peer,
			Trigger:    TriggerOnline,
			SendAt:     time.Now().UTC().Add(time.Hour).UnixMilli(),
			Status:     StatusPending,
		}))

		rec := callJSON(t, p, http.MethodPost, "/api/send", userA, map[string]any{"id": id})
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		require.Equal(t, 1, fake.createPosts)
		require.Equal(t, channelID, fake.lastPost.ChannelId)
		require.Equal(t, userA, fake.lastPost.UserId)
		require.Equal(t, "ping", fake.lastPost.Message)
		stored, err := loadMessage(fake, userA, id)
		require.NoError(t, err)
		require.Nil(t, stored)
	})

	t.Run("other user", func(t *testing.T) {
		channelID := model.NewId()
		p, fake := newAPIPlugin(&model.Channel{}, true)
		p.post = fake
		id := model.NewId()
		require.NoError(t, saveMessage(fake, &ScheduledMessage{
			ID:         id,
			UserID:     "user-b",
			ChannelIDs: []string{channelID},
			Message:    "secret",
			PeerID:     model.NewId(),
			Trigger:    TriggerOnline,
			Status:     StatusPending,
		}))

		rec := callJSON(t, p, http.MethodPost, "/api/send", "user-a", map[string]any{"id": id})
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Equal(t, "сообщение не найдено", strings.TrimSpace(rec.Body.String()))
		require.Zero(t, fake.createPosts)
		stored, err := loadMessage(fake, "user-b", id)
		require.NoError(t, err)
		require.Equal(t, "secret", stored.Message)
	})

	t.Run("not pending", func(t *testing.T) {
		userA := model.NewId()
		channelID := model.NewId()
		p, fake := newAPIPlugin(&model.Channel{}, true)
		p.post = fake
		id := model.NewId()
		require.NoError(t, saveMessage(fake, &ScheduledMessage{
			ID:         id,
			UserID:     userA,
			ChannelIDs: []string{channelID},
			Message:    "done",
			Trigger:    TriggerOnline,
			Status:     StatusFailed,
		}))

		rec := callJSON(t, p, http.MethodPost, "/api/send", userA, map[string]any{"id": id})
		require.Equal(t, http.StatusConflict, rec.Code)
		require.Equal(t, "править можно только ожидающее сообщение", strings.TrimSpace(rec.Body.String()))
		require.Zero(t, fake.createPosts)
		stored, err := loadMessage(fake, userA, id)
		require.NoError(t, err)
		require.Equal(t, StatusFailed, stored.Status)
	})
}

func TestUpdateConvertsPresenceToTimed(t *testing.T) {
	t.Run("send_at clears presence", func(t *testing.T) {
		userA := model.NewId()
		peer := model.NewId()
		channelID := model.NewId()
		p, fake := newAPIPlugin(&model.Channel{
			Type: model.ChannelTypeDirect,
			Name: model.GetDMNameFromIds(userA, peer),
		}, true)
		id := model.NewId()
		require.NoError(t, saveMessage(fake, &ScheduledMessage{
			ID:         id,
			UserID:     userA,
			ChannelIDs: []string{channelID},
			Message:    "old",
			PeerID:     peer,
			Trigger:    TriggerOnline,
			Status:     StatusPending,
			CreatedAt:  10,
		}))

		when := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
		rec := callJSON(t, p, http.MethodPatch, "/api/update", userA, map[string]any{
			"id":          id,
			"channel_ids": []string{channelID},
			"message":     "later",
			"send_at":     when.Format(time.RFC3339),
		})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		stored, err := loadMessage(fake, userA, id)
		require.NoError(t, err)
		require.Empty(t, stored.Trigger)
		require.Empty(t, stored.PeerID)
		require.Equal(t, when.UnixMilli(), stored.SendAt)
		require.Equal(t, "later", stored.Message)
	})

	t.Run("message only keeps presence", func(t *testing.T) {
		userA := model.NewId()
		peer := model.NewId()
		channelID := model.NewId()
		p, fake := newAPIPlugin(&model.Channel{}, true)
		id := model.NewId()
		require.NoError(t, saveMessage(fake, &ScheduledMessage{
			ID:         id,
			UserID:     userA,
			ChannelIDs: []string{channelID},
			Message:    "old",
			PeerID:     peer,
			Trigger:    TriggerOnline,
			SendAt:     0,
			Status:     StatusPending,
		}))

		rec := callJSON(t, p, http.MethodPatch, "/api/update", userA, map[string]any{
			"id":      id,
			"message": "only text",
		})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		stored, err := loadMessage(fake, userA, id)
		require.NoError(t, err)
		require.Equal(t, TriggerOnline, stored.Trigger)
		require.Equal(t, peer, stored.PeerID)
		require.Equal(t, "only text", stored.Message)
		require.Zero(t, stored.SendAt)
	})
}

// chA, chB, chC are 26-character ids: model.IsValidId rejects shorter values
// such as the literal "chA". "nope" is the invalid query from the brief.
func TestListFiltersByChannel(t *testing.T) {
	const (
		chA = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
		chB = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
		chC = "cccccccccccccccccccccccccc"
	)
	p, fake := newAPIPlugin(&model.Channel{}, true)
	const userID = "user-a"
	require.NoError(t, saveMessage(fake, &ScheduledMessage{
		ID: "m1", UserID: userID, ChannelIDs: []string{chA}, Message: "one", Status: StatusPending,
	}))
	require.NoError(t, saveMessage(fake, &ScheduledMessage{
		ID: "m2", UserID: userID, ChannelIDs: []string{chA, chB}, Message: "two", Status: StatusPending,
	}))
	require.NoError(t, saveMessage(fake, &ScheduledMessage{
		ID: "m3", UserID: userID, ChannelIDs: []string{chC}, Message: "three", Status: StatusFailed,
	}))

	ids := func(t *testing.T, rec *httptest.ResponseRecorder) []string {
		t.Helper()
		var msgs []*ScheduledMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &msgs))
		out := make([]string, len(msgs))
		for i, msg := range msgs {
			out[i] = msg.ID
		}
		return out
	}

	rec := callAPI(p, http.MethodGet, "/api/list?channel_id="+chA, userID, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"m1", "m2"}, ids(t, rec))

	rec = callAPI(p, http.MethodGet, "/api/list?channel_id="+chB, userID, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"m2"}, ids(t, rec))

	rec = callAPI(p, http.MethodGet, "/api/list?channel_id="+chC, userID, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Empty(t, ids(t, rec))

	rec = callAPI(p, http.MethodGet, "/api/list", userID, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"m1", "m2", "m3"}, ids(t, rec))

	rec = callAPI(p, http.MethodGet, "/api/list?channel_id=nope", userID, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "некорректный канал", strings.TrimSpace(rec.Body.String()))
}
