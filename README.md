# Отложенные сообщения для Mattermost Team Edition

Плагин `com.attoktist.scheduled-posts` версии 1.0.0 принимает текст сейчас и публикует его от имени автора в заданное время. Он работает на Mattermost Team Edition 12.0.1 без лицензии.

Штатные scheduled posts Mattermost требуют лицензию Professional или Enterprise. Этот плагин даёт отложенную отправку на Team Edition без такой лицензии.

Минимальная версия сервера — 12.0.0 (`min_server_version` в `plugin.json`).

## Сборка

Команд PowerShell ниже достаточно. Makefile необязателен: это тот же результат для POSIX-оболочки, не отдельный способ сборки.

Нужны Go 1.26.10 и Node.js. Все команды Go запускаются из корня модуля, где лежит `go.mod`.

```powershell
$env:GOTOOLCHAIN = "go1.26.10"
$env:CGO_ENABLED = "0"
New-Item -ItemType Directory -Force -Path server/dist | Out-Null

$env:GOOS = "linux";   $env:GOARCH = "amd64"; go build -o server/dist/plugin-linux-amd64 ./server
$env:GOOS = "linux";   $env:GOARCH = "arm64"; go build -o server/dist/plugin-linux-arm64 ./server
$env:GOOS = "darwin";  $env:GOARCH = "amd64"; go build -o server/dist/plugin-darwin-amd64 ./server
$env:GOOS = "darwin";  $env:GOARCH = "arm64"; go build -o server/dist/plugin-darwin-arm64 ./server
$env:GOOS = "windows"; $env:GOARCH = "amd64"; go build -o server/dist/plugin-windows-amd64.exe ./server
Remove-Item Env:GOOS, Env:GOARCH
```

`CGO_ENABLED=0` нужен, чтобы кросс-сборка с Windows не искала C-компилятор целевой ОС. Пути совпадают с `server.executables` в `plugin.json`.

Webapp, из корня репозитория:

```powershell
Set-Location webapp
npm ci
npx tsc --noEmit
npm run build
Set-Location ..
```

`npm run build` пишет `webapp/dist/main.js`. React в этот файл не попадает: его даёт сервер Mattermost.

Архив для загрузки: `dist/com.attoktist.scheduled-posts-1.0.0.tar.gz`. Внутри один каталог `com.attoktist.scheduled-posts/` с `plugin.json`, `server/dist/` (все пять бинарников) и `webapp/dist/main.js`.

```powershell
$pluginId = "com.attoktist.scheduled-posts"
$stage = "dist/$pluginId"
Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path "$stage/server/dist", "$stage/webapp/dist" | Out-Null
Copy-Item plugin.json "$stage/plugin.json"
Copy-Item server/dist/plugin-linux-amd64, server/dist/plugin-linux-arm64, server/dist/plugin-darwin-amd64, server/dist/plugin-darwin-arm64, server/dist/plugin-windows-amd64.exe "$stage/server/dist"
Copy-Item webapp/dist/main.js "$stage/webapp/dist/main.js"
if (Test-Path "dist/$pluginId-1.0.0.tar.gz") { Remove-Item "dist/$pluginId-1.0.0.tar.gz" }
tar -czf "dist/$pluginId-1.0.0.tar.gz" -C dist $pluginId
```

`dist/`, `server/dist/` и `webapp/dist/` перечислены в `.gitignore`. В git попадает способ сборки, не готовые бинарники.

## Установка

System Console → Plugins → Plugin Management → Upload Plugin. Выберите `dist/com.attoktist.scheduled-posts-1.0.0.tar.gz`, затем включите плагин.

## Slash-команды

Триггер — `/schedule`. Текст справки:

```
/schedule "message" "YYYY-MM-DD HH:MM" [timezone] [repeat=...] [count=N|until=YYYY-MM-DD]
/schedule list
/schedule cancel <id>
```

Повторы: `daily`, `weekdays`, `weekly`, `fortnightly`, `monthly`, `yearly`.

```
/schedule "stand-up" "2026-04-30 09:00" Australia/Sydney repeat=weekdays count=20
/schedule "monthly invoice" "2026-05-01 10:00" UTC repeat=monthly until=2026-12-31
```

В шапке канала есть кнопка с часами: она открывает тот же сценарий в календаре.

Второй `/schedule` не зарегистрируется. Если этот триггер уже занят другим плагином, `OnActivate` вернёт ошибку `RegisterCommand`, и плагин не включится.

## govulncheck

Из корня модуля:

```powershell
$env:GOTOOLCHAIN = "go1.26.10"
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...
```

Единственная зависимость Mattermost — `github.com/mattermost/mattermost/server/public` v0.4.3. Импорта `github.com/mattermost/mattermost-server/v6` нет.

## Лицензия

MIT. Copyright (c) 2026 Andrew Bednarz. Производная работа: Copyright (c) 2026 Eugene Khramchenkov. Полный текст — в `LICENSE`.

Поведение команд и планировщика перенесено из [bed42/mattermost-scheduled-posts-plugin](https://github.com/bed42/mattermost-scheduled-posts-plugin) v1.2.0 (`0b45fb0`), copyright Andrew Bednarz (2026).
