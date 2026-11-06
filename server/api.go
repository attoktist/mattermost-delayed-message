package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
)

const userIDHeader = "Mattermost-User-Id"

func (p *Plugin) handleHTTP(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get(userIDHeader)
	if userID == "" {
		http.Error(w, "нет доступа", http.StatusUnauthorized)
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/list":
		p.apiList(w, r, userID)
	case r.Method == http.MethodPost && r.URL.Path == "/api/create":
		p.apiCreate(w, r, userID)
	case r.Method == http.MethodPost && r.URL.Path == "/api/send":
		p.apiSend(w, r, userID)
	case r.Method == http.MethodDelete && r.URL.Path == "/api/cancel":
		p.apiCancel(w, r, userID)
	case r.Method == http.MethodPatch && r.URL.Path == "/api/update":
		p.apiUpdate(w, r, userID)
	default:
		http.NotFound(w, r)
	}
}

func (p *Plugin) apiList(w http.ResponseWriter, r *http.Request, userID string) {
	channelID := r.URL.Query().Get("channel_id")
	if channelID != "" && !model.IsValidId(channelID) {
		http.Error(w, "некорректный канал", http.StatusBadRequest)
		return
	}
	msgs, err := listMessagesForUser(p.kv, userID)
	if err != nil {
		p.logAPIError("api: не удалось загрузить список", err)
		http.Error(w, "не удалось загрузить список", http.StatusInternalServerError)
		return
	}
	if channelID != "" {
		kept := make([]*ScheduledMessage, 0, len(msgs))
		for _, msg := range msgs {
			if msg.Status == StatusPending && slices.Contains(msg.Targets(), channelID) {
				kept = append(kept, msg)
			}
		}
		msgs = kept
	}
	if msgs == nil {
		msgs = []*ScheduledMessage{}
	}
	writeJSON(w, http.StatusOK, msgs)
}

// scheduleRequest is the create/update body. user_id is not a field: the
// author is the Mattermost-User-Id header, never the JSON.
type scheduleRequest struct {
	ID         string   `json:"id,omitempty"`
	ChannelIDs []string `json:"channel_ids"`
	ChannelID  string   `json:"channel_id,omitempty"`
	Message    string   `json:"message"`
	SendAt     string   `json:"send_at"`
	Timezone   string   `json:"timezone"`
	Repeat     string   `json:"repeat,omitempty"`
	EndsMode   string   `json:"ends_mode,omitempty"`
	EndsOn     string   `json:"ends_on,omitempty"`
	EndsAfter  int      `json:"ends_after,omitempty"`
	WindowMs   int64    `json:"window_ms,omitempty"`
	Messages   []string `json:"messages,omitempty"`
	Trigger    string   `json:"trigger,omitempty"`
}

type acceptedSchedule struct {
	targets   []string
	message   string
	messages  []string
	sendAt    int64
	timezone  string
	repeat    string
	endsMode  string
	endsAt    int64
	endsAfter int
	windowMs  int64
	trigger   string
	peerID    string
}

