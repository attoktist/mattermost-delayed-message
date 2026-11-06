package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestOtherDMUser(t *testing.T) {
	a := model.NewId()
	b := model.NewId()

	peer, err := otherDMUser(&model.Channel{
		Type: model.ChannelTypeDirect,
		Name: model.GetDMNameFromIds(a, b),
	}, a)
	require.NoError(t, err)
	require.Equal(t, b, peer)

	peer, err = otherDMUser(&model.Channel{Type: model.ChannelTypeOpen, Name: "town-square"}, a)
	require.Error(t, err)
	require.Empty(t, peer)
}

func TestValidatePresenceRejects(t *testing.T) {
	a := model.NewId()
	b := model.NewId()
	channelID := model.NewId()
	dm := &fakeGate{
		allow: true,
		channel: &model.Channel{
			Type: model.ChannelTypeDirect,
			Name: model.GetDMNameFromIds(a, b),
		},
	}

	t.Run("public channel", func(t *testing.T) {
		gate := &fakeGate{allow: true, channel: &model.Channel{Type: model.ChannelTypeOpen, Name: "town-square"}}
		peer, err := validatePresence(gate, a, []string{channelID}, "", 0, nil)
		require.Error(t, err)
		require.Empty(t, peer)
	})

	t.Run("daily", func(t *testing.T) {
		peer, err := validatePresence(dm, a, []string{channelID}, RepeatDaily, 0, nil)
		require.Error(t, err)
		require.Empty(t, peer)
	})

	t.Run("window", func(t *testing.T) {
		peer, err := validatePresence(dm, a, []string{channelID}, "", 1, nil)
		require.Error(t, err)
		require.Empty(t, peer)
	})

	t.Run("extra messages", func(t *testing.T) {
		peer, err := validatePresence(dm, a, []string{channelID}, "", 0, []string{"one", "two"})
		require.Error(t, err)
		require.Empty(t, peer)
	})

	t.Run("two channels", func(t *testing.T) {
		peer, err := validatePresence(dm, a, []string{channelID, model.NewId()}, "", 0, nil)
		require.Error(t, err)
		require.Empty(t, peer)
	})

	t.Run("dm", func(t *testing.T) {
		peer, err := validatePresence(dm, a, []string{channelID}, "", 0, nil)
		require.NoError(t, err)
		require.Equal(t, b, peer)
	})
}
