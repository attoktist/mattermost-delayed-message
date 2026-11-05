package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 26 letters: model.IsValidId requires that length and only letters or digits.
const validChannelID = "abcdefghijklmnopqrstuvwxyz"

func TestCheckCanPost(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		api := &fakeGate{allow: true, channel: &model.Channel{}}
		err := checkCanPost(api, "u1", "nope")
		require.ErrorIs(t, err, errInvalidChannel)
		assert.Zero(t, api.channelGets)
		assert.Zero(t, api.permCalls)
	})

	t.Run("missing channel", func(t *testing.T) {
		api := &fakeGate{allow: true, channelErr: &model.AppError{StatusCode: 404, Message: "missing"}}
		err := checkCanPost(api, "u1", validChannelID)
		require.ErrorIs(t, err, errCannotPost)
		assert.Equal(t, 1, api.channelGets)
		assert.Zero(t, api.permCalls)
	})

	t.Run("nil channel", func(t *testing.T) {
		api := &fakeGate{allow: true}
		err := checkCanPost(api, "u1", validChannelID)
		require.ErrorIs(t, err, errCannotPost)
		assert.Zero(t, api.permCalls)
	})

	t.Run("deleted", func(t *testing.T) {
		api := &fakeGate{allow: true, channel: &model.Channel{DeleteAt: 1}}
		err := checkCanPost(api, "u1", validChannelID)
		require.ErrorIs(t, err, errCannotPost)
		assert.Zero(t, api.permCalls)
	})

	t.Run("permission false", func(t *testing.T) {
		api := &fakeGate{channel: &model.Channel{}}
		err := checkCanPost(api, "u1", validChannelID)
		require.ErrorIs(t, err, errCannotPost)
		require.Equal(t, 1, api.permCalls)
		assert.Same(t, model.PermissionCreatePost, api.permissionSeen)
		assert.Equal(t, "u1", api.userIDSeen)
		assert.Equal(t, validChannelID, api.channelIDSeen)
	})

	t.Run("permission true", func(t *testing.T) {
		api := &fakeGate{allow: true, channel: &model.Channel{}}
		require.NoError(t, checkCanPost(api, "u1", validChannelID))
		assert.Same(t, model.PermissionCreatePost, api.permissionSeen)
		assert.Equal(t, "u1", api.userIDSeen)
		assert.Equal(t, validChannelID, api.channelIDSeen)
	})
}

func TestUserActive(t *testing.T) {
	t.Run("deleted", func(t *testing.T) {
		api := &fakeGate{user: &model.User{DeleteAt: 1}}
		require.ErrorIs(t, userActive(api, "u1"), errUserGone)
	})

	t.Run("not found", func(t *testing.T) {
		api := &fakeGate{userErr: &model.AppError{StatusCode: 404, Message: "missing"}}
		require.ErrorIs(t, userActive(api, "u1"), errUserGone)
	})

	t.Run("other error", func(t *testing.T) {
		appErr := &model.AppError{StatusCode: 500, Message: "boom"}
		api := &fakeGate{userErr: appErr}
		err := userActive(api, "u1")
		require.Error(t, err)
		assert.NotErrorIs(t, err, errUserGone)
		assert.Same(t, appErr, err)
	})

	t.Run("active", func(t *testing.T) {
		api := &fakeGate{user: &model.User{}}
		require.NoError(t, userActive(api, "u1"))
	})

	t.Run("nil user", func(t *testing.T) {
		require.ErrorIs(t, userActive(&fakeGate{}, "u1"), errUserGone)
	})
}

func TestMaxPostSize(t *testing.T) {
	got := maxPostSize(&fakeGate{})
	assert.Equal(t, model.PostMessageMaxRunesV2, got)
	assert.Equal(t, 16383, got)
}