func (p *Plugin) apiCreate(w http.ResponseWriter, r *http.Request, userID string) {
	var body scheduleRequest
	if !decodeBody(w, r, &body) {
		return
	}
	fields, ok := p.acceptSchedule(w, userID, &body, true)
	if !ok {
		return
	}

	msg := &ScheduledMessage{
		ID:         model.NewId(),
		ChannelIDs: fields.targets,
		UserID:     userID,
		Message:    fields.message,
		SendAt:     fields.sendAt,
		CreatedAt:  time.Now().UTC().UnixMilli(),
		Status:     StatusPending,
		Timezone:   fields.timezone,
		Repeat:     fields.repeat,
		WindowMs:   fields.windowMs,
		EndsMode:   fields.endsMode,
		EndsAt:     fields.endsAt,
		EndsAfter:  fields.endsAfter,
		Trigger:    fields.trigger,
		PeerID:     fields.peerID,
	}
	if len(fields.messages) > 0 {
		msg.Messages = fields.messages
	}
	msg.FireAt = randomFireAt(msg.SendAt, msg.WindowMs)
	if err := saveMessage(p.kv, msg); err != nil {
		p.logKVSaveError(err)
		http.Error(w, "не удалось сохранить", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, msg)
}

func (p *Plugin) apiUpdate(w http.ResponseWriter, r *http.Request, userID string) {
	var body scheduleRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if body.ID == "" {
		http.Error(w, "нужен id", http.StatusBadRequest)
		return
	}
	existing, err := loadMessage(p.kv, userID, body.ID)
	if err != nil {
		p.logAPIError("api: не удалось загрузить", err)
		http.Error(w, "не удалось загрузить", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		http.Error(w, "сообщение не найдено", http.StatusNotFound)
		return
	}
	if existing.Status != StatusPending {
		http.Error(w, "править можно только ожидающее сообщение", http.StatusConflict)
		return
	}
	// A presence row with no send_at stays presence: only the text changes.
	if existing.Trigger == TriggerOnline && body.Trigger == "" && strings.TrimSpace(body.SendAt) == "" {
		body.Message = strings.TrimSpace(body.Message)
		if body.Message == "" {
			http.Error(w, "нужен текст", http.StatusBadRequest)
			return
		}
		if textTooLong(p.gate, body.Message, nil) {
			http.Error(w, "сообщение длиннее допустимого", http.StatusBadRequest)
			return
		}
		existing.Message = body.Message
		if err := saveMessage(p.kv, existing); err != nil {
			p.logKVSaveError(err)
			http.Error(w, "не удалось сохранить", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, existing)
		return
	}

	fields, ok := p.acceptSchedule(w, userID, &body, false)
	if !ok {
		return
	}

	existing.ChannelIDs = fields.targets
	existing.ChannelID = ""
	existing.Message = fields.message
	existing.SendAt = fields.sendAt
	existing.Timezone = fields.timezone
	existing.Repeat = fields.repeat
	existing.WindowMs = fields.windowMs
	existing.FireAt = randomFireAt(existing.SendAt, existing.WindowMs)
	existing.EndsMode = fields.endsMode
	existing.EndsAt = fields.endsAt
	existing.EndsAfter = fields.endsAfter
	existing.Trigger = fields.trigger
	existing.PeerID = fields.peerID
	if !equalStrings(existing.Messages, fields.messages) {
		existing.Messages = fields.messages
		existing.MessageCycle = nil
		existing.MessageCyclePos = 0
		existing.LastSentIndex = nil
	}

	if err := saveMessage(p.kv, existing); err != nil {
		p.logKVSaveError(err)
		http.Error(w, "не удалось сохранить", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

func (p *Plugin) apiCancel(w http.ResponseWriter, r *http.Request, userID string) {
	var body struct {
		ID string `json:"id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.ID == "" {
		http.Error(w, "нужен id", http.StatusBadRequest)
		return
	}

	existing, err := loadMessage(p.kv, userID, body.ID)
	if err != nil {
		p.logAPIError("api: не удалось загрузить", err)
		http.Error(w, "не удалось загрузить", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		http.Error(w, "сообщение не найдено", http.StatusNotFound)
		return
	}
	if err := deleteMessage(p.kv, userID, body.ID); err != nil {
		p.logAPIError("api: не удалось удалить", err)
		http.Error(w, "не удалось удалить", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *Plugin) apiSend(w http.ResponseWriter, r *http.Request, userID string) {
	var body struct {
		ID string `json:"id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.ID == "" {
		http.Error(w, "нужен id", http.StatusBadRequest)
		return
	}
	existing, err := loadMessage(p.kv, userID, body.ID)
	if err != nil {
		p.logAPIError("api: не удалось загрузить", err)
		http.Error(w, "не удалось загрузить", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		http.Error(w, "сообщение не найдено", http.StatusNotFound)
		return
	}
	if existing.Status != StatusPending {
		http.Error(w, "править можно только ожидающее сообщение", http.StatusConflict)
		return
	}
	p.deliver(existing, p.getConfiguration().MaxAttempts, true)
	w.WriteHeader(http.StatusNoContent)
}

// scheduleNow is the lead-time clock. The default-zone fixture uses a fixed
// wall date, so the test pins this instead of time.Now.
var scheduleNow = time.Now

// acceptSchedule applies the create checks. quota is false for update.
// The order is the body contract: trigger, trim, channels, caps, optional quota,
// permission, rune length. TriggerOnline then calls validatePresence and skips
// send time, recurrence and extras. An empty trigger keeps that timed path.
func (p *Plugin) acceptSchedule(w http.ResponseWriter, userID string, body *scheduleRequest, quota bool) (*acceptedSchedule, bool) {
	if body.Trigger != "" && body.Trigger != TriggerOnline {
		http.Error(w, "неизвестный тип отправки", http.StatusBadRequest)
		return nil, false
	}
	body.Message = strings.TrimSpace(body.Message)
	body.Messages = trimMessages(body.Messages)

	targets := normalizeChannelIDs(body.ChannelIDs, body.ChannelID)
	if len(targets) == 0 {
		http.Error(w, "нужен канал", http.StatusBadRequest)
		return nil, false
	}
	if body.Message == "" && len(body.Messages) == 0 {
		http.Error(w, "нужен текст", http.StatusBadRequest)
		return nil, false
	}
	if len(targets) > maxChannelsPerMessage {
		http.Error(w, "слишком много каналов", http.StatusBadRequest)
		return nil, false
	}
	if len(body.Messages) > maxRotationMessages {
		http.Error(w, "слишком много текстов", http.StatusBadRequest)
		return nil, false
	}
	if quota {
		existing, err := listMessagesForUser(p.kv, userID)
		if err != nil {
			p.logAPIError("api: не удалось загрузить список", err)
			http.Error(w, "не удалось загрузить список", http.StatusInternalServerError)
			return nil, false
		}
		if len(existing) >= maxPendingPerUser {
			http.Error(w, "достигнут лимит: 100 отложенных сообщений", http.StatusConflict)
			return nil, false
		}
	}
	for _, channelID := range targets {
		if err := checkCanPost(p.gate, userID, channelID); err != nil {
			if errors.Is(err, errInvalidChannel) {
				http.Error(w, "некорректный канал", http.StatusBadRequest)
				return nil, false
			}
			http.Error(w, "нельзя писать в канал", http.StatusForbidden)
			return nil, false
		}
	}
	if textTooLong(p.gate, body.Message, body.Messages) {
		http.Error(w, "сообщение длиннее допустимого", http.StatusBadRequest)
		return nil, false
	}
	if body.Trigger == TriggerOnline {
		peer, err := validatePresence(p.gate, userID, targets, body.Repeat, body.WindowMs, body.Messages)
		if err != nil {
			writePresenceError(w, err)
			return nil, false
		}
		return &acceptedSchedule{
			targets: targets,
			message: body.Message,
			trigger: TriggerOnline,
			peerID:  peer,
		}, true
	}

	// Empty body timezone is the user's zone, then DefaultTimezone, then UTC.
	// userOrDefaultTimezone copies the string and does not write the snapshot.
	tz := body.Timezone
	if tz == "" {
		tz = p.userOrDefaultTimezone(userID)
	}

	sendAt, err := parseScheduleTime(body.SendAt, tz)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil, false
	}
	if err := validateSendAt(sendAt, scheduleNow().UTC()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil, false
	}
	if err := validateRecurrence(body.Repeat, body.EndsMode, body.EndsAfter, body.EndsOn != ""); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil, false
	}
	if err := validateExtras(body.Repeat, body.WindowMs, body.Messages); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil, false
	}

	primary := body.Message
	if len(body.Messages) > 0 {
		primary = body.Messages[0]
	}
	out := &acceptedSchedule{
		targets:  targets,
		message:  primary,
		messages: body.Messages,
		sendAt:   sendAt.UnixMilli(),
		timezone: tz,
		repeat:   body.Repeat,
		windowMs: body.WindowMs,
	}
	if body.Repeat != "" {
		out.endsMode = body.EndsMode
		if body.EndsMode == EndsAfter {
			out.endsAfter = body.EndsAfter
		}
		if body.EndsMode == EndsOn {
			endsAt, err := parseScheduleTime(body.EndsOn+" 23:59", tz)
			if err != nil {
				http.Error(w, "некорректная дата окончания: "+err.Error(), http.StatusBadRequest)
				return nil, false
			}
			out.endsAt = endsAt.UnixMilli()
		}
	}
	return out, true
}

// textTooLong compares each text with maxPostSize using runes. maxPostSize
// is PostMessageMaxRunesV2 and is not a byte cap.
func textTooLong(api postGate, message string, messages []string) bool {
	limit := maxPostSize(api)
	if message != "" && utf8.RuneCountInString(message) > limit {
		return true
	}
	for _, m := range messages {
		if utf8.RuneCountInString(m) > limit {
			return true
		}
	}
	return false
}

// normalizeChannelIDs collapses channel_ids and the legacy channel_id into
// one deduplicated slice. The legacy id is used only when channel_ids is
// empty. Order follows the first occurrence.
func normalizeChannelIDs(channelIDs []string, legacyChannelID string) []string {
	if len(channelIDs) == 0 && legacyChannelID != "" {
		channelIDs = []string{legacyChannelID}
	}
	seen := make(map[string]struct{}, len(channelIDs))
	out := make([]string, 0, len(channelIDs))
	for _, id := range channelIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func writePresenceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalidChannel):
		http.Error(w, "некорректный канал", http.StatusBadRequest)
	case errors.Is(err, errCannotPost):
		http.Error(w, "нельзя писать в канал", http.StatusForbidden)
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxHTTPBodyBytes))
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		http.Error(w, "некорректное тело", http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (p *Plugin) logAPIError(msg string, err error) {
	if p.API != nil {
		p.API.LogError(msg, "err", err.Error())
	}
}

func (p *Plugin) logKVSaveError(err error) {
	if p.API != nil {
		p.API.LogError("не удалось сохранить отложенное сообщение: " + err.Error())
	}
}
