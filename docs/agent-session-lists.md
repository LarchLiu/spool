# Native agent session lists

Go's `agent.SessionLister` returns provider-owned session metadata through
`ListSessions(context.Context, ListSessionsOptions)`. Transcript bodies remain
in the provider history store. Spool applies project/worktree ownership, sharing
permissions, exclusion of Spool-created sessions, and the latest-20 window across
both providers after discovery. The existing 10-second refresh interval remains.

Only enabled providers are listed. Discovery runs outside the lock used by page
requests: a request never waits for another caller's discovery, and a navigation
event follows any change. Each provider returns at most its 2000 most recently
updated sessions.

## Codex

`CodexSessionLister` initializes an app-server connection using the configured
Codex executable and calls `thread/list`, following `nextCursor` until the
requested limit or the end. It closes the server's input and waits briefly for a
normal exit before reaping its process group.
It sorts by `updated_at`, includes all model providers, and explicitly selects
`cli`, `vscode`, `exec`, `appServer`, and `unknown` sources. Archived, ephemeral,
and subagent sessions are excluded; a subagent is recognized from the structure
of `source`, not from text inside it. Listing does not start/resume a thread or
send a model request.

Native names take precedence over previews; a preview title is provisional. Native timestamps in seconds are
converted to milliseconds. Spool does not scan Codex rollout directories for
listing. The server's default JSONL metadata-repair behavior is retained, so
histories missing from its state database can still be discovered.

## Claude

`ClaudeSessionLister` implements the local-filesystem behavior of
`@anthropic-ai/claude-agent-sdk` **0.3.323** in Go. It does not require the Claude
CLI, a JavaScript runtime, or a watcher:

- Resolve `CLAUDE_CONFIG_DIR` (default `~/.claude`) and enumerate project session
  files. Optional directory filtering includes git worktrees by default and
  recognizes truncated/hashed project directories.
- Validate UUID filenames, order candidates by modification time, sample up to
  64 KiB from each end, and extract SDK-style metadata. Honor custom-title
  sidecars, title priority, relocation, and continuation records.
- Filter sidechains, optionally exclude programmatic/daemon sessions, deduplicate
  IDs, and apply offset/limit after filtering.

The SDK's custom `sessionStore` hook is outside the local-filesystem adapter.
Unknown optional creation times stay zero in the adapter; Spool uses the update
time when projecting these into its database.

Spool departs from the SDK where its own list needs it: the session ID keeps the
transcript file name's letter case, a `cwd` missing from the head sample is taken
from the tail, and `IncludeUntitled` keeps sessions without a derivable title,
which Spool shows under the provider label. `NewClaudeSessionLister` reuses the
result for a transcript whose file and sidecar are unchanged, so a refresh only
stats files.

Discovery and opened history store titles in one form: a single line of at most
200 bytes. A sampled title that complete history would replace (for example a
last prompt when a summary record exists) is provisional and never overwrites
the title confirmed by opening the chat.

`LocalSessionIndex` tracks file identity/size/modification time and native update
time to invalidate opened history, including replacement at the same timestamp.
A provider error retains its last successful snapshot for up to five minutes and
is retried once a minute; successful empty results remove that provider's old
entries. Each outage and recovery is logged once, naming only the provider. Shutdown cancels and waits for discovery
before closing the database.

For large Claude histories, sampled AI titles remain provisional so they cannot
replace a custom title confirmed by reading the complete history. Opening a chat
also checks current tail/sidecar rename metadata independently of cached turns.

## Verification

Opened history, native pagination and tool activity projection are described in
[agent-session-history.md](agent-session-history.md).

Run ordinary offline tests and vet with the repository's scoped targets:

```sh
make test-go GO_PACKAGES='./internal/agent ./internal/workspace'
make vet-go GO_PACKAGES='./internal/agent ./internal/workspace'
make test-race GO_PACKAGES='./internal/agent ./internal/workspace'
```

The optional SDK parity check downloads the pinned npm package into the owned
run directory, verifies its pinned sha512 integrity before extraction, and compares both implementations on the same synthetic history:

```sh
sh tools/run.sh claude-list-parity bun tools/check-claude-session-list.mjs
```

An optional real Codex app-server smoke test uses a synthetic rollout and an
isolated `CODEX_HOME`, without model inference:

```sh
sh tools/run.sh codex-list-smoke sh -eu -c '
  SPOOL_CODEX_LIST_SMOKE="$(command -v codex)" \
    go test ./internal/agent -run TestCodexSessionListNativeSmoke -count=1
'
```

The SDK is a verification dependency only. Updating its pinned version requires
updating the integrity value, reviewing changes to `listSessions` and rerunning the parity corpus.
