# Spool: MyGo Desktop + Go API + Web/PWA

Only the Go project is affected. The Rust application is unchanged.
The application name is **Spool**. The source directory remains `go/`; the Go
module/package, commands and data directory use lowercase `spool`. The desktop
bundle identifier is `com.cloudgeek.spool`.

## Architecture

- `cmd/desktop`: MyGo native control center and tray, without a WebView.
- `cmd/server`: optional headless HTTP server using the exact same services.
- `internal/agent/`: persistent CLI processes, Codex/Claude protocols, model and
  permission discovery, and process cleanup on supported platforms.
- `internal/model/`: shared conversation/attachment types, model and permission
  options, message limits, ID validation, and bounded UTF-8 text helpers.
- `internal/workspace/`: SQLite storage, users/projects/settings, authenticated
  HTTP/SSE APIs, presence, chat orchestration, and service lifecycle/leases.
- `internal/i18n/`: shared locale resolution and message translation.
- `internal/testutil/agentfixture/`: fake native CLI subprocesses shared by unit
  and integration tests; production entry points do not import this package.
- `web/`: React + TypeScript + Vite chat client, usable in a browser or as a PWA.
- `frontend/`: production Web assets embedded in **both** Go executables.
- `resources.go`: the root package's only Go source file, embedding frontend,
  locales and icons. Keeping embeds beside their resource directories avoids
  duplicate assets and unsupported parent-directory `go:embed` paths.
- `spool.sqlite`: users, projects, project membership, chats, turns, attachments, server
  settings, agent settings/catalogs, issue schedules/runs, task-chat assignees,
  PR artifacts, and hashed login sessions. New tables are added on startup
  without replacing existing projects or chats.

Both entry points use `internal/workspace`, which coordinates `internal/agent`
and `internal/model`. The CLI package does not import SQLite or HTTP handlers.
Tests live next to their implementation; cross-layer chat tests stay in
`internal/workspace`. The transcript change keeps SQLite at schema version 1;
it does not change the `~/.spool` data directory, HTTP routes, or CLI wire
protocols.

There is no separate frontend service in production and no Node.js runtime
requirement. Installed and signed-in Codex/Claude CLIs are still required.
MyGo is pinned to **v0.6.4**, which requires **Go 1.27.3+**. Go's automatic
local toolchain selection downloads that version when necessary. Frontend builds
need Node.js 22.12+ and npm (DiceBear 10.x requires Node.js 22+).

## Development

From this directory:

```sh
npm --prefix web ci
npm --prefix web run build
go run ./cmd/desktop
```

For frontend hot reload, run the backend and Vite in separate terminals:

```sh
go run ./cmd/server
npm --prefix web run dev
```

Vite proxies `/api` to `127.0.0.1:8787`. Change `web/vite.config.ts` if the
configured backend port differs. The proxy preserves the development page's
`Host` (`changeOrigin: false`) so Go can validate its original `Origin`; login,
language/theme changes, and other writes still require the usual CSRF token.
Use the Go-served production URL, not Vite, when checking Service Worker updates
or offline behavior.

`go run .` is not an entry point: the root package only embeds resources. Use
`go run ./cmd/desktop` or `go run ./cmd/server`; both commands are unchanged.

## First Desktop Setup

1. Open **People**, enter a username, nickname, and password, then save.
   Passwords must be 12–72 bytes and are stored only as cost-12 bcrypt hashes.
   Existing users can be renamed, have their password reset, or be disabled.
   Editing a user revokes their existing Web login sessions; disabled users and
   their history remain in the database.
2. Open **Projects**, choose an existing local directory, name the project, and
   check the people authorized to use it. Save the project.
   The native directory picker runs asynchronously, starts at the current path,
   and leaves that path unchanged when canceled. Only one picker opens at a time.
3. Open **Agents** to inspect Codex/Claude installation, versions, executable
   paths, and available models/levels. Blank executable paths use automatic
   discovery; a saved path overrides environment/PATH detection. Agents can be
   enabled/disabled. Saving an agent also triggers a fresh probe.
4. Open **Service** and choose the listen IP, external Web port, public origin,
   and idle release interval. Settings are persisted in SQLite. **Stop → Start**
   applies saved listener/origin/idle settings; stopping interrupts active tasks.
5. Open Web Chat and sign in. With an authorized project and no chats yet, type
   directly in the composer, choose an agent/model, and send: the first message
   automatically creates a chat in the current project. Click **+** beside a
   project whenever you want another chat in that project. **+** only opens a
   local draft: no chat row is written to SQLite or added to history until Send.
   Switching away from an unsent draft or starting another discards that draft;
   empty drafts are not restored after a page reload.

### Scheduled GitHub Issues

In **Projects**, enable **Scheduled GitHub issues** and choose the GitHub
`owner/repo`, interval (1–10080 minutes), Codex or Claude, and one or more
responsible project members. All assignees can see and control task chats:
send messages/files, stop responses and answer permission requests, even when
project-wide chat sharing is disabled. Responsibility changes also update
existing task chats; removing membership or disabling an account immediately
revokes its controls. Regular chats remain owner-only, including shared ones.
Human follow-ups use the same project lock so scheduled recoveries cannot overlap
them. Model, reasoning level and native permission mode
are optional. Configuration is per project; no repository/path is hardcoded.
The project must point at the root of a checkout whose `origin` matches the
configured GitHub.com repository. Install and authenticate `gh` on the service
host, and ensure the repository has a `dev` branch.

The Go scheduler runs in both desktop and headless services while the Web
service is running (checks admitted within about 15 seconds of their due time).
It does not wake a previous chat or replay missed intervals. Stopping the
service cancels both preflight and accepted agent work and waits for actual
cleanup before releasing the project lock. Disabling/archiving a project or
changing its schedule affects future runs; it does not discard accepted work.
An update cannot start while a scheduled preflight or agent turn is running.

