package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func execSlash(t *testing.T, p *Plugin, userID, channelID, command string) *model.CommandResponse {
	t.Helper()
	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{
		UserId:    userID,
		ChannelId: channelID,
		Command:   command,
	})
	require.Nil(t, appErr)
	require.NotNil(t, resp)
	require.Equal(t, model.CommandResponseTypeEphemeral, resp.ResponseType)
	return resp
}

func slashWhen() string {
	return time.Now().UTC().Add(2 * time.Hour).Format("2006-01-02 15:04")
}

func TestSlashRejectsWhenCannotPost(t *testing.T) {
	when := slashWhen()
	cases := []struct {
		name    string
		channel *model.Channel
		allow   bool
	}{
		{name: "archived", channel: &model.Channel{DeleteAt: 1}, allow: true},
		{name: "permission", channel: &model.Channel{}, allow: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			channelID := model.NewId()
			p, fake := newAPIPlugin(tc.channel, tc.allow)
			resp := execSlash(t, p, "user-a", channelID, fmt.Sprintf(`/schedule "hello" "%s" UTC`, when))
			require.Equal(t, "В этот канал писать нельзя.", resp.Text)
			require.Empty(t, fake.data)
		})
	}
}

func TestSlashQuota(t *testing.T) {
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{}, true)
	seedPending(t, fake, "user-a", channelID, maxPendingPerUser, 0)
	require.Len(t, fake.data, maxPendingPerUser)

	resp := execSlash(t, p, "user-a", channelID, fmt.Sprintf(`/schedule "one more" "%s" UTC`, slashWhen()))
	require.Equal(t, "Достигнут лимит: 100 отложенных сообщений.", resp.Text)
	require.Len(t, fake.data, maxPendingPerUser)

	stored, err := listMessagesForUser(fake, "user-a")
	require.NoError(t, err)
	require.Len(t, stored, maxPendingPerUser)
	for _, msg := range stored {
		require.Equal(t, "seed", msg.Message)
	}
}

func TestSlashCreateUsesCommandChannel(t *testing.T) {
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{}, true)
	resp := execSlash(t, p, "user-a", channelID, fmt.Sprintf(`/schedule "hello" "%s" UTC`, slashWhen()))
	require.NotContains(t, resp.Text, "Не удалось сохранить")

	stored, err := listMessagesForUser(fake, "user-a")
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.Equal(t, []string{channelID}, stored[0].ChannelIDs)
	require.Equal(t, "user-a", stored[0].UserID)
	require.Equal(t, "hello", stored[0].Message)
	require.Empty(t, stored[0].ChannelID)
	require.Zero(t, stored[0].WindowMs)
	require.Empty(t, stored[0].Messages)
}

func TestSlashCancelOtherUser(t *testing.T) {
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{}, true)
	otherID := model.NewId()
	require.NoError(t, saveMessage(fake, &ScheduledMessage{
		ID:         otherID,
		UserID:     "user-b",
		ChannelIDs: []string{channelID},
		Message:    "secret",
		SendAt:     time.Now().UTC().Add(time.Hour).UnixMilli(),
		Status:     StatusPending,
	}))

	gets := &kvGets{kvAPI: fake}
	p.kv = gets
	otherKey := messageKey("user-b", otherID)

	resp := execSlash(t, p, "user-a", channelID, "/schedule cancel "+otherID)
	require.Equal(t, "Сообщение не найдено.", resp.Text)
	require.NotContains(t, gets.keys, otherKey)
	require.Contains(t, gets.keys, messageKey("user-a", otherID))

	still, err := loadMessage(fake, "user-b", otherID)
	require.NoError(t, err)
	require.NotNil(t, still)
	require.Equal(t, "secret", still.Message)
}

func TestSlashRuneCount(t *testing.T) {
	// maxPostSize is 16383 runes. "я" is two bytes, so len(string) would reject the boundary.
	const limit = 16383
	channelID := model.NewId()
	when := slashWhen()

	t.Run("limit runes", func(t *testing.T) {
		p, fake := newAPIPlugin(&model.Channel{}, true)
		message := strings.Repeat("я", limit)
		require.Equal(t, limit, utf8.RuneCountInString(message))
		require.Greater(t, len(message), limit)
		execSlash(t, p, "user-a", channelID, fmt.Sprintf(`/schedule "%s" "%s" UTC`, message, when))

		stored, err := listMessagesForUser(fake, "user-a")
		require.NoError(t, err)
		require.Len(t, stored, 1)
		require.Equal(t, limit, utf8.RuneCountInString(stored[0].Message))
	})

	t.Run("one rune over", func(t *testing.T) {
		p, fake := newAPIPlugin(&model.Channel{}, true)
		message := strings.Repeat("я", limit+1)
		resp := execSlash(t, p, "user-a", channelID, fmt.Sprintf(`/schedule "%s" "%s" UTC`, message, when))
		require.Equal(t, "Сообщение длиннее допустимого.", resp.Text)
		require.Empty(t, fake.data)
	})
}

