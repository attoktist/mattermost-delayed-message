# Delayed Messages for Mattermost 12.0.1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Плагин `com.attoktist.scheduled-posts` 1.0.0 повторяет поведение bed42/mattermost-scheduled-posts-plugin v1.2.0 (`0b45fb0`) и включается на Mattermost Team Edition 12.0.1 без лицензии и без модуля v6.

**Architecture:** Домен (время, повторы, KV, опрос, slash, HTTP, UI) переносится файлами из источника. Хостовый контракт — `server/public` v0.4.3 и React 19 automatic JSX. Проверки права писать и лимиты — новые функции вокруг create/update/slash/send, клиенту не доверяем.

**Tech Stack:** Go 1.26.10, `github.com/mattermost/mattermost/server/public` v0.4.3, `github.com/pkg/errors` v0.9.1, `github.com/stretchr/testify` v1.12.1, webpack/babel/TypeScript со starter template, React хоста.

**Spec:** `docs/superpowers/specs/2026-11-05-scheduled-posts-mm12-design.md`

## Global Constraints

- Ветка `feat/scheduled-posts-mm12` от `main`. Модуль `github.com/attoktist/mattermost-delayed-message`.
- id `com.attoktist.scheduled-posts`, name `Delayed Messages`, version `1.0.0`, `min_server_version` `12.0.0`, trigger `schedule`.
- `go` 1.26.10. Единственный Mattermost require: `github.com/mattermost/mattermost/server/public v0.4.3`. Не импортировать `mattermost-server/v6`, `pluginapi`, `cluster.Job`, testcontainers, docker.
- Заголовок пользователя только `Mattermost-User-Id`. Поле `user_id` в JSON не задаёт автора.
- `checkCanPost` на create, update, slash и перед каждым `CreatePost`: `model.IsValidId`, канал с `DeleteAt == 0`, `HasPermissionToChannel(..., model.PermissionCreatePost)`.
- Лимиты: 100 записей KV на пользователя, 10 каналов, 20 текстов ротации, тело HTTP 1 MiB, длина текста = `MaxPostSize` (0 → 16383 байт), `MaxAttempts` вне `[1,10]` зажимается, `PollIntervalSeconds` < 5 становится 5.
- Квота — HTTP 409. Кривой ввод — 400. Нет права / нет канала / архив — 403 `forbidden` без id канала и без `err.Error()` KV/CreatePost.
- `CreatePost` заполняет только `ChannelId`, `UserId`, `Message`.
- Удалённый автор (`GetUser` 404 или `DeleteAt != 0`) на send: удалить запись, не постить. Прочая ошибка `GetUser` — обычный retry.
- Семантика повторов, окна, ротации, частичного фейла, lock TTL 60с, GC completed 7 суток — как в источнике `0b45fb0`.
- UI-строки английские. README русский. MIT: copyright Andrew Bednarz (2026) и Eugene Khramchenkov (2026).
- Приёмка без живого Mattermost: `go test ./...`, `govulncheck ./...`, `npm run build`, `npx tsc --noEmit`.
- Коммиты из PowerShell: `git commit -m "сообщение"`. Не `--no-verify`, не amend, не push.

## File Structure

```
go.mod                          модуль и v0.4.3
plugin.json                     манифест и три настройки
LICENSE                         оба copyright
README.md                       сборка PowerShell, установка, slash, govulncheck
Makefile                        POSIX-дубль тех же команд, не единственный путь
.gitignore
server/main.go                  plugin.ClientMain
server/plugin.go                OnActivate/OnDeactivate/ServeHTTP/ExecuteCommand
server/configuration.go         снимок настроек
server/model.go                 ScheduledMessage и константы статусов/повторов
server/timeutil.go              разбор времени, nextOccurrence, seriesEnded
server/rotation.go              окно, ротация, validateExtras
server/store.go                 KV, lock, kvAPI
server/access.go                checkCanPost, userActive, maxPostSize, лимиты
server/api.go                   HTTP
server/command.go               /schedule
server/scheduler.go             опрос и dispatch
server/*_test.go                рядом с файлом, package main
webapp/src/jsx_runtime.ts       копия со starter, без правок
webapp/webpack.config.js        externals + alias jsx runtime
webapp/babel.config.js          runtime automatic
webapp/src/**                   UI источника, PLUGIN_ID новый
```

Один `package main` в `server/`. Узкие интерфейсы, чтобы тесты не реализовывали весь `plugin.API`.

## Review Focus

Пять входов, на которых ошибка молча меняет автора, крутит CPU или режет границу лимита. Тесты на них стоят в задачах ниже.

