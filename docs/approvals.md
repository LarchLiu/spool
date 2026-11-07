# Native approvals and interactive input

Web and Android render the same pending request. The refusal action has its own
button. Other decisions share one confirm button; a separate dropdown appears
only when more than one other decision is available. Selecting a menu item changes
the confirm button and its policy preview without submitting. Pressing the main
button submits that selection.

## Supported protocols

| Provider | Request | Supported replies |
| --- | --- | --- |
| Codex | Command execution and file changes | Accept once, accept for session, decline, cancel turn; command and network policy amendments when advertised |
| Codex | Additional permissions | Select individual network/filesystem grants, turn or session scope, strict automatic review |
| Codex | `item/tool/requestUserInput` | Advertised options, free text when allowed, secret inputs, answers keyed by question ID |
| Codex | MCP elicitation | Accept, decline, cancel; advertised session/permanent persistence, form values and URL authorization |
| Codex | Legacy command/patch approval | Native approved, approved for session, denial feedback, abort |
| Claude | `can_use_tool` | Exact or edited input, supplied permission suggestions, denial feedback, deny and interrupt |
| Claude | `AskUserQuestion` | Single/multiple choices, custom text, text/number questions; answers keyed by question text |
| Claude | MCP `elicitation` | Accept, decline, cancel; form inputs, property order and URL authorization |
| Claude | `request_user_dialog` | Declared model fallback, automatic mode, permission review, network, device MCP consent, planning, resume, cost and plugin choices |

The Claude dialog registry in `internal/agent/claude_dialog.go` is also the source
for the exact `supportedDialogKinds` declaration. Unknown or undeclared kinds are
left for the native CLI or another attached client; they are never reported as a
user cancellation. Native JSX/installation flows and dialogs requiring native
telemetry or device proofs are not declared as portable dialogs. A tool that
requires an unsupported native interaction retains a visible refusal action and
explains where the interaction must be completed.

MCP primitive schemas use normal form controls. Other self-contained JSON schemas
use a JSON input with the requested schema available for inspection. The server
validates the entire original schema, including nested objects and constraints.
External schema references are not loaded. Codex `openai/userVerification` requires
a proof produced by the native device and is shown as requiring verification in
the Codex app; a normal approval cannot generate that proof.

## Validation and history

The server retains the pending choices. A submission identifies one of those
choices; it cannot supply new policy objects, persistence scopes or permissions.
Permission subsets reuse the exact requested entries and preserve filesystem deny
entries. Claude persistent permission suggestions are omitted when the CLI sets
`suppress_always_allow_rule`. Invalid submissions leave the request open for retry.

Requests withdrawn by the CLI, a stopped turn or a changed chat stop accepting
responses. Buttons disable during submission. Audit history records the actor,
request summary, selected decision and time, including separate answer/cancel
actions. Submitted answers, secret values, edited input and denial feedback are
not copied into the audit summary. Timestamps follow the description inline.

Protocol coverage uses installed Codex 0.166.0 and Claude Code 2.1.310 schemas.
Tests use synthetic provider processes and isolated app data; no real inference
or provider history is needed.

## Permission discovery failures

Spool checks the selected CLI's permissions in the chat's working directory
before accepting a message. Being able to run Codex interactively does not prove
that its app-server initialization and configuration RPCs succeed in that directory.
`Permission discovery is unavailable` reports a failure of this check; by itself
it does not identify a damaged Spool database.

The Web client shows discovery failures beside the composer, retries transient
failures at most twice, and keeps an explicit Retry button. A failed message keeps
its draft and refreshes the permission catalog; the message itself is never retried
automatically. An unavailable mode still requires choosing an available mode.

On the service host, find `event=permission_discovery_failed` in the Spool log.
`stage=session_directory` identifies a failure reading the chat's working-directory
record; `stage=provider` identifies CLI discovery. The error distinguishes executable
and directory resolution, process startup, initialization, `config/read`, and
`configRequirements/read`. These details stay in the host log. Remote clients
receive the localized message and `permission_discovery_unavailable` error code.

On macOS and other Unix desktops, Spool appends the selected CLI's directory and
existing standard agent installation directories to each agent child's `PATH`.
This allows CLI launchers to find their runtime when the app was opened from
Finder with a minimal environment. Existing `PATH` entries retain precedence;
the parent environment and shell startup files are unchanged. Version-probe
failures retain their native output alongside the exit code, so an exit status
127 can identify the missing launcher dependency in the host log.

For `env: node: No such file or directory`, compare `command -v node` in the
working terminal with the desktop's environment. As a temporary recovery, fully
quit Spool and launch it from that terminal with
`open -a "Spool" --env "PATH=$PATH"`, then retry discovery in the client.
This passes the terminal's PATH to that app launch without changing system settings.
