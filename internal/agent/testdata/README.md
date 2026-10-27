# Agent Transcript Fixtures

These JSONL files are checked-in snapshots used by the Go parser tests. They
are intentionally local to this project so tests and tooling do not depend on
another checkout being present.

- `carry/codex-tools.jsonl`: Codex function calls, custom tool calls,
  reasoning parts, and matching outputs.
- `carry/claude-tools.jsonl`: Claude Bash, Write, Read, Edit, thinking, tool
  results, and structured file patches.
- `traces/codex-real-sanitized.jsonl`: Codex sanitized transcript with text,
  tool calls, and tool results.
- `codex-native-paginated.jsonl`: Codex output-only page used to verify the
  safe fallback for a result without its preceding tool call.
- `claude-native-replay.jsonl`: Claude assistant text replay.

The Web activity renderer derives display categories from the normalized
`tool_use`, `tool_result`, and `thinking` parts. The fixtures currently cover
shell, read, create, edit, think, and generic result rows. List-files and web
search classifiers are also supported for live `Glob`, `ls`, `find`,
`WebSearch`, and `WebFetch` events when those tools are used.
