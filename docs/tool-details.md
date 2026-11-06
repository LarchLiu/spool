# Lazy Tool Details

Web and Android negotiate compact chat payloads with `tool_details=lazy` on
chat SSE and either that query parameter or `X-Tool-Details: lazy` on HTTP.
Clients that do not negotiate this keep the complete transcript representation.
Normal user/assistant messages and transcript part positions are unchanged.

Tool input, output and thinking parts carry a small `tool` summary instead of
their body: chat-scoped reference, body version, byte count, classification,
path/query, bounded thinking preview and patch statistics. Labels and icons
remain client-localized. The provider transcript remains authoritative; no
second SQLite transcript or durable tool-detail store is introduced.

The Go native history cache retains compact parts and a separate
content-addressed body map, deduplicating identical bodies within a session.
The existing whole-session LRU/byte budget includes both. As before, a single
oversized session can exceed the target rather than continually evict itself.
Internal runner/reconciliation consumers can reattach immutable body strings;
transport projection never mutates those snapshots.

## Detail Reads

`GET /api/chats/{chat}/tool-detail?ref={ref}&offset={bytes}&prefix={sha256}`
uses the same account/chat permission gate as reading the chat. References are
not credentials and cannot be used to read another chat. Responses are `no-store`.

- Each response contains at most 32 KiB of source, ending on a UTF-8 boundary.
- `next` and `prefix` cover bytes received so far. Appends resume from there.
- Replaced content or an invalid prefix returns `reset: true` and offset zero.
- Missing references return 404, invalid offsets 400 and unavailable history 503.
- The whole body remains available through subsequent sections; no original
  output is truncated or silently discarded.

While a detail is expanded, the existing chat SSE summary version drives
coalesced reads. An outstanding read is allowed to complete and paint even
when newer versions arrive. The latest pending revision is fetched afterwards.
This is throttling, not cancel-and-restart debouncing: a continuously streaming
Think must not starve its initial detail response. Closing/unmounting stops
follow-up reads and ignores old callbacks. Web also aborts its fetch.

Both clients retain bounded detail-page caches (128 pages / approximately
8 MiB) outside chat state. Web clears on account/CSRF changes; Android cache
lifetime is the authenticated UI instance. A body renders at most one 32 KiB
section. Complete small sections retain Markdown/diff styling; partial large
sections use selectable raw text with navigation rather than interpreting
incomplete Markdown/patch syntax.

Compound shell calls, including Codex `exec_command` and Claude `Bash`, appear
as one initially collapsed group per original call. Its summary lists each
activity category once (for example, “Ran command, read files”). Expanding the
group shows the parsed commands in order. These derived steps have no individual
detail disclosure: the provider only supplies the call's combined output. Calls
remain separate even when adjacent calls have the same categories; progress
messages keep their place between calls. Raw inputs and compact server summaries
use the same grouping on Web and native Android.

Each provider turn has one workflow disclosure. The first user input and the
answer after the last input stay outside it; intervening user inputs, earlier
answers and tool/progress messages retain their chronological positions inside.
Collapsing the workflow hides all intervening inputs and activity together.
User inputs reuse the initial message's right alignment, attachments, avatar
and footer. The workflow spans the same width as the final answer. Codex and
Claude native user records retain their own timestamps; records without one use
the turn's start time.

## Verification

Run from the repository root; `run.sh` owns all disposable data and configures
the canonical Go cache:

```sh
sh android/tools/run.sh go test -race ./internal/workspace ./internal/model
sh android/tools/run.sh go -C android/transport test -race ./...
sh android/tools/run.sh bun run --bun --cwd web test
sh android/tools/run.sh bun run --bun --cwd web build
sh android/gradlew :app:assembleDebug :app:testDebugUnitTest :app:lintDebug
```

`android/tools/tool-detail-reference.mjs` exercises the production React loader
in Chrome: no closed-body requests, continuous revisions do not cancel the
initial response, closing stops reads and large outputs remain page-bounded.
Use `SPOOL_NODE_MODULES` if Playwright comes from a shared tool runtime. All
fixtures are synthetic and the report contains no conversation content.

For an explicitly selected real Codex session:

```sh
sh android/tools/run.sh env SPOOL_HISTORY_PROFILE=/path/to/rollout.jsonl \
  go test ./internal/workspace -run '^TestNativeHistoryProfile$' -count=1 -v
```

This freezes only complete JSONL records into `t.TempDir`, removes them on exit
and logs only counts/timings. It measures process-cache cold parse, five warm
reads, all history pages including JSON encoding and five first-detail reads
per tool kind. It excludes network, authentication, client JSON decode and UI
rendering. Allocation totals are cumulative, not peak or retained memory.

The November 1 user-selected roughly 185 MB snapshot has 62 turns / 15,306
parts. One measured cold parse/cache takes 1.918 s (index 639 ms; native turn
decode 781 ms), and five warm reads have a 3.474 ms median. Full-detail mode
produces 103.011 MB across 62 pages versus 7.586 MB for compact mode (92.64%
less); the largest page falls from 21.128 MB to 550,751 bytes. Compact page
construction/encoding has median 3.769 ms / p95 6.450 ms. These are local
measurements, not a latency SLA or an Android smoothness result.

That snapshot's largest Think is 1,795 bytes. Warm first-detail reads are
3.102 ms median for Think, 2.975 ms for tool input and 5.238 ms for tool output.
This does not establish that an observed Think delay is network/server-bound.
Android caches unchanged Think previews and avoids unchanged disclosure
text/icon/layout work. Its debug-only `spool-performance` dump now reports
per-kind request queue, HTTP/decode, main-thread delivery and content-build
times under `toolDetails`. Content-build time does not include a subsequent
layout/draw frame. It contains no IDs, paths, bodies or credentials.

The detail endpoint also includes `server_ms` and a `Server-Timing` header for
chat authorization/hydration plus detail selection. This excludes login/session
identity lookup before the chat route and response transmission/JSON encoding.
Android's `lastServerMs` is -1 if the connected server predates this field.
`lastRequestMs` includes the entire transport/request/decode interval, so the
difference is not a pure network-RTT measurement.

One real-device Think expansion on the updated Android client records 536 ms
request/decode, 135 ms main-thread delivery wait and 14 ms content build. After
history hydration settles, a later sample records 199 ms request/decode,
1.36 ms delivery wait and 18.24 ms build (maximum build 21.80 ms across four
requests). This is a small observational sample, not a controlled before/after
benchmark; it does show that Markdown construction alone does not explain the
observed initial wait. Full hydration still reaches over 3,000 mounted views
and approximately 846 MB of native allocations, so memory/scroll work remains.

## Remaining Work

An actively appended large file can still invalidate the entire native cache
generation during parsing; three retries can end in `errHistoryChanged`.
The original unfrozen profile reproduced this (~33 s). Incremental append
index/cache reuse is not implemented here and must preserve rewrite/truncate
invalidation. The profile's frozen-source measurements do not prove this case
fixed. Server restarts also still require an initial parse.

The compact protocol requires the updated server as well as the updated
clients. Native/Web raster parity and real-device finger-to-photon/scroll
performance remain separate gates. These tests do not prove either complete.
