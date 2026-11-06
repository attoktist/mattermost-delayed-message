package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pkg/errors"
)

const (
	keyPrefix    = "scheduled_"
	lockPrefix   = "lock_"
	listPageSize = 100
)

type kvAPI interface {
	KVSet(key string, value []byte) *model.AppError
	KVGet(key string) ([]byte, *model.AppError)
	KVDelete(key string) *model.AppError
	KVList(page, count int) ([]string, *model.AppError)
	KVSetWithOptions(key string, value []byte, options model.PluginKVSetOptions) (bool, *model.AppError)
	LogWarn(msg string, keyValuePairs ...interface{})
}

// ...any on plugin.API is an alias of ...interface{}.
var _ kvAPI = plugin.API(nil)

func messageKey(userID, msgID string) string {
	return fmt.Sprintf("%s%s_%s", keyPrefix, userID, msgID)
}

func userPrefix(userID string) string {
	return fmt.Sprintf("%s%s_", keyPrefix, userID)
}

func parseUserAndMessageID(key string) (userID, msgID string, ok bool) {
	if !strings.HasPrefix(key, keyPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(key, keyPrefix)
	user, msg, found := strings.Cut(rest, "_")
	if !found {
		return "", "", false
	}
	return user, msg, true
}

func saveMessage(api kvAPI, msg *ScheduledMessage) error {
	if msg.ID == "" || msg.UserID == "" {
		return errors.New("message id and user id are required")
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return errors.Wrap(err, "failed to marshal message")
	}
	if appErr := api.KVSet(messageKey(msg.UserID, msg.ID), data); appErr != nil {
		return errors.Wrap(appErr, "failed to save message")
	}
	return nil
}

func loadMessage(api kvAPI, userID, msgID string) (*ScheduledMessage, error) {
	data, appErr := api.KVGet(messageKey(userID, msgID))
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to load message")
	}
	if data == nil {
		return nil, nil
	}
	var msg ScheduledMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal message")
	}
	msg.normalizeTargets()
	return &msg, nil
}

func deleteMessage(api kvAPI, userID, msgID string) error {
	if appErr := api.KVDelete(messageKey(userID, msgID)); appErr != nil {
		return errors.Wrap(appErr, "failed to delete message")
	}
	return nil
}

func listMessagesForUser(api kvAPI, userID string) ([]*ScheduledMessage, error) {
	return listMessagesWithPrefix(api, userPrefix(userID))
}

func listAllPendingMessages(api kvAPI) ([]*ScheduledMessage, error) {
	return listMessagesWithPrefix(api, keyPrefix)
}

func listMessagesWithPrefix(api kvAPI, prefix string) ([]*ScheduledMessage, error) {
	var messages []*ScheduledMessage
	page := 0
	for {
		keys, appErr := api.KVList(page, listPageSize)
		if appErr != nil {
			return nil, errors.Wrap(appErr, "failed to list keys")
		}
		if len(keys) == 0 {
			break
		}
		for _, key := range keys {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			data, appErr := api.KVGet(key)
			if appErr != nil {
				api.LogWarn("не удалось загрузить отложенное сообщение", "key", key, "err", appErr.Error())
				continue
			}
			if data == nil {
				continue
			}
			var msg ScheduledMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				api.LogWarn("не удалось разобрать отложенное сообщение", "key", key, "err", err.Error())
				continue
			}
			msg.normalizeTargets()
			messages = append(messages, &msg)
		}
		if len(keys) < listPageSize {
			break
		}
		page++
	}
	return messages, nil
}

// acquireSendLock attempts to grab a per-message lock so only one cluster node
// sends a given scheduled message. Returns true if the lock was acquired.
func acquireSendLock(api kvAPI, msgID string) (bool, error) {
	key := lockPrefix + msgID
	ok, appErr := api.KVSetWithOptions(key, []byte("1"), model.PluginKVSetOptions{
		Atomic:          true,
		OldValue:        nil,
		ExpireInSeconds: 60,
	})
	if appErr != nil {
		return false, errors.Wrap(appErr, "failed to acquire send lock")
	}
	return ok, nil
}

func releaseSendLock(api kvAPI, msgID string) {
	_ = api.KVDelete(lockPrefix + msgID)
}