- JSON create с чужим `user_id` сохраняет и шлёт сообщение от пользователя из заголовка. Task 7, `TestCreateIgnoresBodyUserID`.
- Время ровно через 30с принимается, через 29с — нет. Task 2, `TestValidateSendAtBoundary`.
- 10 каналов принимаются, 11-й — 400, дубликаты одного id считаются одним каналом. Task 7, `TestChannelCountLimit`.
- Текст длиной `MaxPostSize` байт принимается, на байт длиннее — 400. Task 7, `TestMessageSizeLimit`.
- `PollIntervalSeconds` 0 и отрицательное становятся 5. Task 6, `TestPollIntervalFloor`.

---

### Task 1: Ветка и пустой плагин, который компилируется

**Files:**
- Create: `plugin.json`, `server/main.go`, `server/plugin.go`, `server/plugin_test.go`, `go.mod`, `.gitignore`
- Modify: `LICENSE`, `README.md` (заглушка в одну строку, полный текст в Task 11)

**Interfaces:**
- Consumes: ничего
- Produces: модуль `github.com/attoktist/mattermost-delayed-message`; `const pluginID = "com.attoktist.scheduled-posts"`; тип `Plugin` с встроенным `plugin.MattermostPlugin`

- [ ] **Step 1: Ветка**

```powershell
git checkout -b feat/scheduled-posts-mm12
```

- [ ] **Step 2: Падающий тест**

`server/plugin_test.go`: `TestPluginID` требует `pluginID == "com.attoktist.scheduled-posts"`.

Run: `go test ./server -run TestPluginID -count=1`
Expected: FAIL, пакет или константа не определены.

- [ ] **Step 3: Каркас**

`go.mod`:

```
module github.com/attoktist/mattermost-delayed-message

go 1.26.10

require (
    github.com/mattermost/mattermost/server/public v0.4.3
    github.com/pkg/errors v0.9.1
    github.com/stretchr/testify v1.12.1
)
```

`server/main.go` вызывает `plugin.ClientMain(&Plugin{})`. `plugin.go` содержит константу и пустые `OnActivate() error`, `OnDeactivate() error`. `plugin.json`: id, name, version `1.0.0`, `min_server_version` `12.0.0`, пять executables как в дизайне, `webapp.bundle_path` `webapp/dist/main.js`, три settings источника (`PollIntervalSeconds` default 30, `MaxAttempts` default 3, `DefaultTimezone` default `UTC`). LICENSE — MIT с двумя строками copyright. `.gitignore` — `dist/`, `server/dist/`, `webapp/dist/`, `webapp/node_modules/`.

`go get` / `go mod tidy` из корня. В `go.mod` не должно появиться `mattermost-server/v6`.

- [ ] **Step 4: Тест зелёный**

Run: `go test ./server -run TestPluginID -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add go.mod go.sum plugin.json LICENSE .gitignore server/main.go server/plugin.go server/plugin_test.go docs/superpowers
git commit -m "feat: scaffold Mattermost 12.0.1 plugin module"
```

### Task 2: Время и повторы

**Files:**
- Create: `server/model.go`, `server/timeutil.go`, `server/timeutil_test.go`
- Источник поведения: `server/store.go` (только тип и константы статусов/повторов/ends), `server/utils.go` (`parseScheduleTime`, `validateSendAt`, `formatSendAt`, `nextOccurrence`, `seriesEnded`, `validateRecurrence`), `server/recurrence_test.go` репозитория bed42 @ `0b45fb0`

**Interfaces:**
- Consumes: `package main`
- Produces:
  - константы `StatusPending`, `StatusSent`, `StatusFailed`, `StatusCompleted`, `RepeatNone`…`RepeatYearly`, `EndsNever`, `EndsOn`, `EndsAfter`
  - `type ScheduledMessage struct` с json-тегами источника, включая `ChannelIDs`, legacy `ChannelID`, `WindowMs`, `FireAt`, `Messages`, `MessageCycle`, `MessageCyclePos`, `LastSentIndex`
  - `func (m *ScheduledMessage) Targets() []string`
  - `func (m *ScheduledMessage) normalizeTargets()`
  - `func (m *ScheduledMessage) effectiveFireAt() int64`
  - `func parseScheduleTime(timeStr, tz string) (time.Time, error)`
  - `func validateSendAt(sendAt, now time.Time) error` — отказ, если `sendAt` раньше `now+30s`
  - `func nextOccurrence(current time.Time, repeat, tz string) (time.Time, error)`
  - `func seriesEnded(msg *ScheduledMessage, next time.Time) bool`
  - `func validateRecurrence(repeat, endsMode string, endsAfter int, hasEndsAt bool) error`
  - `const minLeadTime = 30 * time.Second`

