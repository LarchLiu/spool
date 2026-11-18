# Agent slash commands

Type `/` in the chat composer to browse commands for the selected agent. The
menu filters names, display names, descriptions and aliases; Up/Down selects a
row, Tab or Enter inserts it, and Escape closes the menu. Send the completed
command to execute it.
Switching the model to another agent switches the command catalog as well.

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