The Go backend, not the agent, performs admission (requirements 1–3):

- Inspect Git state and worktrees, locate the **main checkout**, and acquire
  `docs/.issues-automation.lock`. Its owner includes automation ID `issues`,
  run ID, chat ID, service instance, host and PID. An advisory guard also
  serializes stale-lock reclamation. Only proven-ended owners can be reclaimed;
  age alone never expires a claim. Unknown/external owners are left alone.
- Fully paginate REST issues (all states, excluding PR entries), timelines and
  comments, query paginated Development connection events, and verify actual
  linked PR state with `gh pr view --json`. Previously recorded branches are
  also checked for PRs, recovering a PR created just before a crash.
- Preserve and synchronize the main checkout's **only** shared local ledger,
  `docs/issues.md`. Status is `close` only for a closed issue, `pr` for an open
  linked PR (drafts included), `processing` for an unfinished claim, otherwise
  `open`. Merged PRs do not close still-open issues locally.
- Resume an unfinished `issues` claim only after proving its previous run has
  ended. Otherwise claim at most one lowest-numbered eligible issue. Labels
  match exactly `web`, `server`, `app`; a multi-label issue occupies every one
  of its categories. Unknown or overlapping file scope is conservatively
  serialized, even across categories. Unlabelled issues are synchronized only.
- Write the complete claim before sending any code-editing task. No candidate
  means no agent invocation, new empty chat, or periodic empty notification.

Each actionable run creates a fresh owned Spool chat/native session with the
selected issue, comments and execution contract (requirements 4–8). The agent
must follow `AGENTS.md`, safely fetch/pull the latest `dev` with `--ff-only`,
create or recover an isolated `codex/issue-N-fix` branch/worktree, run the
project's actual tests/lint/typecheck/build and `git diff --check`, then commit,
push without force and create/reuse a PR targeting `dev`. Failed or unavailable
verification must retain `processing`, validation results and recovery notes;
it must not push or create a PR. PRs are never automatically merged and issues
are never automatically closed.

The task includes a shell function named `attach_artifact` backed by a scoped,
temporary Spool callback. It verifies that the PR is open with the claimed head
branch and `dev` base, then attaches it to **this run's chat**. The capability
cannot attach to another chat or repository and expires when the round ends.
Confirmed PRs are also reconciled and attached after successful agent execution.
Links appear below the chat header. Do not copy callback credentials into Git,
the ledger, PRs or final messages.

The ledger contains a `spool:issues:v1` marked JSON block. Human text outside the
block, notes, owner, branch/worktree, validation, PR history and unknown JSON
fields are preserved. Standard existing Markdown issue tables can be imported;
unparseable in-progress records fail closed for manual migration rather than
being stolen. The ledger is local automation state: do not commit it to `dev`
or use a worktree/branch copy. Keep the `owner` object intact when recording
progress. Explicitly abandoning a claim requires preserving history and clearing
its owner/branch/worktree before resetting to `open`.

Any failed GitHub page/query aborts preflight **before** writing the ledger or
claiming work. DNS/timeouts are diagnosed as network problems, not invalid
tokens. When the host environment is sandboxed, authorize external networking
through that environment's normal approval process and retry; the scheduler
does not bypass its sandbox. Only explicit invalid/expired credential responses
are reported as authentication failures. Failures and the latest chat ID appear
in Projects; repeated unchanged errors and idle checks do not produce empty
periodic reports. Agent progress and required approvals remain in the new chat.

For a headless host, configure an existing project while the service is stopped:

```sh
go run ./cmd/server -list-projects

go run ./cmd/server -issues-project PROJECT_ID -issues-repo OWNER/REPO \
  -issues-interval 15 -issues-agent codex -issues-assignees alex,bob

go run ./cmd/server -issues-project PROJECT_ID -issues-enabled=false
```

`-issues-owner` can select the creating owner from the assignees (otherwise the
first assignee is used); it also works alone for a single responsible person.
`-issues-model`, `-issues-effort` and `-issues-permission` are optional. These
commands only save configuration; they do not start a round. For example,
`/Users/alex/Work/muyun/test` with `LarchLiu/test` can be configured as a test
project without changing the behavior of other projects.

### User Avatars

