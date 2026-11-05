# Отложенные сообщения для Mattermost Team Edition 12.0.1

## Зачем

В Team Edition штатные scheduled posts закрыты лицензией Professional/Enterprise. Нужен свой плагин: пользователь пишет сообщение сейчас, оно уходит от его имени в выбранное время.

Источник поведения — [bed42/mattermost-scheduled-posts-plugin](https://github.com/bed42/mattermost-scheduled-posts-plugin) v1.2.0 (`0b45fb0`). Его сервер собран на `github.com/mattermost/mattermost-server/v6` v6.7.2 и Go 1.21, webapp — на React 17 с classic JSX. На Mattermost 12.0.1 этот бандл не поднимается: другой plugin RPC и React 19. Транзитивное дерево v6 тянет `golang.org/x/crypto` и `golang.org/x/net` от марта 2022 и `google.golang.org/grpc` v1.45. Точечный bump этого модуля задачу не решает.

Текущий репозиторий `github.com/attoktist/mattermost-delayed-message` пустой (README и MIT). Реализация идёт в ветку `feat/scheduled-posts-mm12` от `main`.

## Цели

- То же пользовательское поведение, что у v1.2.0: slash `/schedule`, модалка, список, несколько каналов, повтор, случайное окно, ротация текстов.
- Плагин включается на Mattermost Team Edition 12.0.1 без лицензии.
- Сервер линкуется с актуальным `server/public`, а не с модулем v6.
- Webapp использует React хоста (React 19) и automatic JSX runtime.
- Проверки права писать и лимиты стоят на сервере. Клиент им не доверяет.
- `govulncheck` по серверному модулю не находит достижимых уязвимостей в нашем коде и в зависимостях, которые мы сами выбрали. Уязвимости, которые живут только внутри зафиксированного `server/public` и не достижимы из нашего кода, фиксируются в README, а не замалчиваются.

## Не-цели

- Вложения, черновики, публикация от чужого имени, подмена username/icon.
- Стыковка со штатным API scheduled posts и с его лицензией.
- Перенос KV с `com.bednarz.scheduler`. Идентификатор плагина другой, данные не общие.
- Нативная модалка на iOS/Android. Slash работает на всех клиентах, UI — web и desktop, как в источнике.
- Смена семантики повторов, DST, ротации и частичного фейла по каналам.
- Замена опроса на серверный job scheduler.
- i18n. Строки UI и slash остаются английскими, как в источнике.
- Скриншоты из README источника.

## Ограничения

- Хост: Mattermost 12.0.1, `min_server_version` = `12.0.0`. Ниже плагин не обещаем.
- Сборка проверяется на Windows. README даёт команды PowerShell (`go build`, `npm run build`). Makefile — дополнительный POSIX-вход с тем же результатом, не единственный путь.
- Лицензия источника MIT, copyright Andrew Bednarz (2026). Текст лицензии и это уведомление сохраняются. Второй строкой copyright — Eugene Khramchenkov (2026) за производную работу.
- Демо-код starter template (webhook, channel status, testcontainers, Gitpod, AGENTS.md, CLAUDE.md, workflows) не переносится.

## Подходы

1. Поднять `go.mod` на месте, оставив импорт `mattermost-server/v6` и React 17. Отброшен: handshake плагина и JSX runtime на 12.0.1 другие, старое дерево crypto/grpc остаётся.
2. То же дерево файлов bed42, заменить импорты и webpack на месте. Отклонён: каркас сборки у источника самодельный и не содержит shim `react/jsx-runtime`, который требует хост 12.
3. Выбранный: каркас сборки с официального [mattermost-plugin-starter-template](https://github.com/mattermost/mattermost-plugin-starter-template) (webpack externals и `jsx_runtime.ts`; Go 1.26.10), внутрь переносится домен расписания и UI источника. Версия `server/public` — из `server/go.mod` тега `v12.0.1`: `github.com/mattermost/mattermost/server/public v0.4.3`. Стартер на момент дизайна требовал v0.6.0; побеждает тег сервера. Go — 1.26.10.

## Идентичность плагина

Решение автора дизайна, не исходного репозитория:

| Поле | Значение |
|---|---|
| id | `com.attoktist.scheduled-posts` |
| name | Delayed Messages |
| module | `github.com/attoktist/mattermost-delayed-message` |
| version | `1.0.0` |
| trigger | `schedule` |
| ветка | `feat/scheduled-posts-mm12` |

Триггер `schedule` оставлен. Если на сервере уже зарегистрирован другой `/schedule`, `OnActivate` упадёт на `RegisterCommand` — это ожидаемо, в README одной строкой.

## Поведение, которое сохраняется

Доменные правила источника не переписываются, их тесты переносятся.

- Время: ISO-8601 или `YYYY-MM-DD HH:MM` в IANA timezone. Пустой timezone — timezone профиля (automatic, иначе manual), иначе `DefaultTimezone`, иначе UTC. Не раньше чем через 30 секунд.
- Повтор: `daily`, `weekdays`, `weekly`, `fortnightly`, `monthly`, `yearly`. Конец: never / on (дата включительно до 23:59 в timezone серии) / after N. Арифметика стенных часов, чтобы DST не сдвигал «9:00». Monthly/yearly: переполнение дня зажимается в последний день целевого месяца (29 февраля → 28).
- Окно `window_ms`: 0…24ч. Фактический fire — случайная точка в `[SendAt, SendAt+window)`. Для серии якорь остаётся `SendAt`.
- Ротация: только у серии, минимум 2 непустых текста. Цикл — перестановка без немедленного повтора последнего отправленного. Смена списка при редактировании сбрасывает цикл.
- Slash создаёт одно сообщение в текущий канал. Флаги: `repeat`, `count`, `until` (взаимоисключающие count и until). `list` и `cancel <id>`. Окно, несколько каналов и ротация — только модалка.
- Список slash — ephemeral-таблица, URL в тексте обёрнуты в backticks.
- HTTP, все под роутером плагина, пользователь только из заголовка `Mattermost-User-Id`, который выставляет сервер:
  - `GET /api/list`
  - `POST /api/create`
  - `PATCH /api/update`
  - `DELETE /api/cancel` с телом `{"id":"..."}`
- Чужой id для update/cancel/load — как отсутствие записи. Ключ KV включает user id.
- Редактировать можно только `pending`. `CreatedAt`, `Status`, `Attempts`, `LastError`, `Occurrences` при edit не сбрасываются. Сброс ротации — только если список текстов изменился.
- Отправка: `CreatePost` с `ChannelId`, `UserId` автора расписания и `Message`. Никаких `props` из клиента. `from_plugin` ставит сервер сам.
- Успех одноразового — удалить запись. Если удаление после успешного `CreatePost` не удалось, статус становится `sent`, `FireAt` обнуляется, в `LastError` короткая пометка, что доставка прошла, а удаление нет, и запись сохраняется один раз. `sent` не отправляется. Если и это сохранение не удалось — только лог. Успех серии — сдвинуть occurrence в памяти и сохранить. Если сохранение сдвинутой или завершённой серии не удалось, та же структура пишется ещё один раз; прежний `SendAt` не восстанавливается. Повторный провал — только лог. Все цели упали — попытка и retry до `MaxAttempts`, потом `failed`; у серии вместо убийства серии occurrence пропускается. Часть целей успела — не повторять (иначе дубли), ошибки только в лог. Completed серия живёт 7 суток, затем GC.
- Кластер: `KVSetWithOptions` atomic, `OldValue` nil, TTL 60с на ключ `lock_<id>`. После захвата запись перечитывается. Pending, у которого `effectiveFireAt` ещё в будущем, не отправляется: серия после успешной отправки остаётся pending, но уже на следующем occurrence.
- Опрос: горутина с тикером, интервал из конфига, нижняя граница 5 секунд. Первый проход сразу после старта. `OnDeactivate` останавливает цикл и ждёт выхода.
- Настройки system console, как в источнике: `PollIntervalSeconds` (default 30), `MaxAttempts` (default 3), `DefaultTimezone` (default UTC). Невалидный timezone в настройке — UTC и warning в лог.
- UI: кнопка-часы в шапке канала открывает модалку; пункт меню шапки и бейдж в левой панели открывают список. Счётчик бейджа обновляется при create/cancel и по таймеру 30с, как в источнике.

## Что меняется относительно источника

Это тоже решения автора дизайна.

### Право писать

`GetChannelMember == nil` недостаточно. На create, update и slash, для каждого канала:

1. `model.IsValidId(channelID)`.
2. Канал существует и `DeleteAt == 0`.
3. `HasPermissionToChannel(userID, channelID, PermissionCreatePost)` истинно.

Та же тройка непосредственно перед каждым `CreatePost`. Потеря права на одну цель — ошибка этой цели, политика retry источника не меняется.

Перед отправкой автор перечитывается. `DeleteAt != 0` или пользователь не найден — запись удаляется, пост не создаётся.

Деактивированный пользователь (`DeleteAt > 0` в модели Mattermost; отдельного флага в этой проверке нет) обрабатывается так же.

### Лимиты

Константы, не новые настройки:

| Константа | Значение |
|---|---|
| записей KV на пользователя (pending, failed, completed) | 100 |
| каналов в одном расписании | 10 |
| текстов в ротации | 20 |
| тело HTTP | 1 MiB (`MaxBytesReader`) |
| длина одного текста | `ServiceSettings.MaxPostSize` из конфига сервера; если 0 — 16383 байт |

Превышение квоты записей — HTTP 409, slash отвечает ephemeral с той же причиной. Остальные нарушения входа — HTTP 400. Тело ответа клиенту не содержит `err.Error()` внутренних ошибок KV/CreatePost; подробности только в лог плагина. Ошибки разбора времени и валидации полей по-прежнему текстом объясняют, что не так с вводом.

`json.Decoder` без `DisallowUnknownFields`. Неизвестные поля игнорируются.

`MaxAttempts` из настроек: значения `< 1` становятся 1, значения `> 10` становятся 10.

Отдельного rate limiter нет. Квоты достаточно.

### Зависимости

- Прямые require сервера: `server/public` (версия с тега v12.0.1), `github.com/pkg/errors`, `github.com/stretchr/testify`.
- Не тащить testcontainers, docker, gorilla/mux, если домен их не использует.
- `go mod tidy` после этого. Старый `go.sum` источника не копировать.
- Webapp: `package.json` и lock со starter только в части toolchain (webpack, babel, typescript, типы React хоста). Компоненты источника перевести на automatic JSX (`runtime: 'automatic'`). В webpack оставить alias на `src/jsx_runtime.ts` для `react/jsx-runtime` и `react/jsx-dev-runtime`, externals `react`, `react-dom`, `redux`, `react-redux`. React в бандл не попадает.
- `PLUGIN_ID` в webapp равен id манифеста.

### Сборка

Манифест перечисляет те же пять бинарников, что starter: linux amd64/arm64, darwin amd64/arm64, windows amd64. Каталог бандла: `dist/<plugin-id>/` с `plugin.json`, `server/dist`, `webapp/dist`, архив `com.attoktist.scheduled-posts-1.0.0.tar.gz`.

## Тесты

Перенести и заставить компилироваться против нового импорта:

- повторы и DST (`recurrence_test.go`);
- окно и ротация (`extras_test.go`);
- планировщик, включая lock и частичный фейл (`scheduler_test.go`);
- KV (`store_test.go`);
- разбор времени и slash-аргументов (`utils_test.go`).

Добавить проверки, которых в источнике нет:

- нет `PermissionCreatePost` — create/update/slash не пишут KV;
- архивный канал — отказ;
- 101-я запись — отказ, 100-я проходит;
- тело больше 1 MiB — отказ до разбора в домен;
- на send пользователь удалён — запись удалена, `CreatePost` не вызван;
- на send нет права на один из двух каналов — пост только во второй, дубля нет.

Живой Mattermost для этих тестов не нужен. Способ подмены `plugin.API` (узкий fake на используемые методы или mock) выбирает тот, кто пишет код. Генерировать мок всего `plugin.API` не требуется.

Прогон приёмки в репозитории:

- `go test ./...` в модуле сервера;
- `govulncheck ./...` по тому же модулю;
- production-сборка webapp и `tsc --noEmit`.

Установка tar.gz на живой 12.0.1 в эту поставку не входит: стенда в репозитории нет.

## Что остаётся на усмотрение реализации

- Раскладка пакетов внутри `server/` (один `package main`, как в источнике, или пакеты starter-стиля). Публичное поведение и пути HTTP от этого не зависят.
- Сигнатуры `plugin.API` в выбранном `server/public` (`*model.AppError` или `error`). Вызывать то, что есть в этой версии; семантику проверок выше не ослаблять.
- Если `registerChannelHeaderButtonAction` на 12.0.1 сменил сигнатуру — подстроить вызов, кнопку не выкидывать.
- Точный набор devDependencies webapp, лишь бы выполнялись условия по JSX и externals.
- Нужен ли `.golangci.yml`. Если копируется со starter и мешает сборке домена — ослабить под наш код, а не тащить демо-пакеты ради линтера.

## Критерий готовности

Ветка `feat/scheduled-posts-mm12` собирает сервер и webapp командами из README, тесты выше зелёные, `govulncheck` соответствует разделу «Цели», манифест требует сервер ≥ 12.0.0, в дереве нет импорта `mattermost-server/v6` и нет classic JSX runtime.
