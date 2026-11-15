# Native agent history and tool items

How Spool reads a chat's history and turns provider tool calls into the
activity rows of the Web and Android clients. Session discovery is described
in [agent-session-lists.md](agent-session-lists.md).

## Tool items

`internal/agent/codex_items.go` converts one native Codex tool item into a call
and its result. The same code reads the three places an item appears: live
`item/started` and `item/completed` notifications, `thread/turns/list` history,
and a rollout's `item_completed` record. Call and result carry the item ID, so
calls that run at once keep their own output.

A call keeps the provider's structured facts beside its text (`Part.Native`,
never sent to clients): status, exit code, duration, Codex's command actions
and the files of a file change. An added or deleted file arrives as content and
becomes a real diff. Claude results keep their `is_error` flag.

`ToolSummary` is what clients receive. Its kind, path and query come from, in
order: the provider's structured facts; the tool name and its literal command
(`internal/workspace/tool_kind.go`, mirrored in `web/src/tool-kind.ts` and
`NativeToolKind.java`); a generic label. Missing native actions and a generic
Codex `unknown` action use the literal command classifier, preserving individual
read/search/list/command steps in compound commands. Specific native categories
and native operation lists take precedence. The Claude Bash fallback follows
the same read/search/list/command categories; `internal/agent/testdata/README.md`
lists them. These categories are display only and take no part in permissions.

Newlines, `;`, `&&` and `||` separate operation rows. A pipeline remains one
operation: filters such as `head` preserve the source command's category and
target, so `rg TODO src | head -20` is a search in `src`. Its command and output
stay together; the provider does not supply each stage's intermediate output.

`status` is `running`, `completed`, `failed` or `declined`, and is absent when
the provider did not say. Clients show a failed or declined call as such and
leave it out of change statistics.

## Codex history

A Codex chat is read through app-server the way Codex's own clients view a
thread they do not own: `thread/read` for metadata, then `thread/turns/list`
with full items. Spool never calls `thread/resume` to read. A resume takes the
thread's writer lease, starts the user's MCP servers and can append to the
rollout; a read does none of these and works while another process is writing.

`agent.CodexHistory` keeps one app-server process for all reads and stops it
after two idle minutes. `internal/workspace/history_native.go` caches the newest
window of at most 16 threads, bounded by the configured history cache size
(64 MiB by default). Each response reads up to eight native pages of ten turns,
stopping after about 200 KB while preserving
whole page boundaries. Older pages are not retained in the server cache. An
unchanged rollout file reuses the native window and cached per-turn metadata.
An append refreshes the native window and scans metadata only in the new
records, retrying any incomplete last line. A window read within two seconds
of the rollout's last write may lack that write, so it is read once more after
the rollout has been quiet that long.

One window is read from Codex at a time, outside the store and cache locks:
other chats keep their cached windows, new turns and writes while a read
waits.

`GET /api/chats/{id}/history` carries Codex's opaque continuation cursor inside
a chat-scoped transport cursor, so paging survives cache eviction and appended
turns. Pages inside a loaded window are anchored to its turns; before its first
turn the Codex cursor continues. The live turn retains its starting window and
cursor, also in the response to stopping it. Tool details and
images and reasoning details carry a stable turn/item locator and use `thread/items/read`; older
app-servers fall back to `thread/items/list` filtered to that turn. Neither
requires walking earlier turns or loading the whole session. Chat access is
checked before these reads.

The rollout parser (`session_transcript.go`, `session_turns.go`) remains the
reader for everything native paging cannot serve, chosen per thread:

- a legacy thread, including sessions converted by Spool;
- a Codex without these methods, or one that is disabled;
- a Codex that cannot be started, for every thread not yet read, or a single
  thread whose read fails, for that thread (each retried after 30 seconds);
- a paginated thread that returns no turns. Codex's history index is a view
  rebuilt from the rollout by whoever writes the thread, so a missing index
  reads as an empty thread; the rollout decides.

When Codex supplies a native turn ID, both readers preserve it for actor
attribution. Legacy turns without one retain their existing file-offset
identity. A finished turn is saved under the identity its rollout records: the
announced ID where the rollout names it, otherwise the first turn begun after
the request, or the file-offset identity of a legacy rollout. A session resumed
before its rollout was located has no request offset, and keeps what was
announced. After a crash before Spool saved the native turn ID, unresolved actor
records seek directly to the saved request offset and stop at the next native
turn boundary to recover that identity and its approval actions, including when
the turn is outside the loaded native window. The recovered identity is saved,
so it is searched for once. A rollout that cannot be read leaves only that
request unattributed. This recovery does not build a full session index or
decode transcript bodies.

The native API does not report per-turn model and reasoning effort. Spool joins
them from each rollout `turn_context` by native turn ID, including on older
pages. Metadata lookup starts at the tail and stops once the oldest requested
turn is reached; opening older pages extends the search backwards. Tool bodies
are skipped rather than decoded into transcript parts. A reverse search scans
up to 4 MiB of blocks per request, plus decoding the matching context records,
and saves its cursor if more is needed. Metadata beyond that budget remains
absent until a later request continues the search. Missing IDs never inherit
current thread settings. Cached results survive appends, including one made
while they are read; a changed file identity, truncation, same-size edit, or
changed append boundary resets them. A single tool's details need no metadata
and do not search for it.
A matching in-memory turn can fill metadata that has not reached the rollout yet.

Session conversion still reads the complete source file. An explicit external-file
request is decided from the loaded turns when they attach or reference the
file. Otherwise the source file is read, so links from older pages remain
authorized after those pages leave the cache; if it cannot be read the request
fails as unavailable rather than as denied.

Claude history is read from its JSONL files by the Go parser.

Web and Android keep compound steps as summary rows and expose the original
command and combined output once under “Command and output”. Full patch
updates replace earlier bodies; command output deltas still append by item ID.
A completed item is the record of its body: text that does not continue what
was streamed (reasoning sections, rewritten output) replaces it, and a
completion without output keeps the streamed output.

## Verification

```sh
make test-go GO_PACKAGES='./internal/agent ./internal/workspace ./internal/model'
make vet-go GO_PACKAGES='./internal/agent ./internal/workspace ./internal/model'
make test-web WEB_TESTS=./test/tool-activity.test.mjs
sh android/tools/run.sh sh android/gradlew :app:testDebugUnitTest --tests '*NativeToolKindTest' --tests '*NativeBehaviorTest'
```

`web-behavior.json` is generated from the Web code; after changing Web tool
classification run `sh tools/run.sh sync-web-behavior bun android/tools/sync-web-behavior.mjs`.

An optional smoke test reads real paginated history from the installed Codex
in an isolated `CODEX_HOME`, without model inference:

```sh
sh tools/run.sh codex-history-smoke sh -eu -c '
  SPOOL_CODEX_LIST_SMOKE="$(command -v codex)" \
    go test ./internal/agent -run TestCodexHistoryNativeSmoke -count=1
'
```
