# Repository Instructions

## Development And Test Storage

Keep reusable caches separate from disposable test data. Do not create ad hoc
files, cloned source trees, binaries, browser profiles, or cache directories in
`/tmp`, `/private/tmp`, or the system/user temporary directory. The paths below
are relative to the repository root and are already covered by `.gitignore`.

| Purpose | Required location | Lifetime |
| --- | --- | --- |
| Go build/test cache (`GOCACHE`) | `build/cache/go-build/` | Reuse across tasks and runs |
| Go module cache (`GOMODCACHE`) | Existing `go env GOMODCACHE` location | Shared; do not duplicate |
| Per-run scratch space | `build/tmp/<task>.XXXXXX/` | Remove when the owning run exits |
| Go compiler/linker scratch (`GOTMPDIR`) | `<run>/go/` | Same as the owning run |
| Other tool/test temporary files (`TMPDIR`, `TMP`, `TEMP`) | `<run>/tmp/` | Same as the owning run |
| Disposable app data and browser profiles | Subdirectories of `<run>/` | Same as the owning run |
| Logs, screenshots, coverage, and diagnostic reports to retain | `build/artifacts/<task>/` | Keep only the latest useful set |
| Normal build/package output | Existing `build/` layout | Reuse existing output paths |

Existing tool-managed `web/node_modules/`, `web/.test-build/`, `frontend/`,
`.mygo/`, and `smoke-results/` remain valid. Do not copy their outputs elsewhere
just to rerun a check. Keep reusable test helpers in the source tree rather than
leaving one-off scripts in a system temporary directory.

### Go Cache Rules

- Set `GOCACHE` to the absolute `build/cache/go-build` path for every local Go
  build, test, vet, run, generate, and MyGo invocation, including commands started
  through `make`. All tasks in this checkout use the same cache.
- Do not create task-, timestamp-, platform-, or feature-specific Go caches such
  as `spool-gocache`, `spool-go-cache`, `spool-side-fix-go-cache`, or
  `codex-go-cache` in a system temporary directory. Go manages cache keys itself.
- Additional worktrees should reuse the primary checkout's canonical cache when
  permissions allow, rather than creating a cache for each worktree. Never use
  a different source tree's generated binaries or frontend output as a shortcut.
- Preserve the existing shared `GOMODCACHE`/`GOPATH`. Do not copy downloaded
  modules into scratch space or change persistent configuration with `go env -w`.
- If the canonical cache or module cache needs permissions or network access,
  request approval; do not silently invent another cache directory to bypass
  restrictions. Do not migrate or delete existing caches without user approval.
- Do not routinely run `go clean -cache`, `go clean -testcache`, or
  `go clean -modcache`. Use `go test -count=1` only when an uncached test execution
  is needed; it still reuses compiled packages.

### Running Checks

Run commands from the repository root. Use a subshell so these environment
variables do not leak into the user's interactive shell. For example, after
preparing frontend assets as described in `README.md`:

```sh
(
  set -eu
  REPO_ROOT="$(git rev-parse --show-toplevel)"
  cd "$REPO_ROOT"
  export GOCACHE="$REPO_ROOT/build/cache/go-build"
  mkdir -p "$GOCACHE" "$REPO_ROOT/build/tmp"
  RUN_ROOT="$(mktemp -d "$REPO_ROOT/build/tmp/verify.XXXXXX")"
  cleanup() { rm -rf -- "$RUN_ROOT"; }
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  mkdir -p "$RUN_ROOT/tmp" "$RUN_ROOT/go"
  export TMPDIR="$RUN_ROOT/tmp" TMP="$RUN_ROOT/tmp" TEMP="$RUN_ROOT/tmp"
  export GOTMPDIR="$RUN_ROOT/go"

  go test -race ./...
  go vet ./...
)
```

Use a descriptive task prefix instead of `verify` when appropriate. The random
suffix isolates concurrent runs; it is for scratch space, never for caches.
The same environment wrapper applies to `make test`, `make dev`, Node tests,
browser probes, and release smoke checks. For long-running development, keep
the run directory until the app and its children have stopped.

### Isolation And Cleanup

- Prefer `t.TempDir()` in Go tests. When `os.MkdirTemp`, Node `mkdtemp`, or a
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