func TestSlashRejectsWindow(t *testing.T) {
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{}, true)
	resp := execSlash(t, p, "user-a", channelID, fmt.Sprintf(`/schedule "hello" "%s" UTC window=60000`, slashWhen()))
	require.Contains(t, resp.Text, "Неизвестный флаг: `window`")
	require.Empty(t, fake.data)
}

func TestSlashHelpIsRussian(t *testing.T) {
	p, _ := newAPIPlugin(&model.Channel{}, true)
	resp := execSlash(t, p, "user-a", model.NewId(), "/schedule help")
	want := strings.Join([]string{
		"Отложенные сообщения.",
		"/schedule \"текст\" \"ГГГГ-ММ-ДД ЧЧ:ММ\" [часовой_пояс]",
		"/schedule list",
		"/schedule cancel <id>",
		"Флаги: repeat:daily|weekdays|weekly|fortnightly|monthly|yearly, count:N, until:ГГГГ-ММ-ДД.",
		"count и until вместе нельзя. Окно, несколько каналов и ротация — только через модалку.",
	}, "\n")
	require.Equal(t, want, resp.Text)
}

func TestSlashEmptyTimezoneUsesAutomaticUserZone(t *testing.T) {
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

	execSlash(t, p, "user-a", channelID, `/schedule "hello" "2026-06-01 09:30"`)

	stored, err := listMessagesForUser(fake, "user-a")
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.Equal(t, time.Date(2026, 6, 1, 9, 30, 0, 0, loc).UnixMilli(), stored[0].SendAt)
	require.Equal(t, "Australia/Sydney", stored[0].Timezone)
	require.Equal(t, []string{channelID}, stored[0].ChannelIDs)
	require.Equal(t, "UTC", p.getConfiguration().DefaultTimezone)
}

func TestParseCommandArgs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"two quoted", `"hello world" "2025-06-01 09:30"`, []string{"hello world", "2025-06-01 09:30"}},
		{"quoted plus tz", `"msg" "2025-06-01 09:30" Australia/Sydney`, []string{"msg", "2025-06-01 09:30", "Australia/Sydney"}},
		{"brief three tokens", `"hello world" "2026-06-01 09:30" UTC`, []string{"hello world", "2026-06-01 09:30", "UTC"}},
		{"list", `list`, []string{"list"}},
		{"cancel id", `cancel abc123`, []string{"cancel", "abc123"}},
		{"empty quoted preserved", `"" "2025-06-01 09:30"`, []string{"", "2025-06-01 09:30"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseCommandArgs(c.in)
			if len(c.want) == 0 {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, c.want, got)
		})
	}
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "", truncate("anything", 0))
	assert.Equal(t, "abc", truncate("abc", 5))
	assert.Equal(t, "abcde…", truncate("abcdefghij", 5))
}

func TestBacktickURLs(t *testing.T) {
	assert.Equal(t, "see `https://example.com/a` now", backtickURLs("see https://example.com/a now"))
}

func TestSlashListOnlineWhenColumn(t *testing.T) {
	channelID := model.NewId()
	p, fake := newAPIPlugin(&model.Channel{}, true)
	onlineID := model.NewId()
	timedID := model.NewId()
	sendAt := time.Date(2026, 6, 1, 9, 30, 0, 0, time.UTC).UnixMilli()
	require.NoError(t, saveMessage(fake, &ScheduledMessage{
		ID:         onlineID,
		UserID:     "user-a",
		ChannelIDs: []string{channelID},
		Message:    "ping",
		SendAt:     0,
		Status:     StatusPending,
		Trigger:    TriggerOnline,
		Timezone:   "UTC",
	}))
	require.NoError(t, saveMessage(fake, &ScheduledMessage{
		ID:         timedID,
		UserID:     "user-a",
		ChannelIDs: []string{channelID},
		Message:    "later",
		SendAt:     sendAt,
		Status:     StatusPending,
		Timezone:   "UTC",
	}))

	resp := execSlash(t, p, "user-a", channelID, "/schedule list")
	epoch := formatSendAt(0, "UTC")
	timed := formatSendAt(sendAt, "UTC")
	require.Contains(t, resp.Text, timed)
	require.NotContains(t, resp.Text, epoch)

	var onlineLine string
	for _, line := range strings.Split(resp.Text, "\n") {
		if strings.Contains(line, onlineID) {
			onlineLine = line
			break
		}
	}
	require.NotEmpty(t, onlineLine)
	require.Contains(t, onlineLine, "Когда будет в сети")
	require.NotContains(t, onlineLine, epoch)
}

// kvGets records KVGet keys. Cancel of another user's id must not load their key.
type kvGets struct {
	kvAPI
	keys []string
}

func (g *kvGets) KVGet(key string) ([]byte, *model.AppError) {
	g.keys = append(g.keys, key)
	return g.kvAPI.KVGet(key)
}