Every account gets a deterministic [DiceBear](https://github.com/dicebear/dicebear)
avatar generated from its display name (falling back to username). In Web Chat,
click your avatar at the bottom of the sidebar to choose **Clay**, **Pixel Art**,
**Croodles**, **Lorelei**, **Notionists**, **Toon Head**, **Voxel Art**, or **Thumbs**.
Clay is the default. Pick a suggested avatar, use **More avatars** to browse another
batch, then **Save avatar**. **Use my name** restores automatic generation in
the selected style; automatic avatars follow display-name changes, while picked
avatars stay unchanged. Canceling the picker leaves the saved avatar untouched.

Avatars appear in the sidebar and online people list. Choices are stored per
account in SQLite and restored on other devices. Existing databases receive the
avatar table automatically; accounts and login credentials are unchanged. The
version-pinned DiceBear SDK renders SVG locally: names are not sent to DiceBear,
no external avatar service is required, and arbitrary SVG/URLs are not accepted.
The former Robots, Shapes and Initials styles migrate to Clay on startup while
preserving automatic/picked seeds; Thumbs choices remain selected. Only the eight
selected style definitions from `@dicebear/styles` are bundled. Style credits
and license links appear in the picker and remain embedded in each SVG, including
CC BY 4.0 attribution for Croodles (vijay verma) and Toon Head (Johan Melin).

### Chat Composer, Attachments And Permissions

User messages and streamed agent replies render Markdown, including headings,
lists, quotes, fenced code, links, GFM tables, strikethrough and task lists.
Raw HTML is disabled and unsafe link protocols are filtered. Markdown images
are shown as links instead of fetching external images automatically; uploaded
image attachments retain their existing authenticated preview.

The composer's left toolbar contains a single **+** for both files and images,
followed by the native agent's permission selector. Agent/model selection and
Send remain on the right. Attached images appear as thumbnails above the text;
click to open a full-size preview. File cards show the name and a small text/code
excerpt where possible. Remove an attachment before sending with its **x**.
Images in chat history can also be previewed; other files can be downloaded.
Files-only messages are supported, and failed submissions keep their draft and
attachments for retry.

Send changes to a **Stop response** button while a turn is running. Stopping
cancels only that chat's current turn and its CLI process tree, clears pending
approvals, and saves the partial reply. The next message resumes the saved native
session. A click before the message POST is acknowledged waits for its server turn
ID before stopping; closing the browser alone still does not cancel accepted work.

- Up to **6 files**, **4 MB per file**, **16 MB total** per message.
- Native image inputs support PNG, JPEG, GIF and WebP, at most 25 megapixels.
  Other formats are regular files; active image formats such as SVG are not
  accepted as images.
- Attachment bytes and metadata are stored in SQLite, not in an `agents/`
  directory or project folder. Native CLIs receive private temporary copies of
  the files. Historical files are rematerialized on each turn so resumed chats
  get current paths; temporary copies are removed when the service stops.
- Codex receives native `localImage` input; Claude receives native base64 image
  blocks. Both get file paths for regular attachments. File contents are user
  data, not system/developer instructions.
- Downloads and previews require current project access. Uploads, approvals,
  and chat controls require the chat owner; writes also require same-origin
  and CSRF validation. Attachment responses are never cached by the PWA.

Permission choices are discovered by the Go service in the project's working
directory and checked again on submission. Unavailable choices show a policy or
CLI capability reason. Each accepted turn records its selection in SQLite;
reopening a chat restores it. Changing modes between turns reuses the live CLI.

- **Claude:** Manual, Auto, Accept Edits (default), Plan, Don't Ask, and Bypass
  Permissions, subject to native CLI/account/project restrictions. Mode changes
  use `set_permission_mode` and wait for acknowledgement before sending input.
  Bypass Permissions skips native permission checks: use only in trusted
  projects. The startup capability flag enables choosing it, not bypass by
  default.
- **Codex:** Ask For Approval or Approve For Me (`user` / `auto_review`). The
  service reads project config, administrator requirements and feature flags;
  it retains `on-request`, workspace-write sandboxing, and network disabled
  unless approved. Approve For Me changes the reviewer, not the sandbox scope.
- Native command/file/extra-permission requests appear in the chat with
  **Deny / Allow Once**. A reply applies only to the exact pending request,
  cannot be replayed across chats/turns, and never writes persistent allow rules.
  Pending approvals do not time out an otherwise active long-running task.

The default listener is `127.0.0.1:8787`. To allow LAN access, choose
`0.0.0.0` (IPv4) or `::` (IPv6), then access the computer's LAN address.
All chat APIs require an enabled user account, including on localhost. There is
no default password, remote sign-up, or remote administrative API.

Closing the settings window hides it to the tray and **keeps the Web server and
agent tasks running**. Reopen through the tray or Dock/app icon. Explicit Quit
stops the server, terminates CLI processes, and saves interrupted partial replies.
The service becomes unreachable while the computer is asleep or the app is quit.

### Headless Bootstrap

The desktop is the normal management interface. A server-only host can create
initial users/projects locally with the following commands **while its service
is stopped**. These commands never expose administration over HTTP:

```sh
SPOOL_NEW_PASSWORD='replace-with-a-long-unique-password' \
  go run ./cmd/server -add-user alex -nickname Alex

go run ./cmd/server -add-project Example -directory /absolute/project/path -grant alex

go run ./cmd/server -host 127.0.0.1 -port 8787
```

All entry points support `-data-dir /path` (default `~/.spool`), or
`SPOOL_HOME`. `SPOOL_CODEX_BIN` and `SPOOL_CLAUDE_BIN`
are fallback executable overrides when no explicit agent path is saved.
The SQLite file is `spool.sqlite` inside that directory. The application only
uses the requested directory; it does not automatically discover or import data
from a differently named directory. The previous `~/.tern` directory is left
untouched; it is not read by Spool. An existing SQLite store can be relocated
while all service/desktop processes are stopped, preserving the database and
renaming its file to `spool.sqlite` (and any matching `-wal`/`-shm` sidecars).
Only one desktop/headless service may own a database at a time. A SQLite lease
prevents accidentally running two managers and marking live tasks interrupted;
after a forced crash, wait up to 30 seconds for the lease to expire.

## Projects, Accounts, And Trust

Each chat is permanently associated with a project and its creating user.
Remote callers cannot supply an arbitrary working directory. Each agent turn,
including resumed turns, uses the project's canonical local directory. Changing
that directory is rejected once the project has chats; create another project
instead. Projects can be renamed, archived, and have their member list edited.
Archiving/revoking membership blocks new access but does not cancel an already
accepted task.

Users only see projects they have been granted. Chats are private by default;
the desktop Projects panel can enable **Share chats with all project members**
for an individual project. When enabled, members can read one another's
history and streams, but only the chat owner can send messages or control a
run. Scheduled-task chats additionally allow all configured task assignees to
send messages, stop turns and handle approvals. Knowing another user's chat ID
never bypasses current membership and task-assignment checks.
Native CLI session IDs and configured executable paths are not returned by the
Web API. Online nicknames are visible to signed-in users.

**This is not a multi-tenant sandbox.** Users in a project share its actual files;
all CLIs run as the desktop OS user with that user's provider login. A malicious
coding-agent request may read or modify files the underlying CLI can access.
Grant access only to trusted people. API ownership is not OS/container isolation.

Authentication uses seven-day HttpOnly, SameSite=Strict cookies, SHA-256 hashed
session tokens in SQLite, CSRF headers, strict JSON request bodies, origin checks,
and a per-client-IP login attempt limit. Cookies are Secure when the configured
public origin uses HTTPS. Proxy forwarding headers are not blindly trusted.

## Agent Detection And Models

At startup and every five minutes, discovery resolves each executable, reads
`--version`, and obtains model metadata without submitting a user prompt:

- Codex: initialize a temporary `app-server`, `config/read`, paginated `model/list`.
- Claude: initialize a temporary stream-json connection and read its advertised
  `models` from the initialize control response. Older/unsupported CLI versions
  report an error rather than inventing a model catalog.

Installation state, enable flag, configured/resolved path, version, supported
models/levels, effective defaults, check time, last success, and errors are saved
in SQLite. Failed probes retain the previous successful catalog and mark it
stale. **Refresh installation & models** probes immediately. Probes have short
separate deadlines; they never terminate a chat task or reuse its connection.
Model metadata currently reflects the discovery process's working directory and
CLI configuration; individual projects can still have provider-specific config.

The Web composer offers Codex and Claude model/level selection with model-specific
slider stops. Only levels advertised by the selected agent/model are shown.
Claude also offers **Default** to leave the level to its native CLI; models without
advertised effort support have no adjustable slider. Selections apply to the next submitted turn and are saved on the chat and
turn. Known unsupported combinations are rejected before history/task creation.
The composer menu groups detected models under **Codex** and **Claude**. Selecting
a model selects its agent for the next message; chat creation itself launches no
CLI process. Uninstalled/disabled groups remain visible but cannot be selected.
Codex sends `model`/`effort` on `turn/start`. Claude uses its stream-json
`set_model` and session-local `apply_flag_settings` (`effortLevel`) control requests,
waiting for acknowledgements and confirming explicit effort with `get_settings`
before sending the user message. These controls do not write Claude settings
files. A rejected/overridden effort or an older CLI without these controls fails
before the prompt is sent, rather than silently ignoring the choice; update
Claude Code if necessary. Both agents can change models/levels without restarting
the active CLI, and idle-release resume reapplies the selected options. Claude's
supported effort names are separate from Codex's: Codex-only `none`, `minimal`,
or `ultra` are never sent to Claude.

Any chat can choose an available agent/model for its next message. Selecting a
different agent does not start a process or change saved history until Send.
Sending converts the current native session into a new target-agent session and
resumes it **in the same Web chat**, retaining its ID, project, owner, title and
attachments. Switching back converts the latest context again; it does not
resume an outdated pre-switch conversation. Same-agent model changes still reuse
the live CLI. Agent/model changes are disabled while a turn is running, and only
the chat owner can submit a handoff.

Conversion follows Magpie's native Claude/Codex carry-over format (MIT notice in
`internal/agent/MAGPIE-LICENSE`). User and
assistant messages, embedded user images, and tool results are retained. Shell
calls translate to Claude's `Bash` or Codex's `exec_command`; other tools become
historical assistant notes rather than calls to tools the target does not have.
Claude's active parent chain and native compaction boundaries are respected.
Provider instructions and signed/encrypted reasoning are not transferred. Target
permission modes are discovered independently and default to that agent's normal
mode; permissions from the source agent are not inherited.

