# History Presentation

## Pagination

The Go store parses and caches the complete native session. HTTP chat reads,
history pages and SSE snapshots apply the same transport-only window:

- Start at the newest turn, or immediately before the requested cursor.
- Include whole turns backwards until original text/image URLs total at least
  200,000 bytes. The crossing turn is included in full.
- There is no fixed turn count, no per-part truncation and no hard page-byte cap.
- This selection precedes lazy tool projection: tool bodies influence page
  boundaries even though negotiated responses contain summaries, not bodies.
- Older-page cursors anchor to the preceding native turn, not an unwritten live
  turn whose identity may change.
- Web and Android automatically fetch older pages sequentially, yielding after
  each committed page. Loading is not restricted to scrolling to the top.

The 32 KiB limit on tool-detail responses is separate from history pagination.

## Provider-Only Context

Native files remain read-only. Filtering changes display projection, user-input
classification and derived titles only, not the context used to resume/convert
a provider session.

Codex `compacted` records and context-compaction items are not transcript parts.
Some native versions also write internal reasoning and a normal-looking
`final_answer` just before `compacted`. Only an uncorroborated response tail
matching that record's compaction message is hidden. A normal AgentMessage
event protects a real response; unrelated answers and ordinary Think remain.
Claude compact boundaries, `isCompactSummary` records, local command echoes
and known continued-session summary wrappers are hidden.

Known user-context tags include environment, permissions, skills, collaboration
mode, IDE context, AGENTS instructions, system reminders and local command
wrappers. Complete quoted attributes are supported. Standalone
`codex_internal_context` (including `source="goal"`), `subagent_notification`
and `task-notification` blocks can also be removed after/between request text.
Unknown/incomplete tags, inline mentions, fenced/indented code and quotations
are preserved. Assistant/tool text does not pass through user-context cleanup.

After filtering, both clients render the user row (including avatar, timestamp
and spacing) only when text or attachments remain. Image/file-only input remains
visible. Assistant/tool output on automatic continuation turns is retained even
without user input; entirely provider-only historical turns are omitted by Go.

## Live Compaction

Compaction is an optional `compacting` boolean on a running turn, not another
chat message or activity. Codex contextCompaction start/end notifications and
Claude system status/compact-boundary events update it. Duplicate status events
are coalesced; resumed output, explicit end, completion, failure, stop or restart
clear it. Nested Claude agents and unrelated Codex threads/turns cannot change
the current turn. Older CLIs that emit only an end marker cannot provide a
reliable start state; no artificial compaction progress is inferred.

Both clients replace the existing work label with localized "Compacting
context..." only while the running flag is active. There is no persistent
compaction message in the conversation and no added connection/header status.

Read-only, count-only verification of an explicitly selected Codex session:

```sh
sh android/tools/run.sh env SPOOL_HISTORY_PROFILE=/path/to/rollout.jsonl \
  go test ./internal/agent -run '^TestNativeCompactionProfile$' -count=1 -v
```
