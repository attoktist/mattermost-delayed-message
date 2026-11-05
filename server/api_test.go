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
}

func (f *apiFake) KVSet(key string, value []byte) *model.AppError {
	f.kvOps++
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

func (f *apiFake) CreatePost(*model.Post) (*model.Post, *model.AppError) {
	f.createPosts++
	return &model.Post{}, nil
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
		require.Equal(t, "forbidden", strings.TrimSpace(rec.Body.String()))
		require.Empty(t, fake.data)
	})

	t.Run("archived", func(t *testing.T) {
		p, fake := newAPIPlugin(&model.Channel{DeleteAt: 1}, true)
		rec := callJSON(t, p, http.MethodPost, "/api/create", "user-a", body())
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.Equal(t, "forbidden", strings.TrimSpace(rec.Body.String()))
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
	require.Equal(t, "unauthorized", strings.TrimSpace(rec.Body.String()))
	require.Zero(t, fake.kvOps)
	require.Zero(t, fake.channelGets)
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