- [ ] **Step 1: Перенести тесты повторов из источника и добавить границу 30с**

Скопировать тесты из `recurrence_test.go` источника, поправив только импорт модуля (пакет тот же `main`). Добавить `TestValidateSendAtBoundary`: `now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)`; `validateSendAt(now.Add(30*time.Second), now)` возвращает nil; `validateSendAt(now.Add(29*time.Second), now)` возвращает ошибку.

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `go test ./server -run "Recurrence|ValidateSendAt|NextOccurrence|SeriesEnded" -count=1`
Expected: FAIL компиляцией.

- [ ] **Step 3: Перенести функции**

Перенести тип, константы и функции из списка Interfaces без изменения арифметики. Импорт модели Mattermost не нужен в этом файле. `model.NewId` здесь не вызывать.

- [ ] **Step 4: Тесты зелёные**

Run: `go test ./server -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add server/model.go server/timeutil.go server/timeutil_test.go
git commit -m "feat: port schedule time and recurrence rules"
```

### Task 3: Окно и ротация

**Files:**
- Create: `server/rotation.go`, `server/rotation_test.go`
- Источник: `validateExtras`, `randomFireAt`, `newShuffledCycle`, `pickMessageBody`, `needsNewCycle`, `trimMessages`, `equalStrings`, `randIntn`/`randInt63n` и `extras_test.go` @ `0b45fb0`

**Interfaces:**
- Consumes: `ScheduledMessage`, константы повторов, `maxWindowMs` = 24ч
- Produces:
  - `var randIntn func(n int) int`, `var randInt63n func(n int64) int64` — боевье значение `math/rand`, тесты подменяют
  - `func validateExtras(repeat string, windowMs int64, messages []string) error`
  - `func randomFireAt(sendAt, windowMs int64) int64`
  - `func pickMessageBody(msg *ScheduledMessage) (string, int)`
  - `func trimMessages(in []string) []string`
  - `func equalStrings(a, b []string) bool`

- [ ] **Step 1: Перенести `extras_test.go`**

Имена и утверждения не менять. Плюс `TestValidateExtrasRejectsThirdMessageWithoutRepeat`: `validateExtras("", 0, []string{"a", "b"})` возвращает ошибку. `TestWindowCap`: `validateExtras("daily", 24*time.Hour.Milliseconds(), nil)` — nil; на миллисекунду больше — ошибка.

- [ ] **Step 2: Падение**

Run: `go test ./server -run "Extras|Window|Rotation|PickMessage" -count=1`
Expected: FAIL компиляцией.

- [ ] **Step 3: Перенести функции как есть**

- [ ] **Step 4: Зелёные**

Run: `go test ./server -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add server/rotation.go server/rotation_test.go
git commit -m "feat: port random window and message rotation"
```

### Task 4: KV и lock

**Files:**
- Create: `server/store.go`, `server/store_test.go`, `server/fakekv_test.go`
- Источник: функции KV и `store_test.go` @ `0b45fb0`

**Interfaces:**
- Consumes: `ScheduledMessage`
- Produces:
  - `type kvAPI interface { KVSet(key string, value []byte) *model.AppError; KVGet(key string) ([]byte, *model.AppError); KVDelete(key string) *model.AppError; KVList(page, count int) ([]string, *model.AppError); KVSetWithOptions(key string, value []byte, options model.PluginKVSetOptions) (bool, *model.AppError); LogWarn(msg string, keyValuePairs ...interface{}) }`
  - в `store.go` строка `var _ kvAPI = plugin.API(nil)` — метод-сет `plugin.API` покрывает интерфейс
  - `func messageKey(userID, msgID string) string` → `scheduled_<userID>_<msgID>`
  - `func saveMessage(api kvAPI, msg *ScheduledMessage) error`
  - `func loadMessage(api kvAPI, userID, msgID string) (*ScheduledMessage, error)` — чужой userID не читает чужой ключ; nil, nil если нет
  - `func deleteMessage(api kvAPI, userID, msgID string) error`
  - `func listMessagesForUser(api kvAPI, userID string) ([]*ScheduledMessage, error)`
  - `func listAllPendingMessages(api kvAPI) ([]*ScheduledMessage, error)` — имя источника сохранено, выбирает все ключи с префиксом `scheduled_`
  - `func acquireSendLock(api kvAPI, msgID string) (bool, error)` — atomic, `OldValue: nil`, `ExpireInSeconds: 60`
  - `func releaseSendLock(api kvAPI, msgID string)`
  - `const listPageSize = 100`