Source files remain read-only. Conversion requires available native history; if
it cannot be read or converted, Send fails without changing the chat. A failed
SQLite transaction removes the newly converted file and rolls back all metadata.
Web history keeps references to each original native segment, so historical model
attribution, reasoning summaries and tool displays survive reloads and repeated
agent changes without displaying the imported history twice. SQLite stores only
segment references and byte offsets, not conversation text or a second transcript.
The target CLIs must be installed and signed in as usual.
Like Magpie's converter, conversion to Claude rejects project paths requiring
its hashed long-path naming (over 200 encoded characters), rather than writing
a session that Claude cannot resume.

### Magpie-Compatible Session Transcripts

Every chat exposes one ordered `transcript` with Magpie's JSON structure:
`parts` plus optional `cut`, `source`, and `captured`; each part contains
`role`, `kind`, `text`, and optional `name`, `cut`, and `model`. The Go Codex
app-server and Claude stream-json readers normalize their native events into
those shared part kinds (`text`, `thinking`, `tool_use`, `tool_result`,
`context`, and `image`) as they arrive. Adding an agent therefore requires an
adapter from its native events to the same part type, not a new Web/API shape.
The public session's `turns` remain Spool-specific lifecycle metadata and point
into this transcript by part ranges; they are not a second copy of chat text.

`internal/agent.ParseSessionTranscript` also reads native Codex rollout and
Claude Code JSONL files without modifying them. It applies the same filtering
as Magpie (drops provider metadata/developer wrappers, preserves user/tool
parts, records assistant models, and ignores Claude synthetic messages). The
parser tests use the checked-in fixtures under
`internal/agent/testdata/`; they do not require another checkout at test time.
The bundled tool fixtures cover Codex `function_call`, `custom_tool_call`,
reasoning, and paginated results, plus Claude `Bash`, `Write`, `Read`, and
`Edit` records. Claude structured patches are converted to the shared diff
text used by the Web viewer.

SQLite deliberately does **not** store a transcript or message body. Session
rows keep lifecycle metadata and the provider's native session ID/path; turn
rows keep only lifecycle, selection, range, attachment, and error metadata.
The `session_history` table additionally references each native context segment
when a chat changes agents, with the first turn and imported-prefix byte offset.
`Prompt`, `Reply`, `Activities`, and normalized `Part` values are omitted from
the persisted turn JSON. The live normalized transcript exists only in memory
while a turn streams. When a completed chat is opened again,
`internal/agent.ParseSessionTranscript` reads the provider-owned Codex/Claude
JSONL and rebuilds the Magpie-compatible transcript, including command output,
file diffs, tool results, and readable reasoning summaries. Streaming and
historical parsing use the same 8,000-rune part and 400,000-byte transcript
limits. The SQLite schema remains version 1; there is no v1-to-v2 migration
for uncommitted test data.

