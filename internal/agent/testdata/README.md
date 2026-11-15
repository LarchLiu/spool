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

Activity categories come first from what the provider itself reported: Codex's
command actions (`commandActions` live and in thread history, `parsed_cmd` in a
rollout) and the files of a native file change. Calls without that use the
normalized tool name and structured arguments.
An explicit native `unknown` action stays a general command, without inferring
a more specific category from its text.
Shell tools inspect the executable and literal shell wrappers, rather than
matching keywords in paths, descriptions, search patterns, or source code.
Scripts, redirections, expansions, and mixed command categories use the generic
command label. Categories follow Codex's own reading, so both providers label
the same command alike: content searches such as `rg` and `grep` are searches
with their pattern and path, `rg --files` is a file listing, and `find` with a
name pattern is a search.
Print-only `sed` commands such as `sed -n '1,140p' file` read that file;
in-place edits, external scripts, and `e`/`w` commands keep the command category.
A pipeline stage that only narrows or formats the previous stage's output
(`head`, `wc`, a `grep` without a path) does not change the pipeline's category.

Literal compound shell calls split into display-only operation summaries, one
per sequential step. Each pipeline stays together because providers return its
combined output, and a lone pipeline is a single call with its own details.
Derived rows have no detail disclosure. Their parent exposes the original
command and combined output once; ordinary single tool calls retain details.
The provider's original call/result IDs and bodies are preserved. Bounded
operation metadata also travels in lazy historical summaries.

`android/app/src/test/resources/tool-classification.json` is shared by the Go,
Web, and Android tests. It covers Codex raw shell/JSON calls and Claude's
`Bash.command`, `Read`, `Grep`, `Glob`, `Edit`, `Write`, and other native tools.
The adjacent `tool-operations.json` fixture covers decomposition, pipeline
grouping, quoting, and the equivalence of raw and lazy summaries.
