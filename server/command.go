package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const commandTrigger = "schedule"

// quotedArgsPattern captures quoted strings or bare tokens.
var quotedArgsPattern = regexp.MustCompile(`"([^"]*)"|(\S+)`)

var urlPattern = regexp.MustCompile(`https?://\S+`)

func (p *Plugin) scheduleCommand() *model.Command {
	cmd := &model.Command{
		Trigger:          commandTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "Отложить сообщение",
		AutoCompleteHint: `"текст" "ГГГГ-ММ-ДД ЧЧ:ММ" [часовой_пояс]`,
		DisplayName:      "Schedule",
		Description:      "Отложить сообщение на будущее время.",
	}

	ac := model.NewAutocompleteData(commandTrigger, `"текст" "ГГГГ-ММ-ДД ЧЧ:ММ" [часовой_пояс]`, "Отложить сообщение")

	list := model.NewAutocompleteData("list", "", "Список ожидающих отложенных сообщений")
	ac.AddCommand(list)

	cancel := model.NewAutocompleteData("cancel", "<id>", "Отменить ожидающее отложенное сообщение")
	cancel.AddTextArgument("ID отложенного сообщения", "<id>", "")
	ac.AddCommand(cancel)

	help := model.NewAutocompleteData("help", "", "Показать справку")
	ac.AddCommand(help)

	cmd.AutocompleteData = ac
	return cmd
}

func (p *Plugin) ExecuteCommand(_ *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	rawArgs := strings.TrimSpace(strings.TrimPrefix(args.Command, "/"+commandTrigger))
	tokens := parseCommandArgs(rawArgs)

	if len(tokens) == 0 {
		return ephemeral(usageText()), nil
	}

	switch strings.ToLower(tokens[0]) {
	case "help":
		return ephemeral(usageText()), nil
	case "list":
		return p.handleListCommand(args)
	case "cancel":
		if len(tokens) < 2 {
			return ephemeral("Использование: `/schedule cancel <id>`"), nil
		}
		return p.handleCancelCommand(args, tokens[1])
	default:
		return p.handleCreateCommand(args, tokens)
	}
}