- [ ] **Step 1: Fake и тесты**

`fakekv_test.go`: map-хранилище, реализующее `kvAPI`. `KVSetWithOptions` с `Atomic` и пустым old успешен только если ключа нет. Перенести сценарии `store_test.go` источника на этот fake. Добавить `TestLoadMessageIsScopedToUser`: запись user A не читается запросом user B.

- [ ] **Step 2: Падение**

Run: `go test ./server -run "Store|LoadMessage|SendLock" -count=1`
Expected: FAIL

- [ ] **Step 3: Реализация store.go по источнику**

`loadMessage` после unmarshal вызывает `normalizeTargets`.

- [ ] **Step 4: Зелёные**

Run: `go test ./server -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add server/store.go server/store_test.go server/fakekv_test.go
git commit -m "feat: store scheduled messages in plugin KV"
```

### Task 5: Право писать и размер поста

**Files:**
- Create: `server/access.go`, `server/access_test.go`, `server/fakegate_test.go`

**Interfaces:**
- Consumes: `model.IsValidId`, `model.PermissionCreatePost`, `model.Channel.DeleteAt`, `model.User.DeleteAt`, `model.Config.ServiceSettings.MaxPostSize`
- Produces:
  - `var errInvalidChannel = errors.New("invalid channel id")`
  - `var errCannotPost = errors.New("cannot post in channel")`
  - `type postGate interface { GetChannel(channelID string) (*model.Channel, *model.AppError); HasPermissionToChannel(userID, channelID string, permission *model.Permission) bool; GetUser(userID string) (*model.User, *model.AppError); GetConfig() *model.Config }`
  - `var _ postGate = plugin.API(nil)`
  - `func checkCanPost(api postGate, userID, channelID string) error` — битый id → `errInvalidChannel`; ошибка GetChannel, `DeleteAt != 0` или нет permission → `errCannotPost`
  - `func userActive(api postGate, userID string) error` — `GetUser` со `StatusCode == 404` или `DeleteAt != 0` → ошибка `errUserGone`; прочая ошибка GetUser возвращается как есть; nil если пользователь жив
  - `var errUserGone = errors.New("user gone")`
  - `func maxPostSize(api postGate) int` — значение конфига, если > 0, иначе 16383
  - `const maxPendingPerUser = 100`, `maxChannelsPerMessage = 10`, `maxRotationMessages = 20`, `maxHTTPBodyBytes = 1 << 20`, `maxConfiguredAttempts = 10`, `minPollIntervalSeconds = 5`

- [ ] **Step 1: Тесты на fake postGate**

`TestCheckCanPost`: невалидный id (`"nope"`) → `errInvalidChannel` и `GetChannel` не вызван; `DeleteAt != 0` → `errCannotPost`; permission false → `errCannotPost`; permission true и `DeleteAt == 0` → nil.
`TestUserActive`: `DeleteAt != 0` → `errUserGone`; AppError 404 → `errUserGone`; AppError 500 → не `errUserGone` и не nil; живой пользователь → nil.
`TestMaxPostSize`: конфиг 100 → 100; `MaxPostSize == nil` или 0 → 16383.

- [ ] **Step 2: Падение**

Run: `go test ./server -run "CheckCanPost|UserActive|MaxPostSize" -count=1`
Expected: FAIL

- [ ] **Step 3: Реализовать access.go**

`HasPermissionToChannel` вызывается с `model.PermissionCreatePost`.

- [ ] **Step 4: Зелёные**

Run: `go test ./server -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add server/access.go server/access_test.go server/fakegate_test.go
git commit -m "feat: check create-post permission before scheduling"
```

### Task 6: Настройки

**Files:**
- Create: `server/configuration.go`, `server/configuration_test.go`
- Modify: `server/plugin.go` — поле `configuration atomic.Value`, методы get/set

**Interfaces:**
- Consumes: `minPollIntervalSeconds`, `maxConfiguredAttempts`
- Produces:
  - `type configuration struct { PollIntervalSeconds int; MaxAttempts int; DefaultTimezone string }`
  - `func (p *Plugin) getConfiguration() *configuration` — nil snapshot заменяется на 30 / 3 / `UTC`
  - `func (p *Plugin) OnConfigurationChange() error`

- [ ] **Step 1: Тест пола**

