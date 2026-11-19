# Agent slash commands

Type `/` in the chat composer to browse commands for the selected agent. The
menu filters names, display names, descriptions and aliases; Up/Down selects a
row, Tab or Enter inserts it, and Escape closes the menu. Send the completed
command to execute it.
Switching the model to another agent switches the command catalog as well.
The Android composer uses the same project catalog, command routing and compact
menu layout, including skill groups and source labels. Its popup keeps the input
focused for the touch keyboard and scrolls long catalogs above the composer.
The connected Spool server must also include command discovery; updating only
the Android APK cannot add command support to an older server. Android shows a
server update message when the returned catalogs lack the `commands` field,
and distinguishes this from loading, discovery failure and an empty search.

## Discovery and saved catalogs

Claude Code returns its SDK/print-mode commands in the `initialize` control
response alongside models. Spool saves both in the existing agent catalog in
SQLite. Commands include the native description, argument hint, aliases and
builtin marker. The UI reads the saved catalog immediately; the existing agent
refresh replaces it after successful discovery and preserves it on failure.
The ordinary command section contains `/code-review`, `/plan`, `/goal`, `/loop`
and `/deep-research`. Except for the native plan control, entries appear only
when the installed CLI advertises them. The **Skills** section is discovered
separately through the read-only native `get_skills_dialog` control request.
Spool includes a skill only when that session marks it as advertised and enabled
for user invocation, and its slash-command name exists in `initialize.commands`.
Other commands are hidden, rather than relabeled as skills. Builtin status alone
does not determine whether an entry is a skill.
Older CLIs or connections that do not support `get_skills_dialog` show the
ordinary command shortlist without an inferred Skills list.
Project permission discovery also returns commands from initialization in that
project, including project skills and plugin commands. Those take precedence in
the composer. Internal and removed commands are excluded. `/plan` is provided
through the native permission control protocol even when absent from the list.

Codex app-server has no equivalent slash-command discovery or generic dispatch
method. Spool maintains the three supported commands `/goal`, `/plan` and
`/review`, marked `commandSource: "protocol"`. Claude discovery is
marked `commandSource: "native"`. Terminal-only commands without an equivalent
are excluded. Native command availability can vary with CLI version, account,
project configuration and installed plugins; errors from native execution are
shown to the user.

Codex skills are discovered through native `skills/list` in the current project.
The menu lists enabled skills in a **Skills** section with their native display
name, short description and source (personal, project, system, administrator or
plugin). Project catalogs are cached for 30 seconds and kept separate from the
global saved model catalog. Claude skills use their advertised names and
descriptions in the same Skills section and support the same search, click and
keyboard selection. Claude's native Skills list also includes legacy custom
commands from `.claude/commands`; these appear because Claude itself classifies
them as skills. Disabled, shadowed and model-only skills are excluded.

## Execution

Claude's advertised commands are sent through its persistent stream-json
session. `/code-review [target]`, `/loop [interval] [prompt]`,
`/deep-research [topic]` and selected skills use native slash-command dispatch.
`/plan [task]` sets native plan permissions before optionally sending the task.
`/goal` retains native goal controls. A builtin entry appears in Skills only
when the native Skills response confirms it.

Native commands and ordinary prompts share the same live subagent tracking.
Claude `Agent`/`Task` calls and task lifecycle events are joined by their native
IDs; foreground forks, background tasks and nested agents each get a live
activity. Codex collaboration calls and child-thread events update each child's
own state, independently of whether the spawning tool finished. Spool reads a
bounded page of recent child turns/items when Codex does not forward progress;
these reads never resume a child or take its writer lease.

Each task shows its description, state and available progress summary, last
tool and usage. Parallel tasks keep separate rows. If the main reply ends first,
the request remains running while known child tasks continue, so progress,
approvals and cancellation still work and the idle timeout does not reclaim
their process. Live task rows remain visible beside the main reply. Refreshing
an active session recovers them from the running turn. Child transcripts never
become the main reply. Failed progress reads are shown as unavailable, not as a
successful or failed child execution: only consecutive reads that told nothing
count, and a child's own events or requests start the count again. Claude Code
offers no such read, so a child of a finished turn that reports nothing for 15
minutes is shown as unavailable and no longer holds the request open. The turn
Claude Code runs in answer to a task notification belongs to the same request.
History display uses native tool records; Spool does not load independent
subagent histories.

Codex implements these commands:

| Commands | Execution |
| --- | --- |
| `/goal [condition\|clear]` | Native goal get/set/clear and initial turn |
| `/review [base-branch]` | Native review of uncommitted changes or the branch |
| `/plan [task]` | Native plan collaboration mode, optionally starting a task |

Selecting a skill inserts its command token in the composer. Add task text and
send it, or send the selected skill directly. Codex receives an explicit native
`skill` input containing its name and path alongside the task text. The server
refreshes the project's skill list before execution and resolves the path itself;
the browser does not supply filesystem paths. Duplicate skill names and names
that overlap `/goal`, `/plan` or `/review` receive distinct stable command tokens.
Disabled or removed skills fail explicitly when submitted from a stale menu.

Local controls return output in the command notice without creating a synthetic
assistant turn. Model and permission changes are saved for subsequent requests.
Commands that start agent work use the normal streaming turn path. Unknown
commands fail explicitly rather than being sent as ordinary model instructions.
Commands reject attachments and cannot be steered or queued while a reply is
running. Session ownership, project access and CSRF checks apply to execution.

The saved catalogs are available in the normal agent response as
`catalog.commands`. Project-scoped commands are returned in the permission
catalog's `commands`. Local execution uses `POST /api/chats/:id/commands` with
`{"command":"/context"}` and optional model, reasoning effort and permission
selection. Commands that start agent work use the existing messages endpoint;
goal and session management retain their dedicated endpoints.

## History display checks

Command and skill invocations remain user input when native histories are read,
including Claude's command envelopes and a later continuation. Skill instruction
records stay hidden. The same history projection serves Desktop, Web and Android,
and preserves command requests when converting between Codex and Claude.
Codex records the skill selection separately from the task: its history shows
the task text, or `$skill-name` for an invocation without arguments. Injected
`<skill>` instruction bodies do not become extra user turns.

The opt-in browser check renders synthetic native histories through the actual
server and frontend at desktop and phone widths. It writes server responses to
`build/artifacts/slash-history/` for the Android renderer contract check:

```sh
CHROME_BIN='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' \
  make test-go GO_PACKAGES=./internal/workspace \
  GO_TEST_FLAGS='-run TestSlashCommandHistoryBrowser'
SPOOL_COMMAND_HISTORY_FIXTURES="$PWD/build/artifacts/slash-history" \
  sh android/gradlew --offline :app:testDebugUnitTest --tests '*MainActivitySlashHistoryTest'
```

Build the frontend with `make web` before the browser check. All snapshots and
screenshots in these checks contain synthetic conversations.