func (p *Plugin) handleCreateCommand(args *model.CommandArgs, tokens []string) (*model.CommandResponse, *model.AppError) {
	if len(tokens) < 2 {
		return ephemeral("Использование: `/schedule \"текст\" \"ГГГГ-ММ-ДД ЧЧ:ММ\" [часовой_пояс] [repeat=...] [count=N|until=ГГГГ-ММ-ДД]`"), nil
	}

	message := tokens[0]
	timeStr := tokens[1]
	tz := ""
	repeat := ""
	until := ""
	count := 0

	// Positional arg 3, if present and not a key=value flag, is the timezone.
	startFlags := 2
	if len(tokens) >= 3 && !strings.Contains(tokens[2], "=") {
		tz = tokens[2]
		startFlags = 3
	}

	for _, tok := range tokens[startFlags:] {
		key, val, ok := strings.Cut(tok, "=")
		if !ok {
			return ephemeral("Неожиданный аргумент: `" + tok + "`. Ожидается `key=value` (repeat, until, count)."), nil
		}
		switch strings.ToLower(key) {
		case "repeat":
			repeat = strings.ToLower(val)
		case "until":
			until = val
		case "count":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return ephemeral("`count=` должно быть целым числом больше нуля."), nil
			}
			count = n
		default:
			return ephemeral("Неизвестный флаг: `" + key + "`."), nil
		}
	}

	if tz == "" {
		tz = p.userOrDefaultTimezone(args.UserId)
	}

	if strings.TrimSpace(message) == "" {
		return ephemeral("Текст сообщения пуст."), nil
	}

	sendAt, err := parseScheduleTime(timeStr, tz)
	if err != nil {
		return ephemeral("Не удалось разобрать время: " + err.Error()), nil
	}
	if err := validateSendAt(sendAt, scheduleNow().UTC()); err != nil {
		return ephemeral(err.Error()), nil
	}

	endsMode := ""
	switch {
	case until != "" && count != 0:
		return ephemeral("Укажите либо `until=`, либо `count=`, но не оба."), nil
	case until != "":
		endsMode = EndsOn
	case count != 0:
		endsMode = EndsAfter
	}
	if err := validateRecurrence(repeat, endsMode, count, until != ""); err != nil {
		return ephemeral(err.Error()), nil
	}

	if err := checkCanPost(p.gate, args.UserId, args.ChannelId); err != nil {
		return ephemeral("В этот канал писать нельзя."), nil
	}
	pending, err := listMessagesForUser(p.kv, args.UserId)
	if err != nil {
		p.logAPIError("не удалось загрузить список отложенных сообщений", err)
		return ephemeral("Не удалось загрузить отложенные сообщения."), nil
	}
	if len(pending) >= maxPendingPerUser {
		return ephemeral(fmt.Sprintf("Достигнут лимит: %d отложенных сообщений.", maxPendingPerUser)), nil
	}
	if utf8.RuneCountInString(message) > maxPostSize(p.gate) {
		return ephemeral("Сообщение длиннее допустимого."), nil
	}

	msg := &ScheduledMessage{
		ID:         model.NewId(),
		ChannelIDs: []string{args.ChannelId},
		UserID:     args.UserId,
		Message:    message,
		SendAt:     sendAt.UnixMilli(),
		CreatedAt:  time.Now().UTC().UnixMilli(),
		Status:     StatusPending,
		Timezone:   tz,
		Repeat:     repeat,
	}
	if repeat != "" {
		msg.EndsMode = endsMode
		switch endsMode {
		case EndsOn:
			endsAt, err := parseScheduleTime(until+" 23:59", tz)
			if err != nil {
				return ephemeral("Не удалось разобрать дату `until`: " + err.Error()), nil
			}
			msg.EndsAt = endsAt.UnixMilli()
		case EndsAfter:
			msg.EndsAfter = count
		}
	}
	if err := saveMessage(p.kv, msg); err != nil {
		p.logKVSaveError(err)
		return ephemeral("Не удалось сохранить отложенное сообщение."), nil
	}

	return ephemeral(fmt.Sprintf("Отложенное сообщение `%s` — первая отправка %s%s.",
		msg.ID, formatSendAt(msg.SendAt, tz), repeatSuffix(msg))), nil
}