`TestPollIntervalFloor`: подменить API так, чтобы `LoadPluginConfiguration` записал `PollIntervalSeconds: 0`, `MaxAttempts: 0`, `DefaultTimezone: "Not/AZone"`. После `OnConfigurationChange` снимок равен 5, 1, `UTC`. Второй кейс: 4 секунды → 5; 11 attempts → 10; `Australia/Sydney` остаётся. `TestPollIntervalNegative`: `-1` → 5.

Fake должен реализовать `LoadPluginConfiguration`. Это метод `plugin.API`, не `postGate`. Тест вызывает `OnConfigurationChange` на `Plugin` с `plugintest` не нужен: вынести разбор в `func normalizeConfiguration(cfg *configuration) *configuration` и тестировать её. `OnConfigurationChange` только грузит и вызывает normalize. `LoadPluginConfiguration` в тесте не обязателен.

- [ ] **Step 2: Падение**

Run: `go test ./server -run TestPollInterval -count=1`
Expected: FAIL

- [ ] **Step 3: normalizeConfiguration**

Границы ровно те, что в тесте. Невалидный timezone — UTC. Пустой timezone — UTC. Лог warning при подмене timezone делает `OnConfigurationChange`, не normalize.

- [ ] **Step 4: Зелёные**

Run: `go test ./server -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add server/configuration.go server/configuration_test.go server/plugin.go
git commit -m "feat: clamp scheduler configuration"
```

### Task 7: HTTP API

**Files:**
- Create: `server/api.go`, `server/api_test.go`
- Modify: `server/plugin.go` — `ServeHTTP` вызывает `handleHTTP`

**Interfaces:**
- Consumes: `checkCanPost`, `maxPostSize`, `saveMessage`, `loadMessage`, `deleteMessage`, `listMessagesForUser`, `parseScheduleTime`, `validateSendAt`, `validateRecurrence`, `validateExtras`, `trimMessages`, `equalStrings`, `randomFireAt`, константы лимитов, `userIDHeader = "Mattermost-User-Id"`
- Produces:
  - `func (p *Plugin) handleHTTP(w http.ResponseWriter, r *http.Request)`
  - маршруты `GET /api/list`, `POST /api/create`, `PATCH /api/update`, `DELETE /api/cancel`
  - пустой заголовок → 401, тело `unauthorized`
  - `func normalizeChannelIDs(channelIDs []string, legacyChannelID string) []string` — поведение источника

- [ ] **Step 1: Тесты httptest**

Собрать `Plugin` с fake, который реализует и `kvAPI`, и `postGate`, и присвоить его в поле, с которого `handleHTTP` читает API. Чтобы не реализовывать весь `plugin.API`, `handleHTTP` и хелперы принимают уже прочитанный `userID`, а гейт/KV ходят через интерфейсы, лежащие на `Plugin`:

```go
type Plugin struct {
    plugin.MattermostPlugin
    configuration atomic.Value
    gate postGate
    kv kvAPI
}
```

`OnActivate` делает `p.gate, p.kv = p.API, p.API`. Тесты ставят fake в `p.gate` и `p.kv`, не в `p.API`.

Обязательные тесты:

- `TestCreateIgnoresBodyUserID`: заголовок `user-a`, тело `user_id: user-b`, канал разрешён. В KV `UserID == "user-a"`. `CreatePost` не вызывается на create.
- `TestChannelCountLimit`: 10 разных `model.NewId()`, permission true → 201; 11-й → 400 и KV пуст; один и тот же `model.NewId()` дважды в `channel_ids` → 201 и в записи один канал.
- `TestMessageSizeLimit`: `maxPostSize` fake возвращает 4. Тело `"abcd"` → 201. `"abcde"` → 400.
- `TestPendingQuota`: 100 записей этого пользователя → следующий create даёт 409, KV не растёт. Update существующего pending при 100 записях → 200.
- `TestCreateForbidden`: permission false → 403, тело ровно `forbidden`, KV пуст. Архивный канал — то же.
- `TestCreateBadID`: id `x` → 400.
- `TestBodyTooLarge`: тело `maxHTTPBodyBytes+1` → 400, `GetChannel` не вызван.
- `TestUpdateAndCancelScoped`: cancel/update чужого id (ключа нет при load от этого user) → 404. Свой pending update меняет текст и не сбрасывает `Occurrences`.
- `TestUnauthorizedWithoutUserHeader`: нет `Mattermost-User-Id` → 401, тело `unauthorized`, KV не читается.
- `TestRotationMessageCap`: `repeat=daily` и 20 непустых текстов короче `MaxPostSize` → 201; 21-й текст → 400 и KV пуст.