Agent icons come from `@lobehub/icons` (direct, tree-shaken Codex/Claude SVG
components). They appear in model group headings, the composer model selector,
project chat lists/Recents, and the conversation title. The title's subtitle shows
the project's name rather than repeating the agent name.

The icon package's UI peer dependency tree currently reports five low-severity
`npm audit` findings through its math-rendering dependencies. Only the two SVG
components are imported; that UI/math code is not included in the Web bundle.
No forced major dependency overrides are applied.

## Process Lifetime And Streaming

- One persistent CLI process **per chat**, never shared between users/chats.
- Codex initializes once, starts/resumes the native thread once, then uses
  `turn/start`. Claude keeps stream-json stdin open and submits another user
  message only after the preceding `result`.
- **No running-turn total limit and no output-inactivity timeout.** Only after a
  terminal reply does the default 15-minute user-idle timer start. New input
  cancels that timer. After release, the next input resumes the saved native
  provider session in the same project directory.
- The user message appears optimistically as soon as Send/Cmd+Enter/Alt+Enter is
  pressed. A POST reserves the turn and returns `202`; a separate authenticated
  SSE connection streams snapshots of the current turn, coalesced at 100 ms.
- Browser disconnects, switching chats, and mobile background suspension do not
  cancel accepted work. EventSource reconnects with a fresh history/live snapshot;
  the client does not resubmit the original message automatically.
- Partial replies are checkpointed during output (at most once per second), and
  terminal replies are persisted transactionally. A restart marks unfinished
  tasks interrupted. Abrupt termination can lose output since the last checkpoint.
- The conversation follows new output only while at the bottom. Scrolling up
  reveals a floating latest-message button. Empty assistant/activity headings and
  empty activity disclosure arrows are not rendered.
- No added system/user prompt wrapper, artifacts scan/index/download, workspace
  preparation, Layers injection, or per-chat `agents/` directory is performed.
  Existing project instructions and each CLI's native configuration still apply.

## Online Presence

Authenticated pages send a heartbeat every 20 seconds and on visibility changes.
Presence is kept in memory and broadcast over SSE; users are deduplicated by
account and their tab/device connection count is aggregated. A visible page is
online, a background page is away, and connections expire after 90 seconds.
Disabled/revoked login sessions disappear from presence. The desktop Online tab
and Web header show the list. Mobile OS suspension may delay these indications;
agent task state is independent of a person's presence.

## Navigation, Appearance, And Languages

The Web sidebar groups chats beneath collapsible projects, with compact nested
rows, running-task indicators, per-project **Show more**, and a cross-project
**Recents** section. Each project row has its own **+** to create a chat; there is
no global New Chat/agent selector above the sidebar. Clicking a project name only
expands/collapses its chat list, without changing the active chat or creating one.
Clicking a recent chat also
selects and expands its project, revealing older selected chats when necessary.
Private chats and, when enabled, shared project chats appear only for currently
authorized projects. Sidebar metadata refreshes every 15 seconds; the active
chat continues streaming over SSE.

The composer starts at one visible line, grows with explicit line breaks and
wrapped text up to six visible lines, then scrolls internally. It shrinks again
when text is removed or sent, and recalculates when its width changes.

Both Web and desktop support **Light**, **Dark**, and **Follow system**, plus
**English**, **简体中文**, **日本語**, and a system-language option. Theme and language
changes apply without restarting the service or agent processes. The Web uses the
device's `prefers-color-scheme` and ordered browser languages. MyGo uses its native
appearance and locale APIs, including title bars, native controls and tray labels.

In Web, open the appearance icon beside your name in the sidebar footer. In the
desktop control center, open **Appearance**. Preferences are stored in SQLite:
one local desktop record, and an independent record for each Web user. Web user
preferences follow that account on the next login or reload, without changing
other users or the desktop. Logged-out choices are temporary; the offline fallback
uses device preferences because it cannot retrieve SQLite settings. No personal
preferences or chat data are put in browser Cache Storage or localStorage.

Shared message catalogs live in `locales/en.json`, `locales/zh-CN.json`, and
`locales/ja.json`; Go embeds them and Vite bundles the same files. Missing keys
fall back to the English source. Tests check matching keys and interpolation
parameters. User messages, assistant output, project names, chat titles and raw
provider/OS diagnostics are not machine-translated.

## PWA And HTTPS

The build includes a manifest, real 192/512 PNG icons, standalone display,
installation prompt where supported, a versioned Service Worker, and an offline
reconnection page. Only public, immutable frontend assets/icons and the offline
page are cached. Login responses, `/api`, chat history, SSE, and credentials are
**never** put in Cache Storage. Offline mode cannot send messages or run agents.

Service Workers require a secure context. `http://localhost` / `127.0.0.1` on
**the current device** is a browser exception; plain `http://192.168.x.x` from a
phone is not. For remote PWA installation, use a trusted HTTPS reverse proxy or
private-network HTTPS endpoint, and set **Public origin** to its exact origin,
for example `https://chat.example.com` (no path). A simple Caddy configuration is:

```caddyfile
chat.example.com {
    reverse_proxy 127.0.0.1:8787 {
        flush_interval -1
    }
}
```

Keep the upstream bound to loopback when using a same-machine proxy, use trusted
certificates, and avoid proxy buffering or short SSE timeouts. Once HTTPS public
origin is configured, use that HTTPS URL to log in: Secure cookies intentionally
will not work over an unrelated plain HTTP LAN address. iOS installation uses
Safari's Share → Add to Home Screen; not every browser emits an install prompt.
TLS certificate provisioning and reverse-proxy setup are not automated here.

## SQLite Storage

