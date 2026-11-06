version ?= patch

.PHONY: dev web desktop server test package auto-format branding release
web: branding
	# Force dependency lifecycle scripts to use Bun as well.
	bun run --bun --cwd web bun install --frozen-lockfile
	bun run --bun --cwd web build

dev: web
	go run github.com/egoist/mygo/cmd/mygo@v0.6.4 dev

branding:
	go run ./tools/branding.go

desktop: web
	go build -ldflags "-X github.com/egoist/mygo.production=1" -o build/spool-desktop ./cmd/desktop

server: web
	go build -ldflags "-X main.production=1" -o build/spool-server ./cmd/server

test:
	bun test tools/*.test.mjs
	bun run --bun --cwd web test
	go test -race ./...
	go vet ./...

auto-format:
	gofmt -w resources.go cmd internal tools
	bun run --bun --cwd web format

package: web
	go run github.com/egoist/mygo/cmd/mygo@v0.6.4 build -skip-dmg

release:
	bun tools/release.mjs "$(version)"