Валидный канал в фейке: 26-символьный id, который `model.IsValidId` принимает. Взять `model.NewId()` в тесте.

- [ ] **Step 2: Падение**

Run: `go test ./server -run "TestCreate|TestChannel|TestMessage|TestPending|TestBody|TestUpdate|TestUnauthorized|TestRotation" -count=1`
Expected: FAIL

- [ ] **Step 3: Хендлеры**

Порядок create: лимит тела (`http.MaxBytesReader` на `maxHTTPBodyBytes`) → JSON без `DisallowUnknownFields` → trim → normalize channels → `len(channels) > maxChannelsPerMessage` даёт 400 → `len(messages) > maxRotationMessages` даёт 400 → квота (`len(listMessagesForUser) >= maxPendingPerUser`) → `checkCanPost` на каждый → размер каждого текста в байтах (`len(string)`) против `maxPostSize` → `validateSendAt(sendAt, time.Now().UTC())` → recurrence/extras → save. Поле `user_id` в JSON не читать. Ответ 201 — сохранённый объект. Внутренняя ошибка KV → 500 с телом `save failed`, текст ошибки только в `p.API.LogError`. Если `p.API` в тесте nil, лог пропускать через проверку `p.API != nil`.

Update: те же проверки, `loadMessage` до записи, статус не pending → 409 `only pending messages can be edited`. Квоту не проверять.

Cancel: тело `{"id":"..."}`, пустой id → 400, нет записи → 404, успех → 204.

- [ ] **Step 4: Зелёные**

Run: `go test ./server -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add server/api.go server/api_test.go server/plugin.go
git commit -m "feat: add scheduled message HTTP API"
```

### Task 8: Slash `/schedule`

**Files:**
- Create: `server/command.go`, `server/command_test.go`
- Modify: `server/plugin.go` — `OnActivate` регистрирует команду, `ExecuteCommand` делегирует
- Источник синтаксиса: `server/command.go` @ `0b45fb0` (`parseCommandArgs` уже может жить в `timeutil.go` или `command.go`; перенести `parseCommandArgs` и `backtickURLs` вместе с их тестами из `utils_test.go`)

**Interfaces:**
- Consumes: `checkCanPost`, `saveMessage`, `listMessagesForUser`, `deleteMessage`, `loadMessage`, `parseScheduleTime`, `validateSendAt`, `validateRecurrence`, `getConfiguration`, `maxPendingPerUser`, `maxPostSize`
- Produces:
  - `func (p *Plugin) scheduleCommand() *model.Command` — trigger `schedule`, подкоманды list, cancel, help как в источнике
  - `func (p *Plugin) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError)`
  - ответы только `model.CommandResponseTypeEphemeral`
  - `func (p *Plugin) userOrDefaultTimezone(userID string) string` — профиль automatic, иначе manual, иначе `DefaultTimezone`, иначе `UTC`

- [ ] **Step 1: Тесты**

`TestSlashRejectsWhenCannotPost`: `CommandArgs.ChannelId` архивный или без permission → ephemeral с отказом, KV пуст.
`TestSlashQuota`: 100 записей → ephemeral про лимит, новая запись не появляется.
`TestSlashCreateUsesCommandChannel`: успешный create пишет ровно `[]string{args.ChannelId}` и `args.UserId`.
`TestSlashCancelOtherUser`: id, которого нет у этого user → ephemeral «not found», чужой ключ не удалять (его и нет в запросе load).
Перенести тесты `parseCommandArgs`, если они есть в `utils_test.go` источника; если источник их не содержит, один тест: `"\"hello world\" \"2026-06-01 09:30\" UTC"` → три токена `hello world`, `2026-06-01 09:30`, `UTC`.

- [ ] **Step 2: Падение**

Run: `go test ./server -run "TestSlash|TestParseCommandArgs" -count=1`
Expected: FAIL

- [ ] **Step 3: Перенести разбор команды и вставить проверки**

Перед `saveMessage` в create: `checkCanPost(p.gate, args.UserId, args.ChannelId)`, квота, `len(message) <= maxPostSize`. Ошибки гейта — ephemeral `You cannot post in this channel.` Квота — `You have reached the limit of 100 scheduled messages.` Внутренние ошибки KV не попадают в ephemeral (`Failed to save scheduled message.`). Синтаксис help/list/cancel и флаги `repeat`/`count`/`until` — как в источнике. Окно, несколько каналов и ротация slash не принимает.

