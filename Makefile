version ?= patch
mode ?= debug
GO_PACKAGES ?= ./...
GO_TEST_FLAGS ?=
TOOLS_TESTS ?= tools/*.test.mjs
WEB_TESTS ?= ./test

.PHONY: dev web desktop server test test-tools test-web test-go test-race vet-go package auto-format branding build apk release
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

# Full ordinary checks are explicit; use the scoped targets during development.
test: test-tools test-web test-go vet-go

test-tools:
	sh tools/run.sh tools-test bun test $(TOOLS_TESTS)

# WEB_TESTS paths are relative to web/. Always refresh compiled test fixtures.
test-web:
	sh tools/run.sh web-test sh -eu -c 'cd web; bun run --bun test:prepare; bun test --isolate "$$@"' web-tests $(WEB_TESTS)

test-go:
	sh tools/run.sh go-test go test $(GO_TEST_FLAGS) $(GO_PACKAGES)

test-race:
	sh tools/run.sh go-race go test -race $(GO_TEST_FLAGS) $(GO_PACKAGES)

vet-go:
	sh tools/run.sh go-vet go vet $(GO_PACKAGES)

auto-format:
	gofmt -w resources.go cmd internal tools
	bun run --bun --cwd web format

package: web
	go run github.com/egoist/mygo/cmd/mygo@v0.6.4 build -skip-dmg

build: apk

apk:
	sh android/tools/build-apk.sh "$(if $(filter release,$(MAKECMDGOALS)),release,$(mode))"

ifeq ($(filter build apk,$(MAKECMDGOALS)),)
release:
	bun tools/release.mjs "$(version)"
else
# In `make build apk release`, release selects the APK build type.
release: ;
endif
