# Repository Instructions

## Documentation Scope

Keep `README.md` and `README.zh-CN.md` concise and aligned: project purpose,
features, prerequisites, simple development commands, and links to detailed
guides. Put deployment and operational details in `docs/` or the relevant
component guide. Keep agent test selection and cache policies in this file.
Do not append task-specific implementation notes or verification logs to READMEs.

Use Bun for repository-owned JavaScript tooling and tests. Do not introduce a
Python runtime dependency for development, test, build, or release scripts.

## Development And Test Storage

Keep reusable caches separate from disposable test data. Use each tool's standard
cache location where available. Temporary files, cloned test fixtures, binaries,
and browser profiles must belong to an owned per-run directory under a `spool/`
namespace in the system/user temporary directory.
Repository-relative paths below are already covered by `.gitignore`.

| Purpose | Required location | Lifetime |
| --- | --- | --- |
| Go build/test cache (`GOCACHE`) | Tool default from `go env GOCACHE`; preserve an explicit caller override | Share across projects, worktrees, tasks, and runs |
| Go module cache (`GOMODCACHE`) | Existing `go env GOMODCACHE` location | Shared; do not duplicate |
| Gradle user cache (`GRADLE_USER_HOME`) | Tool default (`~/.gradle/`); preserve an explicit caller override | Share across projects and worktrees |
| Gradle project cache | `build/cache/gradle-project/android/` | Reuse within this checkout; do not share across source trees |
| Per-run scratch space | `<system-temp>/spool-<user>/spool/<task>.XXXXXX/` | Remove when the owning run exits |
| Go compiler/linker scratch (`GOTMPDIR`) | `<run>/go/` | Same as the owning run |
| Other tool/test temporary files (`TMPDIR`, `TMP`, `TEMP`) | `<run>/tmp/` | Same as the owning run |
| Disposable app data and browser profiles | Subdirectories of `<run>/` | Same as the owning run |
| Logs, screenshots, coverage, and diagnostic reports to retain | `build/artifacts/<task>/` | Keep only the latest useful set |
| Normal build/package output | Existing `build/` layout | Reuse existing output paths |

Existing tool-managed `web/node_modules/`, `web/.test-build/`, `frontend/`,
`.mygo/`, and `smoke-results/` remain valid. Do not copy their outputs elsewhere
just to rerun a check. Keep reusable test helpers in the source tree rather than
leaving one-off scripts in a system temporary directory.

The system temporary directory is suitable for disposable runs, not reusable
caches. The private `spool-<user>/` parent keeps users independent when the OS
temporary directory is shared. Its `spool/` namespace may contain uniquely named
runs; never share one mutable run directory between tasks. Capture the original
temporary base before overriding `TMPDIR`. OS cleanup is best effort and has no
guaranteed deadline, so every run must still clean up its own files. Do not create new
scratch data under `build/tmp/`; existing historical files need separate,
explicitly authorized cleanup.

### Go Cache Rules

- Let Go choose its standard user cache for local build, test, vet, run,
  generate, and MyGo invocations, including commands started through `make`.
  Do not set `GOCACHE` in repository scripts; preserve an explicit caller or CI
  override. Use `go env GOCACHE` to inspect the effective location.
- Do not create task-, timestamp-, platform-, or feature-specific Go caches such
  as `spool-gocache`, `spool-go-cache`, `spool-side-fix-go-cache`, or
  `codex-go-cache` in a system temporary directory. Go manages cache keys itself.
- Additional worktrees use the same standard user cache rather than creating
  a cache for each worktree. Never use a different source tree's generated
  binaries or frontend output as a shortcut.
- Preserve the existing shared `GOMODCACHE`/`GOPATH`. Do not copy downloaded
  modules into scratch space or change persistent configuration with `go env -w`.
- If the standard cache or module cache needs permissions or network access,
  request approval; do not silently invent another cache directory to bypass
  restrictions. Do not migrate or delete existing caches without user approval.
- Do not routinely run `go clean -cache`, `go clean -testcache`, or
  `go clean -modcache`. Use `go test -count=1` only when an uncached test execution
  is needed; it still reuses compiled packages.

### Running Checks

Choose checks from the behavior changed in the current diff. During iteration,
run the smallest relevant package, test case, or test file and expand only when
the affected dependencies or a failure warrant it. Do not use `make test`,
`go test ./...`, or `go test -race ./...` as a default after every edit.

- Prefer tests of observable behavior, rendered semantics, and cross-component
  contracts. Avoid tests that only match implementation source, CSS declarations,
  or incidental wording; verify layout in browser or visual checks when needed.
- Documentation and comments need review and formatting/link checks, not runtime
  suites. For scripts and build configuration, check syntax and the affected
  command paths; use `make -n` to inspect recipes without building applications.