`OnActivate`: `p.gate, p.kv = p.API, p.API`, затем `RegisterCommand`, затем старт планировщика из Task 9. Пока планировщика нет — только команда и присваивание интерфейсов. `ExecuteCommand` не возвращает AppError на пользовательский ввод; AppError только если регистрация невозможна. Ошибка Handle для плохого ввода — ephemeral, `AppError == nil`.

- [ ] **Step 4: Зелёные**

Run: `go test ./server -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add server/command.go server/command_test.go server/plugin.go
git commit -m "feat: add /schedule slash command"
```

### Task 9: Планировщик

**Files:**
- Create: `server/scheduler.go`, `server/scheduler_test.go`
- Modify: `server/plugin.go` — горутина и останов, как в источнике (`stopOnce`, `stopCh`, `doneCh`)
- Источник: `scheduler.go` и `scheduler_test.go` @ `0b45fb0`

**Interfaces:**
- Consumes: `listAllPendingMessages`, `acquireSendLock`, `loadMessage`, `saveMessage`, `deleteMessage`, `pickMessageBody`, `nextOccurrence`, `seriesEnded`, `randomFireAt`, `checkCanPost`, `userActive`, `getConfiguration`
- Produces:
  - `func (p *Plugin) runScheduler()`
  - `func (p *Plugin) sendDueMessages()`
  - `func (p *Plugin) dispatch(msg *ScheduledMessage, maxAttempts int)`
  - пост через `poster`:

```go
type poster interface {
    CreatePost(post *model.Post) (*model.Post, *model.AppError)
}
```

`Plugin` хранит `post poster`. `OnActivate` ставит `p.post = p.API`. В тесте fake считает вызовы и запоминает `*model.Post`.

- [ ] **Step 1: Тесты dispatch напрямую, без тикера**

`TestDispatchDeletesGoneUser`: `userActive` → `errUserGone`. `CreatePost` не вызван. Ключ KV удалён.
`TestDispatchSkipsChannelWithoutPermission`: два канала, право только на второй. Один `CreatePost`, у него `UserId` автора, `ChannelId` второго, `Message` текста, `Props` пустой. Запись одноразового сообщения удалена (частичный успех не ретраится).
`TestDispatchRetriesWhenAllTargetsFail`: оба канала без права, `Attempts` становится 1, статус остаётся pending, `CreatePost` не вызван.
`TestDispatchDoesNotCopyProps`: даже если у записи в тесте каким-то образом заполнено лишнее, созданный `Post` имеет нулевые `Props` и пустые `Type`, `FileIds`, `PendingPostId`.
Перенести из источника тесты lock (второй acquire не шлёт) и сдвига серии, адаптировав их к fake.

- [ ] **Step 2: Падение**

Run: `go test ./server -run TestDispatch -count=1`
Expected: FAIL

- [ ] **Step 3: dispatch**

Порядок: lock → reload pending → `userActive`; `errUserGone` удаляет запись и выходит; другая ошибка `userActive` идёт в ветку полного провала. Дальше цели `Targets()`. На каждую: `checkCanPost`; ошибка — `postFailure` без `CreatePost`. Успех — `CreatePost` с тремя полями. Дальше политика источника: все цели упали → Attempts/MaxAttempts/skip серии; хотя бы один успех → не ретраить; одноразовый успех → delete; серия → `advanceRecurring`.

`runScheduler` читает интервал из `getConfiguration` и при смене вызывает `ticker.Reset`. Первый `sendDueMessages` до цикла. `sendDueMessages` до dispatch удаляет запись со статусом `completed`, если `effectiveFireAt` старше 7 суток (`completedRetentionMs`), как в источнике.

- [ ] **Step 4: Зелёные**

Run: `go test ./server -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add server/scheduler.go server/scheduler_test.go server/plugin.go
git commit -m "feat: send due messages with a cluster lock"
```

### Task 10: Webapp на React 19

**Files:**
- Create: `webapp/babel.config.js`, `webapp/webpack.config.js`, `webapp/tsconfig.json`, `webapp/package.json`, `webapp/src/jsx_runtime.ts`, порт `webapp/src/index.tsx`, `client.ts`, `events.ts`, `redux.ts`, `types.ts`, `components/*` из bed42 @ `0b45fb0`
- Не копировать: `webapp/src` starter, кроме `jsx_runtime.ts`

**Interfaces:**
- Consumes: HTTP Task 7, id `com.attoktist.scheduled-posts`
- Produces: `webapp/dist/main.js`, `window.registerPlugin('com.attoktist.scheduled-posts', ...)`

- [ ] **Step 1: Toolchain**