The data directory is private (0700); `spool.sqlite` is created private (0600).
SQLite uses foreign keys, WAL, a busy timeout, transactions, and a versioned
schema. Its `-wal`/`-shm` sidecars are normal SQLite files, not additional JSON
stores. Stop the service before making a plain filesystem backup, or use
SQLite's online backup facilities; copying only a live `.sqlite` file can miss WAL
transactions.

There is deliberately **no compatibility or migration layer** for the old JSON
store. Existing `sessions.json` files and `agents/` directories are not read,
imported, modified, or deleted. Users, projects, settings, managed agents and all
new chats are stored only in SQLite. Start by creating users and projects in the
desktop control center. The CLIs still maintain their own native resume data
outside this application's database.

While the Go app is pre-commit and remains at schema version 1, development
SQLite contents are disposable test data: schema edits do not include a v1
migration. Start with a fresh database when testing a changed v1 schema; the app
rejects other explicit schema versions rather than attempting an upgrade.

## Build And Verify

```sh
npm --prefix web ci
npm --prefix web run build
npm --prefix web test
go test -race ./...
go vet ./...
go build -o build/spool-server ./cmd/server
go build -o build/spool-desktop ./cmd/desktop
```

Package the desktop application with the pinned MyGo CLI:

```sh
go run github.com/egoist/mygo/cmd/mygo@v0.6.4 build -skip-dmg
# Release builds do not use Developer ID signing or notarization.
# Cross-build examples:
# go run github.com/egoist/mygo/cmd/mygo@v0.6.4 build -platform windows/amd64
# go run github.com/egoist/mygo/cmd/mygo@v0.6.4 build -platform linux/amd64
```

`mygo.json` points at `cmd/desktop`, builds Web assets before packaging, and marks
Linux as native-UI-only (GTK needed, no WebKitGTK). Windows installer generation
may require NSIS. MyGo's normal OS requirements and code-signing requirements
apply. Builds are per platform/architecture, not one executable for all OSes.
The macOS output is `build/darwin-arm64/Spool.app` on Apple Silicon. Headless and
unbundled desktop binaries are `build/spool-server` and `build/spool-desktop`.
`assets/logo.png` is the source for the desktop, tray and Web/PWA icons, including
the browser favicon and Apple touch icon. Regenerate the derived assets with
`make branding` (or `go generate ./...`) after replacing the logo. `make web` and
MyGo packaging regenerate them automatically. Maskable PWA icons have a separate
opaque background and safe-area padding; other icons preserve transparency.

Tests use local mock CLIs, not model inference, and cover SQLite persistence and
rollback, ignored old JSON data, settings/users/projects/agents, project permissions,
login revocation, CSRF/origins, model discovery/catalog retention, CLI lifetime and
resume, SSE reconnect, presence, and PWA resource serving. Frontend regression tests
also cover optimistic messages and out-of-order REST/SSE responses.

An opt-in compatibility check restores converted sessions through the installed
Codex/Claude control protocols in isolated temporary homes, without sending any
user prompt or making an inference request:

```sh
SPOOL_TEST_NATIVE_RESUME=1 go test ./internal/agent -run TestConvertedNativeSessionResumesWithoutPrompt -v
```

## Releases And Desktop Updates

`.github/workflows/release.yml` runs when a **`v*` tag is pushed**. It builds and
validates everything before publishing the GitHub Release; do not publish an
empty Release first. Tags must be `vMAJOR.MINOR.PATCH`, optionally with a SemVer
prerelease suffix such as `-beta.1`. The tag determines the packaged version;
`tools/prepare-release.mjs` sets `mygo.json` in CI without changing the checked-in
development configuration.

### Prepare A Version Locally

Use the cross-platform Node.js helper after committing the changes to ship:
prepare on `main`, or merge the resulting release commit into `main` before
pushing the tag. CI rejects tags whose commits are not reachable from `main`.

```sh
# Preview the next patch version without changing anything.
node tools/release.mjs patch --dry-run

# Pick an explicit version (optional leading v is accepted).
node tools/release.mjs 0.2.0
# Or calculate the next version automatically:
# node tools/release.mjs patch
# node tools/release.mjs minor
# node tools/release.mjs major
# node tools/release.mjs 0.3.0-beta.1

# Equivalent Make entry point:
# make release VERSION=0.2.0

# Review, then push the current branch and release tag together.
git show --stat v0.2.0
git push --atomic origin HEAD v0.2.0
```

The helper synchronizes `mygo.json`, `web/package.json`, and both root version
entries in `web/package-lock.json`, without changing dependency versions or
running an install. Go modules do not have a separate application version field;
`go.mod` and `go.sum` are not changed. It creates a `chore: release v...` commit
containing only version metadata and an annotated local tag. It **never pushes**
or publishes. The tag remains authoritative for the CI package version.

Like Sessio's release helper, it requires a clean working tree and a named
branch. Commit or stash other changes first; the helper does not include them
in the release commit. It checks tags both locally and on `origin`, rejects
duplicate or older versions, and chooses automatic bumps from the higher of
the current MyGo version and the latest known tag. Bumping a prerelease first
promotes it when appropriate: `patch` turns `0.3.0-beta.1` into `0.3.0`.
Use an explicit version for another beta/rc release. The initial release can
use the existing `0.1.0` version; in that case an empty release commit is created
before tagging.

`--dry-run` is read-only and can preview even a dirty working tree, with a warning
to commit first. `--offline` explicitly skips the remote tag check, so only use
it when you know local tags are up to date. If origin is unavailable, the normal
command fails before editing any version files. A failed Git hook/signing step
leaves changes available for inspection rather than resetting them; inspect
`git status` and `git log` before retrying. No release signing keys or builds are
needed locally; the GitHub workflow validates keys, tests, and builds before
publication.

### Manual Build Verification

