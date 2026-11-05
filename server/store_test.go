package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyHelpers(t *testing.T) {
	assert.Equal(t, "scheduled_user1_msgA", messageKey("user1", "msgA"))
	assert.Equal(t, "scheduled_user1_", userPrefix("user1"))

	u, m, ok := parseUserAndMessageID("scheduled_user1_msgA")
	require.True(t, ok)
	assert.Equal(t, "user1", u)
	assert.Equal(t, "msgA", m)

	_, _, ok = parseUserAndMessageID("foo_bar")
	assert.False(t, ok)
}

func TestSaveAndLoadMessage(t *testing.T) {
	api := newFakeKV()
	msg := &ScheduledMessage{
		ID:      "m1",
		UserID:  "u1",
		Message: "hello",
		Status:  StatusPending,
	}

	require.NoError(t, saveMessage(api, msg))

	stored, appErr := api.KVGet("scheduled_u1_m1")
	require.Nil(t, appErr)
	require.NotNil(t, stored)

	got, err := loadMessage(api, "u1", "m1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "hello", got.Message)
	assert.Equal(t, "m1", got.ID)
	assert.Equal(t, "u1", got.UserID)
}

func TestLoadMessageNotFound(t *testing.T) {
	got, err := loadMessage(newFakeKV(), "u1", "missing")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestLoadMessageIsScopedToUser(t *testing.T) {
	api := newFakeKV()
	require.NoError(t, saveMessage(api, &ScheduledMessage{
		ID:      "m1",
		UserID:  "userA",
		Message: "secret",
		Status:  StatusPending,
	}))

	got, err := loadMessage(api, "userB", "m1")
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = loadMessage(api, "userA", "m1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "secret", got.Message)
}

func TestSaveMessageRequiresIDAndUser(t *testing.T) {
	api := newFakeKV()
	require.Error(t, saveMessage(api, &ScheduledMessage{ID: "", UserID: "u"}))
	require.Error(t, saveMessage(api, &ScheduledMessage{ID: "m", UserID: ""}))
	keys, appErr := api.KVList(0, listPageSize)
	require.Nil(t, appErr)
	assert.Empty(t, keys)
}

func TestDeleteMessage(t *testing.T) {
	api := newFakeKV()
	require.NoError(t, saveMessage(api, &ScheduledMessage{ID: "m1", UserID: "u1", Message: "x"}))
	require.NoError(t, deleteMessage(api, "u1", "m1"))

	got, err := loadMessage(api, "u1", "m1")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestListMessagesForUserFiltersByPrefix(t *testing.T) {
	api := newFakeKV()
	require.NoError(t, saveMessage(api, &ScheduledMessage{ID: "m1", UserID: "u1", Message: "mine"}))
	require.NoError(t, saveMessage(api, &ScheduledMessage{ID: "m2", UserID: "u2", Message: "other"}))
	require.Nil(t, api.KVSet("unrelated_key", []byte(`{"id":"x","user_id":"z","message":"nope"}`)))

	msgs, err := listMessagesForUser(api, "u1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "mine", msgs[0].Message)
}

func TestListAllPendingMessagesPaginates(t *testing.T) {
	api := newFakeKV()
	for i := 0; i < listPageSize; i++ {
		require.NoError(t, saveMessage(api, &ScheduledMessage{
			ID:     fmt.Sprintf("msg%d", i),
			UserID: "u1",
		}))
	}
	require.NoError(t, saveMessage(api, &ScheduledMessage{ID: "last", UserID: "u1"}))
	require.Nil(t, api.KVSet("not_scheduled", []byte(`{"id":"hidden","user_id":"u1","message":"skip"}`)))

	msgs, err := listAllPendingMessages(api)
	require.NoError(t, err)
	assert.Len(t, msgs, listPageSize+1)
	assert.Equal(t, []kvListCall{{page: 0, count: listPageSize}, {page: 1, count: listPageSize}}, api.lists)
}

func TestAcquireSendLockReturnsAtomicResult(t *testing.T) {
	api := newFakeKV()
	got, err := acquireSendLock(api, "m1")
	require.NoError(t, err)
	assert.True(t, got)

	require.Len(t, api.sets, 1)
	assert.True(t, api.sets[0].Atomic)
	assert.Nil(t, api.sets[0].OldValue)
	assert.Equal(t, int64(60), api.sets[0].ExpireInSeconds)

	raw, appErr := api.KVGet("lock_m1")
	require.Nil(t, appErr)
	assert.Equal(t, []byte("1"), raw)
}

func TestAcquireSendLockReturnsFalseWhenContended(t *testing.T) {
	api := newFakeKV()
	first, err := acquireSendLock(api, "m1")
	require.NoError(t, err)
	assert.True(t, first)

	second, err := acquireSendLock(api, "m1")
	require.NoError(t, err)
	assert.False(t, second)

	raw, appErr := api.KVGet("lock_m1")
	require.Nil(t, appErr)
	assert.Equal(t, []byte("1"), raw)
}

func TestReleaseSendLock(t *testing.T) {
	api := newFakeKV()
	ok, err := acquireSendLock(api, "m1")
	require.NoError(t, err)
	require.True(t, ok)

	releaseSendLock(api, "m1")
	raw, appErr := api.KVGet("lock_m1")
	require.Nil(t, appErr)
	assert.Nil(t, raw)

	ok, err = acquireSendLock(api, "m1")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestListMessagesNormalizesLegacyChannelID(t *testing.T) {
	api := newFakeKV()
	legacy := []byte(`{"id":"m1","user_id":"u1","channel_id":"chOLD","message":"hi","status":"pending"}`)
	require.Nil(t, api.KVSet("scheduled_u1_m1", legacy))

	msgs, err := listMessagesForUser(api, "u1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, []string{"chOLD"}, msgs[0].ChannelIDs)
	assert.Empty(t, msgs[0].ChannelID)
	assert.Equal(t, []string{"chOLD"}, msgs[0].Targets())
}

func TestLoadMessageNormalizesLegacyChannelID(t *testing.T) {
	api := newFakeKV()
	legacy := []byte(`{"id":"m1","user_id":"u1","channel_id":"chOLD","message":"hi","status":"pending"}`)
	require.Nil(t, api.KVSet("scheduled_u1_m1", legacy))

	got, err := loadMessage(api, "u1", "m1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, []string{"chOLD"}, got.ChannelIDs)
	assert.Equal(t, "", got.ChannelID)
	assert.Equal(t, []string{"chOLD"}, got.Targets())
}

func TestListMessagesForUserSkipsCorruptValue(t *testing.T) {
	api := newFakeKV()
	require.NoError(t, saveMessage(api, &ScheduledMessage{ID: "m1", UserID: "u1", Message: "ok"}))
	require.Nil(t, api.KVSet(messageKey("u1", "bad"), []byte("not-json")))

	msgs, err := listMessagesForUser(api, "u1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "ok", msgs[0].Message)
	assert.Equal(t, []string{"failed to unmarshal scheduled message"}, api.warns)
}

func TestTargetsPrefersChannelIDs(t *testing.T) {
	m := &ScheduledMessage{ChannelIDs: []string{"a", "b"}, ChannelID: "ignored"}
	assert.Equal(t, []string{"a", "b"}, m.Targets())
}

func TestTargetsFallsBackToLegacyChannelID(t *testing.T) {
	m := &ScheduledMessage{ChannelID: "only"}
	assert.Equal(t, []string{"only"}, m.Targets())
}

func TestTargetsEmptyWhenNothingSet(t *testing.T) {
	m := &ScheduledMessage{}
	assert.Nil(t, m.Targets())
}

var _ kvAPI = (*fakeKV)(nil)
