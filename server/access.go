package main

import (
	"errors"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const (
	maxPendingPerUser      = 100
	maxChannelsPerMessage  = 10
	maxRotationMessages    = 20
	maxHTTPBodyBytes       = 1 << 20
	maxConfiguredAttempts  = 10
	minPollIntervalSeconds = 5
)

var (
	errInvalidChannel = errors.New("invalid channel id")
	errCannotPost     = errors.New("cannot post in channel")
	errUserGone       = errors.New("user gone")
)

type postGate interface {
	GetChannel(channelID string) (*model.Channel, *model.AppError)
	HasPermissionToChannel(userID, channelID string, permission *model.Permission) bool
	GetUser(userID string) (*model.User, *model.AppError)
	GetConfig() *model.Config
}

var _ postGate = plugin.API(nil)

func checkCanPost(api postGate, userID, channelID string) error {
	if !model.IsValidId(channelID) {
		return errInvalidChannel
	}
	channel, appErr := api.GetChannel(channelID)
	if appErr != nil || channel == nil || channel.DeleteAt != 0 {
		return errCannotPost
	}
	if !api.HasPermissionToChannel(userID, channelID, model.PermissionCreatePost) {
		return errCannotPost
	}
	return nil
}

func userActive(api postGate, userID string) error {
	user, appErr := api.GetUser(userID)
	if appErr != nil {
		if appErr.StatusCode == 404 {
			return errUserGone
		}
		return appErr
	}
	if user == nil || user.DeleteAt != 0 {
		return errUserGone
	}
	return nil
}

func maxPostSize(api postGate) int {
	return model.PostMessageMaxRunesV2
}
