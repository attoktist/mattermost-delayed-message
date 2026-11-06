package main

import (
	"errors"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// statusAPI reads presence. plugin.API satisfies it.
type statusAPI interface {
	GetUserStatus(userID string) (*model.Status, *model.AppError)
}

var _ statusAPI = plugin.API(nil)

// otherDMUser returns the direct-message partner who is not selfID.
// The channel must be type D and its name must be two ids joined by "__".
func otherDMUser(channel *model.Channel, selfID string) (string, error) {
	if channel == nil || channel.Type != model.ChannelTypeDirect {
		return "", errors.New("только личный диалог")
	}
	ids := strings.Split(channel.Name, "__")
	if len(ids) != 2 || ids[0] == "" || ids[1] == "" {
		return "", errors.New("только личный диалог")
	}
	switch {
	case ids[0] == selfID && ids[1] != selfID:
		return ids[1], nil
	case ids[1] == selfID && ids[0] != selfID:
		return ids[0], nil
	default:
		return "", errors.New("собеседник не найден")
	}
}

// validatePresence accepts one direct channel and no recurrence, window, or
// rotation. The peer is taken from the channel, never from the request body.
func validatePresence(api postGate, userID string, channelIDs []string, repeat string, windowMs int64, extraMessages []string) (string, error) {
	if len(channelIDs) != 1 {
		return "", errors.New("укажите один канал")
	}
	if repeat != "" {
		return "", errors.New("отправка при появлении в сети не повторяется")
	}
	if windowMs != 0 {
		return "", errors.New("окно времени не поддерживается")
	}
	if len(extraMessages) != 0 {
		return "", errors.New("несколько текстов не поддерживаются")
	}
	channel, appErr := api.GetChannel(channelIDs[0])
	if appErr != nil || channel == nil {
		return "", errCannotPost
	}
	peer, err := otherDMUser(channel, userID)
	if err != nil {
		return "", err
	}
	if err := checkCanPost(api, userID, channelIDs[0]); err != nil {
		return "", err
	}
	return peer, nil
}

// presenceReady reports whether PeerID is online. errUserGone means the caller
// should delete the record. Any other error must leave the record unchanged.
func (p *Plugin) presenceReady(msg *ScheduledMessage) (bool, error) {
	if err := userActive(p.gate, msg.PeerID); err != nil {
		return false, err
	}
	st, appErr := p.status.GetUserStatus(msg.PeerID)
	if appErr != nil {
		return false, appErr
	}
	return st != nil && st.Status == model.StatusOnline, nil
}