`jsx_runtime.ts` скопировать байт-в-байт из `mattermost-plugin-starter-template` `webapp/src/jsx_runtime.ts` (коммит `2b7f511`). `babel.config.js`: `@babel/preset-react` с `runtime: 'automatic'`. `webpack.config.js`: alias `react/jsx-runtime` и `react/jsx-dev-runtime` на этот файл; externals `react`, `react-dom`, `redux`, `react-redux`; entry `./src/index.tsx`; output `dist/main.js`. В `package.json` не ставить `react` в `dependencies`. Версии babel, webpack, typescript, `@types/react` взять из starter `package.json` того же коммита. `npm install` в `webapp/`.

- [ ] **Step 2: Порт UI**

Скопировать компоненты источника. В `types.ts` `PLUGIN_ID = 'com.attoktist.scheduled-posts'`. Поведение модалки, списка, бейджа и часов не менять. `client.ts` ходит на те же четыре пути. Если `tsc` падает из-за сигнатуры `registerChannelHeaderButtonAction` на типах хоста, поправить вызов под сигнатуру и кнопку оставить.

- [ ] **Step 3: Сборка**

```powershell
cd webapp
npx tsc --noEmit
npm run build
```

Expected: `webapp/dist/main.js` существует. В файле нет вшитого `react.production`. Поиск по бандлу: нет строки `REACT_CURRENT_OWNER` как собственного рантайма; JSX идёт через shim. Достаточная проверка: `tsc` и webpack exit 0, и в исходниках нет `runtime: 'classic'`.

- [ ] **Step 4: Commit**

```powershell
git add webapp/package.json webapp/package-lock.json webapp/babel.config.js webapp/webpack.config.js webapp/tsconfig.json webapp/src
git commit -m "feat: port scheduler webapp to React 19 JSX runtime"
```

### Task 11: Сборка бандла и README

**Files:**
- Create: `Makefile`
- Modify: `README.md`
- Modify: этот план не трогать, кроме уже сделанной работы

**Interfaces:**
- Consumes: все предыдущие задачи
- Produces: `dist/com.attoktist.scheduled-posts-1.0.0.tar.gz` со структурой `com.attoktist.scheduled-posts/{plugin.json,server/dist/plugin-linux-amd64,webapp/dist/main.js}` минимум; манифест перечисляет все пять ОС, и команды README их собирают

- [ ] **Step 1: README на русском**

Разделы: зачем (Team Edition, штатные scheduled posts под лицензией), сборка PowerShell (`go build` с `GOOS`/`GOARCH` для пяти целей, `npm run build`), установка tar.gz через System Console, slash-примеры из `usageText` источника, ограничение «второй `/schedule` не зарегистрируется», атрибуция MIT Andrew Bednarz и производная Eugene Khramchenkov, команда `govulncheck ./...` из корня модуля. Написать, куда складывается отчёт, если `govulncheck` найдёт находку только внутри v0.4.3 и она не достижима: строка в README с id находки. Если достижима — чинить до зелёного `govulncheck`, README про «известную находку» не писать.

- [ ] **Step 2: Makefile**

Цели `server`, `webapp`, `dist`, `test`. Рецепты на sh. `test` = `go test ./server -count=1`. README говорит, что без `make` достаточно команд PowerShell из того же README.

- [ ] **Step 3: Прогон**

```powershell
go test ./server -count=1
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./server
cd webapp; npx tsc --noEmit; npm run build
```

Expected: тесты PASS, govulncheck без достижимых уязвимостей, webpack exit 0.

Собрать linux-amd64 бинарь и убедиться, что `go version -m server/dist/plugin-linux-amd64` не содержит `mattermost-server/v6`.

- [ ] **Step 4: Commit**

```powershell
git add README.md Makefile
git commit -m "docs: describe build and install for Mattermost 12.0.1"
```

## Self-review

Покрытие спеки: идентичность и v0.4.3 — Task 1; время, 30с, повторы, DST — Task 2; окно и ротация — Task 3; KV, lock, изоляция user id — Task 4; permission, архив, MaxPostSize, константы лимитов — Task 5; пол настроек — Task 6; HTTP, квота, 1 MiB, 10 каналов, чужой user_id, 403 без id — Task 7; slash — Task 8; send, удалённый автор, частичный фейл, пустые Props — Task 9; React 19 — Task 10; README, бандл, govulncheck — Task 11. Миграция KV и вложения в плане отсутствуют, это не-цели.

Сигнатуры `checkCanPost`, `userActive`, `maxPostSize`, `kvAPI`, `postGate`, `poster` совпадают между задачами. `Plugin.gate` / `kv` / `post` задаются в Task 7 и используются в 8 и 9.