- For Go changes, test the affected packages first. Include callers when a shared
  interface or behavior changes. Use `GO_TEST_FLAGS='-run <pattern>'` to narrow
  iteration, then run the affected packages without that filter before handoff.
  Run `vet-go` for the relevant packages when Go source changes.
- Use `test-race` for changes to goroutines, synchronization, shared mutable state,
  cancellation, or process/session lifecycle, or when investigating a race. Start
  with the affected packages. Full-repository race runs belong to broad concurrency
  changes, explicit requests, or build/release CI.
- For frontend or tool changes, select the affected test files with `WEB_TESTS`
  or `TOOLS_TESTS`. `test-web` recompiles test fixtures before running selected
  files; do not rely on stale `web/.test-build/` output. Add a frontend type check
  for TypeScript changes and broaden tests when shared state or contracts change.
- Run browser, Android, container, or packaging checks when the changed behavior
  needs that environment. Do not run unrelated platform checks for a local change.
- Run the ordinary full suite (`make test`) for broad changes across subsystems,
  dependency/toolchain upgrades, an explicit request, or unresolved integration
  concerns. Build/release CI retains full tests, race detection, and vet.
- Once relevant checks pass, repeat them only after changes that can affect their
  result. Report the actual scope checked and any relevant checks not run.

Scoped entry points (Go paths are repository-relative; Web paths are relative to
`web/`):

```sh
make test-go GO_PACKAGES=./internal/agent
make vet-go GO_PACKAGES=./internal/agent
make test-race GO_PACKAGES=./internal/agent
make test-tools TOOLS_TESTS=tools/run.test.mjs
make test-web WEB_TESTS=./test/preferences.test.mjs
```

Run commands from the repository root with `sh tools/run.sh <task> <command>`.
The wrapper creates a unique run in the system temporary directory, exports
`SPOOL_RUN_ROOT`, `TMPDIR`, `TMP`, `TEMP`, and `GOTMPDIR`, and removes the run
after its command and owned process group stop, including failure and handled
SIGINT/SIGTERM. It preserves reusable tool caches. Bun is required for this POSIX
wrapper. For example, for an agent-package change, after preparing
frontend assets if needed as described in `README.md`:

```sh
sh tools/run.sh agent-check sh -eu -c '
  go test ./internal/agent
  go vet ./internal/agent
'
```

Use a descriptive task name for each check. The random
suffix isolates concurrent runs; it is for scratch space, never for caches.
All Make test/vet targets wrap their checks automatically. `make test` runs tools,
Web, ordinary Go tests, and vet; race detection is an explicit `make test-race`
step. Android commands keep the convenient `sh android/tools/run.sh <command>`
entry point, backed by the same wrapper.
Browser checks validate that `TMPDIR` belongs to `SPOOL_RUN_ROOT`, without any
checkout-path requirement. For example:

```sh
sh tools/run.sh web-test bun run --bun --cwd web test
sh tools/run.sh browser bun web/test/session-files-smoke.mjs
sh tools/run.sh dev make dev
```

### Isolation And Cleanup

- Prefer `t.TempDir()` in Go tests. When `os.MkdirTemp`, JavaScript `mkdtemp`, or a
  browser profile is necessary, use the configured temporary root and register
  cleanup immediately (`t.Cleanup`, `t.after`, `finally`, or a shell trap).
- A `TestMain` must explicitly clean up after `m.Run()` and before `os.Exit`;
  deferred cleanup does not run on `os.Exit`. Helper subprocesses that call
  `os.Exit` must not create their own unowned `TestMain` temporary homes first.
- Stop and wait for owned servers, browsers, agents, and other child processes
  before deleting their files. Do not kill unrelated processes or remove a run
  directory that belongs to another session.
- For disposable manual app/smoke tests, set `SPOOL_HOME` or `-data-dir` to a
  directory inside the run root. Mock provider homes must also be isolated there.
  Never test against or clean `~/.spool`, `~/.spool-dev`, real provider homes, or
  a developer's database. Normal persistent development data is not scratch.
- Clean the owned run on success, failure, and handled interruption. If a tool
  cannot honor these paths, document the exception and explicitly clean its
  owned temporary directory. After a crash or forced kill, inspect for leftovers
  and confirm no process is using them before proposing cleanup.
- Retain only diagnostics needed to explain a failure in
  `build/artifacts/<task>/` (or existing `smoke-results/`); avoid copying complete
  caches, source trees, app bundles, databases, or browser profiles as evidence.
  Never include credentials or native session histories in retained reports.
- Reuse a task's artifact directory after confirming it is not in use. Do not
  accumulate `final`, `final2`, timestamped, or randomly named diagnostic copies.
- Cleanup is limited to the current run's owned scratch directory. Removing
  historical artifacts, old caches, or system temporary files requires explicit
  user approval and a scoped list of paths. Never broadly delete `/tmp`,
  `$TMPDIR`, `go-build*`, or another project's files.
