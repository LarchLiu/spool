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
  settings, agent settings/catalogs, and hashed login sessions.

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
run. Knowing another user's chat ID never bypasses current membership checks.
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

An empty chat can choose any available agent. **The first accepted message fixes
that chat's agent**, including when the task subsequently fails or is interrupted.
Later turns can change models only within that agent, retaining the same native
resume context. Other agents are removed from that chat's menu; the server also
rejects forged cross-agent requests. Create another chat to use a different agent.
Agent/model changes are disabled while a turn is running.

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
# For a release on macOS, configure Developer ID signing/notarization in mygo.json.
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
