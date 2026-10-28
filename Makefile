version ?= patch

.PHONY: web desktop server test package auto-format branding release
web: branding
	npm --prefix web ci
	npm --prefix web run build

branding:
	go run ./tools/branding.go

desktop: web
	go build -ldflags "-X github.com/egoist/mygo.production=1" -o build/spool-desktop ./cmd/desktop

server: web
	go build -ldflags "-X main.production=1" -o build/spool-server ./cmd/server

test:
	node --test tools/*.test.mjs
	npm --prefix web test
	go test -race ./...
	go vet ./...

auto-format:
	gofmt -w resources.go cmd internal tools
	npm exec --prefix web -- prettier --write web/src

package: web
	go run github.com/egoist/mygo/cmd/mygo@v0.6.4 build -skip-dmg

release:
	node tools/release.mjs "$(version)"