Use the separate **Build** workflow (`.github/workflows/build.yml`) to verify a
branch or commit **before creating a release tag**. It runs only on manual
dispatch; pushing a branch does not trigger it. Commit the workflow to the
default branch first so GitHub exposes the **Run workflow** button.

In GitHub Actions, select **Build > Run workflow** and choose the branch in
**Use workflow from**. Leave `ref` blank to build that branch, or enter another
branch name or full commit SHA in `ref` to override the build source. The workflow
resolves that source once and all packaging jobs use the same commit SHA,
displayed in the run summary. The chosen source must contain the build tooling;
old commits do not gain new scripts retroactively.

Build runs the tooling/frontend tests, Go race tests and vet, then creates macOS,
Windows and Linux packages for both amd64 and arm64. A six-target native smoke
matrix runs the packaged apps and checks the version, window event loop, SQLite,
Web health, embedded PWA and clean exit, just as in Release. Build is successful
only when tests, packaging and all smoke probes pass. It uses the source's
`mygo.json` version, builds the frontend once, and retains
`test-packages-<target>` and `test-smoke-<target>` artifacts for seven days.
`tools/prepare-build.mjs --check` validates without changing metadata; only
packaging jobs run the mutating mode. `tools/prepare-build.mjs`
modifies only the runner's packaging config, after metadata tests, to disable
build hooks, automatic updates and certificate signing. No existing tag, release
notes, repository variables or signing keys are required. These are unsigned
test installers (macOS still uses ad-hoc signing), not official release assets.

Build never creates tags, generates signed update metadata, creates/edits a
Release or changes Latest. It has read-only repository permissions; its purpose
is pre-release test, build and startup verification, not publication. Internally,
the shared archive/smoke tools receive `RELEASE_TAG=v<source version>` as a version
label, not as an existing Git tag or source ref.

### CI Publication

Pushing a new `v*` tag starts the **Release** workflow: verification, all six
builds, native startup smoke tests, signing and publication. There is no
build-only mode or `publish` checkbox in this workflow; use **Build** instead.

Release also supports a manual run with an **existing, pushed tag** to retry an
unfinished publication. Failed uploads leave an unpublished draft that can be
retried. Already published releases are deliberately rejected: ship changes
under a new version instead of replacing builds users have downloaded. Publishing
a Release through the GitHub UI no longer starts a build.

Release notes are generated with pinned `changelogithub@14.0.0` from the previous
**published** release in the same stable/beta/rc channel, not merely the previous
Git tag. The first release in a channel starts from the repository's initial
commit. All targets and the published Release use the same generated notes
artifact. If a draft exists from a failed upload, its notes and expected assets
are replaced on retry. Level-two headings are demoted in MyGo's changelog input
so they do not look like additional version sections.

Before the first release, generate and securely back up an update signing key:

```sh
go run github.com/egoist/mygo/cmd/mygo@v0.6.4 keygen -o /absolute/private/key-directory
gh variable set MYGO_UPDATER_PUBLIC_KEY < /absolute/private/key-directory/mygo-update.pub
gh secret set MYGO_UPDATER_PRIVATE_KEY < /absolute/private/key-directory/mygo-update.key
```

Keep the **same key pair** for later releases. The private key must never be
committed, uploaded as an asset, or included in an app. Only the final signing
step receives the private key and validates that it matches the public key.
Verification, dependency installation and all six builds have no signing secret;
only the public key is embedded in the app. The release repository must be public
for unauthenticated client downloads. Never ship a GitHub access token in the client.

Before building installers, CI runs the release tooling tests, frontend build
and tests, `go test -race -v -timeout=10m ./...`, and a separate `go vet ./...`
step. Verbose test progress distinguishes long-running tests from a stalled
runner. The verification job uses `tools/prepare-release.mjs --check` to validate
the release version, public key and notes without changing `mygo.json`;
frontend metadata tests still check the original development build hook. Only
the packaging jobs rewrite the config. CI builds the native desktop for macOS,
Windows and Linux, each on amd64 and arm64. Branding and frontend resources are
built **once** in the verification job and reused by all six targets. This also
ensures the ignored `frontend/` embed directory exists before MyGo compiles Go
code. CI clears
MyGo's `buildCommand` and skips its build hook: Spool uses embedded HTTP/PWA
assets, not MyGo WebView bindings, so packaging must not run the desktop app
to generate an unused TypeScript client or open the user's database. The tag's
commit must be reachable from `main`; all subsequent jobs check out that exact
verified commit SHA, not a fresh resolution of a potentially moved tag.

All six builds must succeed before publication. MyGo builds installers without
the private key; `tools/package-release.mjs` creates unsigned update archives of
the installed payload. Each target and the merged set are checked for exactly
the expected nonempty installers and archives before signing.

Before signing, a separate six-target `smoke` matrix extracts each update archive
and actually runs its packaged desktop on the matching OS and CPU architecture.
`tools/smoke-release.mjs` requires native execution (not cross-compilation), checks
the packaged version, native-window event-loop dispatch, SQLite initialization,
Web `/healthz`, embedded index/PWA manifest, and clean exit. Linux runs under Xvfb
with software GL; Windows and macOS use their runner's desktop. A failed probe
blocks publication and retains its diagnostics, not the extracted executable.

The desktop's `--release-smoke <report.json>` mode always makes a new temporary
database, binds only loopback on a temporary port, disables agent discovery and
automatic update checks, bypasses the installed app's single-instance lock, and
exits after the probes. It never opens a supplied `--data-dir` or the usual
`SPOOL_HOME` database. The runner enforces a 90-second process timeout.

The publish job's `tools/sign-release.mjs` signs every archive's SHA-256 digest,
generates the six `update-<platform>.json` manifests, and signs each manifest's
exact bytes into a detached, binary `update-<platform>.json.sig`. The signed
manifest binds the version, repository, target, archive URL/size/signature and
release notes. Both signatures and the final exact file set are verified before
upload. Missing, unexpected or incorrectly signed files block publication.

