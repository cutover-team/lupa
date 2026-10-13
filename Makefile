RRWEB_VER=1.1.3
PLAYER_VER=0.7.14
VENDOR=internal/web/static/vendor

.PHONY: vendor build test run
vendor:
	curl -fsSL https://cdn.jsdelivr.net/npm/rrweb@$(RRWEB_VER)/dist/rrweb.min.js -o $(VENDOR)/rrweb.min.js
	curl -fsSL https://cdn.jsdelivr.net/npm/rrweb-player@$(PLAYER_VER)/dist/index.js -o $(VENDOR)/rrweb-player.js
	curl -fsSL https://cdn.jsdelivr.net/npm/rrweb-player@$(PLAYER_VER)/dist/style.css -o $(VENDOR)/rrweb-player.css
	@ls -la $(VENDOR)
build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o lupa ./cmd/lupa
test:
	go test ./...
run:
	go run ./cmd/lupa