// repeatSuffix renders a short " (repeats weekly until ...)" hint after the
// initial send time in the create-confirmation message.
func repeatSuffix(msg *ScheduledMessage) string {
	if msg.Repeat == "" {
		return ""
	}
	parts := []string{"повтор " + msg.Repeat}
	switch msg.EndsMode {
	case EndsOn:
		parts = append(parts, "до "+formatSendAt(msg.EndsAt, msg.Timezone))
	case EndsAfter:
		parts = append(parts, fmt.Sprintf("ещё %d раз", msg.EndsAfter))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func (p *Plugin) handleListCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	msgs, err := listMessagesForUser(p.kv, args.UserId)
	if err != nil {
		p.logAPIError("не удалось загрузить список отложенных сообщений", err)
		return ephemeral("Не удалось загрузить отложенные сообщения."), nil
	}
	if len(msgs) == 0 {
		return ephemeral("Нет ожидающих отложенных сообщений."), nil
	}

	sort.Slice(msgs, func(i, j int) bool { return msgs[i].SendAt < msgs[j].SendAt })

	defaultTz := p.userOrDefaultTimezone(args.UserId)
	var sb strings.Builder
	sb.WriteString("**Ваши отложенные сообщения**\n\n| ID | Когда | Повтор | Статус | Текст |\n|---|---|---|---|---|\n")
	for _, m := range msgs {
		tz := m.Timezone
		if tz == "" {
			tz = defaultTz
		}
		msg := strings.ReplaceAll(m.Message, "|", `\|`)
		msg = backtickURLs(msg)
		fmt.Fprintf(&sb, "| `%s` | %s | %s | %s | %s |\n",
			m.ID,
			listWhen(m, tz),
			repeatSummary(m),
			m.Status,
			truncate(msg, 80),
		)
	}
	return ephemeral(sb.String()), nil
}

// listWhen is the When column of /schedule list. A presence message has
// SendAt 0 for its whole wait; formatSendAt would render the Unix epoch.
func listWhen(msg *ScheduledMessage, tz string) string {
	if msg.Trigger == TriggerOnline {
		return "Когда будет в сети"
	}
	return formatSendAt(msg.SendAt, tz)
}

// repeatSummary renders a compact recurrence description for the list view.
// Returns "—" for one-offs.
func repeatSummary(msg *ScheduledMessage) string {
	if msg.Repeat == "" {
		return "—"
	}
	switch msg.EndsMode {
	case EndsOn:
		return fmt.Sprintf("%s до %s", msg.Repeat, formatSendAt(msg.EndsAt, msg.Timezone))
	case EndsAfter:
		left := msg.EndsAfter - msg.Occurrences
		if left < 0 {
			left = 0
		}
		return fmt.Sprintf("%s · осталось %d", msg.Repeat, left)
	}
	return msg.Repeat
}

func (p *Plugin) handleCancelCommand(args *model.CommandArgs, msgID string) (*model.CommandResponse, *model.AppError) {
	existing, err := loadMessage(p.kv, args.UserId, msgID)
	if err != nil {
		p.logAPIError("не удалось загрузить отложенное сообщение", err)
		return ephemeral("Не удалось отменить."), nil
	}
	if existing == nil {
		return ephemeral("Сообщение не найдено."), nil
	}
	if err := deleteMessage(p.kv, args.UserId, msgID); err != nil {
		p.logAPIError("не удалось удалить отложенное сообщение", err)
		return ephemeral("Не удалось отменить."), nil
	}
	return ephemeral("Отменено отложенное сообщение `" + msgID + "`."), nil
}

func usageText() string {
	return strings.Join([]string{
		"Отложенные сообщения.",
		"/schedule \"текст\" \"ГГГГ-ММ-ДД ЧЧ:ММ\" [часовой_пояс]",
		"/schedule list",
		"/schedule cancel <id>",
		"Флаги: repeat:daily|weekdays|weekly|fortnightly|monthly|yearly, count:N, until:ГГГГ-ММ-ДД.",
		"count и until вместе нельзя. Окно, несколько каналов и ротация — только через модалку.",
	}, "\n")
}

// userOrDefaultTimezone reads the user through the post gate. Automatic
// timezone wins when enabled and non-empty, then manual, then the plugin
// DefaultTimezone, then UTC. The configuration snapshot is not modified.
func (p *Plugin) userOrDefaultTimezone(userID string) string {
	if user, appErr := p.gate.GetUser(userID); appErr == nil && user != nil && user.Timezone != nil {
		automatic := user.Timezone["automaticTimezone"]
		if user.Timezone["useAutomaticTimezone"] == "true" && automatic != "" {
			return automatic
		}
		if manual := user.Timezone["manualTimezone"]; manual != "" {
			return manual
		}
	}
	configured := p.getConfiguration().DefaultTimezone
	if configured != "" {
		return configured
	}
	return "UTC"
}

func ephemeral(text string) *model.CommandResponse {
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         text,
	}
}

// backtickURLs wraps any http(s) URLs in s with backticks so they render as
// inline code in markdown — keeps Mattermost from auto-linking URLs in the
// list ephemeral and triggering link previews on a summary of a future post.
func backtickURLs(s string) string {
	return urlPattern.ReplaceAllStringFunc(s, func(u string) string {
		return "`" + u + "`"
	})
}

// parseCommandArgs splits a slash-command argument string preserving quoted segments.
// Example: `"hello world" "2025-06-01 09:30" UTC` -> ["hello world", "2025-06-01 09:30", "UTC"]
func parseCommandArgs(input string) []string {
	matches := quotedArgsPattern.FindAllStringSubmatch(input, -1)
	args := make([]string, 0, len(matches))
	for _, m := range matches {
		if strings.HasPrefix(m[0], `"`) {
			// quoted segment — keep even if empty
			args = append(args, m[1])
		} else if m[2] != "" {
			args = append(args, m[2])
		}
	}
	return args
}

// truncate returns the first n runes of s, appending "…" if truncated.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
