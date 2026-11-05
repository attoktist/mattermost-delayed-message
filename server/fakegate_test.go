package main

import "github.com/mattermost/mattermost/server/public/model"

// fakeGate is an in-memory postGate. Permission checks record the pointer
// they were given so tests can require model.PermissionCreatePost.
type fakeGate struct {
	channel     *model.Channel
	channelErr  *model.AppError
	channelGets int

	userIDSeen     string
	channelIDSeen  string
	permissionSeen *model.Permission
	permCalls      int
	allow          bool

	user    *model.User
	userErr *model.AppError

	config *model.Config
}

func (f *fakeGate) GetChannel(string) (*model.Channel, *model.AppError) {
	f.channelGets++
	return f.channel, f.channelErr
}

func (f *fakeGate) HasPermissionToChannel(userID, channelID string, permission *model.Permission) bool {
	f.permCalls++
	f.userIDSeen = userID
	f.channelIDSeen = channelID
	f.permissionSeen = permission
	return f.allow
}

func (f *fakeGate) GetUser(string) (*model.User, *model.AppError) {
	return f.user, f.userErr
}

func (f *fakeGate) GetConfig() *model.Config {
	return f.config
}

var _ postGate = (*fakeGate)(nil)
