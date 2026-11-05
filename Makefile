# Optional POSIX duplicate of the PowerShell commands in README.md.
.PHONY: server webapp dist test

PLUGIN_ID = com.attoktist.scheduled-posts
VERSION = 1.0.0
STAGE = dist/$(PLUGIN_ID)
BUNDLE = dist/$(PLUGIN_ID)-$(VERSION).tar.gz

server:
	mkdir -p server/dist
	GOTOOLCHAIN=go1.26.10 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server/dist/plugin-linux-amd64 ./server
	GOTOOLCHAIN=go1.26.10 CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o server/dist/plugin-linux-arm64 ./server
	GOTOOLCHAIN=go1.26.10 CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o server/dist/plugin-darwin-amd64 ./server
	GOTOOLCHAIN=go1.26.10 CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o server/dist/plugin-darwin-arm64 ./server
	GOTOOLCHAIN=go1.26.10 CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o server/dist/plugin-windows-amd64.exe ./server

webapp:
	cd webapp && npm run build

dist: server webapp
	rm -rf $(STAGE)
	mkdir -p $(STAGE)/server/dist $(STAGE)/webapp/dist
	cp plugin.json $(STAGE)/plugin.json
	cp server/dist/plugin-linux-amd64 $(STAGE)/server/dist/plugin-linux-amd64
	cp server/dist/plugin-linux-arm64 $(STAGE)/server/dist/plugin-linux-arm64
	cp server/dist/plugin-darwin-amd64 $(STAGE)/server/dist/plugin-darwin-amd64
	cp server/dist/plugin-darwin-arm64 $(STAGE)/server/dist/plugin-darwin-arm64
	cp server/dist/plugin-windows-amd64.exe $(STAGE)/server/dist/plugin-windows-amd64.exe
	cp webapp/dist/main.js $(STAGE)/webapp/dist/main.js
	tar -czf $(BUNDLE) -C dist $(PLUGIN_ID)

test:
	GOTOOLCHAIN=go1.26.10 go test ./server -count=1