The publish job then generates `SHA256SUMS` and uploads all assets in one command
to a **draft** Release. It publishes only after every upload succeeds. The previous
Latest stays usable throughout. `SHA256SUMS` is a download-integrity convenience,
not an independently authenticated signature. Only the publish job has repository
write permission.

Actions are pinned to full commit SHAs; Dependabot proposes grouped updates.
The repository enables immutable releases for future publications and protects
`v*` tags: only administrators can create them, and existing release tags cannot
be moved or deleted. These are GitHub repository settings, not guarantees imposed
by a local clone; preserve them when transferring/forking the repository. Enabling
immutability does not retroactively lock the previously published `v0.1.1`.

Stable releases become Latest unless a higher stable version is already
published. Publication is serialized across tags and checks the versions again
before uploading, so an older build finishing later does not roll back Latest.
Prereleases are marked automatically and never become Latest. Stable apps read
`releases/latest/download`; prereleases can be downloaded manually but are not a
separate auto-update channel. Delta generation remains disabled so builds do
not depend on earlier release assets or a mutable Latest pointer.

Linux also gets `install.sh`, requiring Python 3, OpenSSL 3+ and curl or wget.
It embeds the public key and verifies both the manifest and archive before
extracting ordinary files into a private staging directory. Links, traversal,
special files and excessive expanded sizes are rejected. Replacement preserves
the old application until staging succeeds, and never removes user data. For
offline installation, put the matching `update-linux-<arch>.json` and `.json.sig`
beside the downloaded archive, then pass that archive to `sh install.sh`.
A per-user installation can update itself; `.deb` installations are normally
updated by the package manager instead. No headless-server update endpoint is exposed.

Release builds do **not** use OS certificate signing: no Apple Developer ID,
notarization, Windows Authenticode, certificates, or Apple credentials are
needed. macOS uses MyGo's minimal ad-hoc signature (`-sign -`) for executable
integrity, not a trusted distribution signature; Gatekeeper can block the
downloaded app on other Macs. Windows builds are unsigned and SmartScreen may
warn. CI removes any OS certificate settings from the release configuration
and explicitly skips notarization.

The Ed25519 **update archive signature** is separate and remains required.
It uses the freely generated MyGo key pair above, not a purchased certificate,
and lets installed apps verify that an update came from the same publisher.
Only the public-key variable and private-key secret are required by CI.

MyGo v0.6.4 authenticates archives but not their version metadata. Spool's
`internal/appupdate/manifest.go` adds a narrow, fail-closed HTTP boundary for the
exact compiled update-feed URL. It follows the asset redirects and verifies the
detached signature and manifest identity **before those same bytes reach MyGo**;
there is no check-then-refetch race. Only that feed and the exact archive URL
authenticated by its signed metadata receive update-network handling; unrelated
HTTP requests are untouched. MyGo keeps
responsibility for archive verification and installation, avoiding a fork or a
second installer implementation. Its process-wide HTTP client is wrapped once
at desktop initialization, before any services start. Revisit this adapter when
MyGo exposes an authenticated-manifest API. Builds missing Spool's release flags
cannot silently fall back to the legacy updater.

The legacy archive-signature field remains compatible with `0.1.1`, allowing it
to install the security update. That old client cannot acquire the new metadata
checks until it runs a new build. A genuine older signed manifest is rejected by
normal version comparison; rewriting it as a higher version invalidates its
signature. This does not implement a trusted timestamp/freshness service: an
attacker controlling distribution can still withhold updates. Initial installation
also requires trusting the source of the installer/script and its embedded key.

The native control center has an **Updates** page and the tray has **Check for
updates**. Official CI builds check shortly after launch and every 12 hours by
default. Automatic installation is **opt-in**, stored locally in SQLite, and
never forces a restart. Development builds and read-only installations explain
why updates are unavailable instead of checking in the background.

Installation and restart both acquire an exclusive service lease under the
same mutex that admits chat turns. Any active turn, including pending approval
or a stopped/superseded turn that is still cleaning up, rejects the operation
with a visible explanation. No update download starts in that case. During
installation, new message submissions receive a retryable HTTP 503 without
starting a turn; existing history remains readable. Automatic installs retry
once a minute after busy chats finish. After installation, chats can run again;
**Restart to update** rechecks for active turns before relaunching. Shutdown
closes the service and releases the database lease before the new process
starts. App updates never replace `~/.spool` or update the installed agent CLIs.

Checks and installations retry transient update-network errors at most twice,
after two and four seconds: connection failures, interrupted/truncated HTTP
downloads, and HTTP 408/429/500/502/503/504. An archive retry downloads it again
from the beginning and MyGo verifies it again; this is not resumable downloading.
Cancellation, missing assets, TLS trust errors, signature failures, disk errors
and extraction errors do not use this retry loop. The installation lease remains
held throughout. After a failed actual installation, the automatic poller does
not keep reinstalling the same version; retry manually or wait for a different
version. Busy-chat refusals still retry when idle. The Linux installer also uses
bounded downloader retries and never retries or bypasses a failed signature.

The Updates page shows the failure stage and a **Show update log** button.
`<data-dir>/update.log` records checks, retries, installation results, shutdown and
startup; it rotates at 256 KiB, retaining one previous file. Download/CDN URLs
are redacted, and no chat content or credentials are recorded. Unix log files
are created with mode 0600. Before a requested restart, `update-pending.json`
records the expected version; a subsequent native-window-ready startup confirms
that version and clears the pending record. An old version starting instead
reports an unconfirmed restart. Startup failures are logged when possible.
MyGo does not expose the child-launch result: if no process starts at all, the
record stays pending until a later launch, rather than falsely claiming success.

Run the update regression tests with:

```sh
node --test tools/*.test.mjs
go test -race ./internal/appupdate ./internal/workspace ./cmd/desktop
```
