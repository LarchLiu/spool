# Spool: MyGo Desktop + Go API + Web/PWA

Only the Go project is affected. The Rust application is unchanged.
The application name is **Spool**. The source directory remains `go/`; the Go
module/package, commands and data directory use lowercase `spool`. The desktop
bundle identifier is `com.cloudgeek.spool`.

## Architecture

- `cmd/desktop`: one MyGo native window switching between WebView settings and
  full-window WebView chat, reusing the Web client and Go service, plus a native tray.
- `cmd/server`: optional headless HTTP server using the exact same services.
- `internal/agent/`: persistent CLI processes, Codex/Claude protocols, model and
  permission discovery, and process cleanup on supported platforms.
- `internal/model/`: shared conversation/attachment types, model and permission
  options, message limits, ID validation, and bounded UTF-8 text helpers.
- `internal/workspace/`: SQLite storage, users/projects/settings, authenticated
  HTTP/SSE/WebSocket APIs, presence, chat orchestration, and service lifecycle/leases.
- `internal/i18n/`: shared locale resolution and message translation.
- `internal/testutil/agentfixture/`: fake native CLI subprocesses shared by unit
  and integration tests; production entry points do not import this package.
- `web/`: React + TypeScript + Vite chat client, usable in a browser or as a PWA.
- `frontend/`: production Web assets embedded in **both** Go executables.
- `resources.go`: the root package's only Go source file, embedding frontend,
  locales and icons. Keeping embeds beside their resource directories avoids
  duplicate assets and unsupported parent-directory `go:embed` paths.
- `db/spool.sqlite` inside the data directory: users, projects, project membership,
  chat metadata, server settings, agent settings/catalogs, issue schedules/runs, task-chat assignees,
  previously recorded PR-link metadata, and hashed login sessions. New tables are added on startup
  without replacing existing projects or chats.

Both entry points use `internal/workspace`, which coordinates `internal/agent`
and `internal/model`. The CLI package does not import SQLite or HTTP handlers.
Tests live next to their implementation; cross-layer chat tests stay in
`internal/workspace`. Schema version 2 removes SQLite turn/attachment history in
favor of the CLIs' native sessions. Production uses `~/.spool`; development
uses `~/.spool-dev` so testing cannot migrate an installed app's database.
Both keep SQLite in `db/spool.sqlite` and preserve the CLI wire protocols,
adding a backward-paginated HTTP history endpoint.
Small SQLite sidecar tables retain turn actors and stop/approval audit events,
not prompts, responses or other transcript content. Native turn IDs (with an
offset fallback for interrupted requests) associate this metadata with history.

Signed-in users can click another person in the online list to open a private
chat, or choose **Online group chat** to talk to everyone currently connected.
Use **New group chat** to select specific online people (up to 32 members
including yourself), optionally name the group, and later **Invite people**
from its header. Selected-member groups are visible only to their members;
newly invited members receive no earlier messages. Group metadata stays only
in server memory while at least one member is connected and disappears when
everyone disconnects or the service restarts.
Each user can create up to 16 active selected-member groups across all their
connections (256 total per server). Groups still count while another member
keeps them alive; their slots are released when everyone disconnects.
Both share the authenticated `/api/events` WebSocket and show unread badges.
Private messages reach only the two users' devices; the online group broadcasts
to all signed-in chat connections, including away users. These messages
are not agent tasks: they never enter SQLite, native CLI sessions, or browser
storage. Each page keeps at most its latest 300 messages in memory; refreshing,
signing out, or closing the page clears them. The server only owns bounded live
delivery queues, with no history, offline delivery, or reconnect replay. Away
users can receive messages while their connection remains active. Sending is
limited to 2000 Unicode code points per message with a small per-connection
burst limit. Unsent drafts stay in page memory when switching or closing chat
windows and clear only after an acknowledged send. Closed groups with retained
messages or drafts remain in the conversation list for read-only access.
The movable, edge-resizable chat window keeps a narrow people/group list on
the left and the selected conversation on the right. Both private and group
messages identify each sender with their profile avatar, with names available
on hover and to screen readers rather than repeated above each message. The
list stays a compact 72px avatar-and-name rail at every window size. The compact
status/invite/close toolbar doubles as the drag handle, without a repeated
conversation title. Timestamps sit below and outside message bubbles, aligned
with the sender's side. Created groups show up to three overlapping member
avatars plus a `+N` remainder; the public online room keeps its generic icon.
Personal avatars in the list have a top-right presence dot (green online,
yellow away, gray offline or reconnecting).

There is no separate frontend service in production and no Node.js runtime
requirement. Installed and signed-in Codex/Claude CLIs are still required.
MyGo is pinned to **v0.6.4**, which requires **Go 1.27.3+**. Go's automatic
local toolchain selection downloads that version when necessary. Frontend builds
need Node.js 22.12+ and npm (DiceBear 10.x requires Node.js 22+).

## Development

From this directory (quit any existing **Spool Dev** instance first; closing
its window only hides it):

```sh
make dev
```

This prepares the embedded assets needed to compile Go, then runs the pinned
MyGo dev CLI. It starts Vite on `127.0.0.1:5173` (the port must be free), waits
for it to be ready, and opens **Spool Dev**. Both desktop chat and settings
support Vite hot module replacement / React Fast Refresh; editing Go code
automatically rebuilds and relaunches the desktop. A compile error keeps the
previous app running until the next successful build. Quit the app or press
Ctrl+C to stop the app and Vite together.

The desktop uses a private loopback proxy for frontend assets, the HMR WebSocket
and API calls. API requests always reach that desktop's active service, including
after changing its port in settings; no extra headless server or fixed API port
is required. The native settings bridge still accepts only the exact settings
document in its own WebView, never the chat page or an ordinary browser.
Production builds and release smoke probes always use embedded assets.

To launch once without hot reload:

```sh
npm --prefix web ci
npm --prefix web run build
go run ./cmd/desktop
```

For browser-only frontend hot reload, run the backend and Vite in separate terminals:

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

With already-built assets, `go run github.com/egoist/mygo/cmd/mygo@v0.6.4 dev`
starts the same desktop hot-reload workflow without the initial build. For a
manual two-terminal desktop session, run Vite and then
`MYGO_DEV_URL=http://127.0.0.1:5173 go run ./cmd/desktop`; this enables frontend
HMR only, not automatic Go rebuilds.

`go run .` is not an entry point: the root package only embeds resources. Use
`go run ./cmd/desktop` or `go run ./cmd/server`; both commands are unchanged.

### Development And Production Data

Development (`go run`, plain `go build`, or `mygo dev`) defaults to
`~/.spool-dev/db/spool.sqlite`. Installed MyGo builds and the production
executables from `make desktop` / `make server` default to
`~/.spool/db/spool.sqlite`. The development desktop is named **Spool Dev**,
with a separate single-instance lock and native preferences.

For a manual production build, use:

```sh
go build -ldflags "-X github.com/egoist/mygo.production=1" -o build/spool-desktop ./cmd/desktop
go build -ldflags "-X main.production=1" -o build/spool-server ./cmd/server
```

`-data-dir` takes precedence over `SPOOL_HOME`; either explicitly selects a
data root in both environments. For example,
`SPOOL_HOME=/path/to/test-data go run ./cmd/server` uses
`/path/to/test-data/db/spool.sqlite`. Do not point a
development build at production data unless you intend to upgrade that data.
Both services still default to port 8787; choose another port for development
if running them concurrently.

On the first launch after upgrading, an existing `<data-dir>/spool.sqlite`
is automatically relocated to `<data-dir>/db/spool.sqlite`. Stop all older
desktop/headless processes first (after a crash, allow up to 30 seconds for
the database lease to expire). Migration checkpoints any SQLite WAL before
moving the database, preserving committed writes. An active lease, busy
database, or databases at both locations stops startup rather than replacing
data. Development never imports or migrates `~/.spool` automatically.
All application logs are stored in `<data-dir>/log/`; update state such as
`update-pending.json` remains directly in the selected data root.

### Startup Errors And Diagnostics

An unreadable, incompatible, corrupt, locked or conflicting database does not
silently close the desktop. A native recovery window displays the exact error,
data/database paths and recovery guidance, with **Retry startup**, **Show data
directory**, **Show application log**, **Copy diagnostics** and **Quit** actions.
The Web service and agents are not started when opening the database fails.
Retry relaunches with the same arguments. The app never automatically deletes,
resets or downgrades a database to hide an error. Release smoke probes still
return a failure rather than waiting in an interactive recovery window.

Desktop and headless services initialize file logging before opening SQLite:

- `log/desktop.log` and `log/server.log`: startup metadata, service/agent errors,
  desktop action failures, HTTP server failures and standard Go log output.
- `log/desktop-crash.log` and `log/server-crash.log`: unhandled Go panics and
  fatal Go runtime reports, including stacks. A separate private crash-monitor
  process drains these reports even after the app's goroutines have stopped.
- `log/update.log`: signed-update checks, retries, installation/restart results
  and desktop startup/shutdown events. Existing root-level `update.log` and
  `update.log.1` are moved into `log/` without overwriting conflicting files.

Each application/crash log file is capped at **1 MiB**, with at most **three
rotated backups** (`.1` to `.3`); even a single oversized write is split and
rotated. The update log is capped at **256 KiB**, with **one backup**. Log
directories are private (0700) and files are private (0600) on Unix. Logs record
timestamps and process context, not request bodies or intentionally copied chat
transcripts; crash stacks and third-party errors can still contain sensitive
information, so inspect reports before sharing them. Download URLs in update
diagnostics are redacted.

If the selected data root is not writable, application/crash logging falls back
to the user cache (`spool/log/` or `spool-dev/log/`), then to a private temporary
directory containing `log/`. The recovery window shows the actual application
log path. If no directory can be written, stderr remains the last resort.
Go crash capture is best-effort: forced termination such as SIGKILL, power loss,
or a native/OS failure that cannot enter the Go runtime cannot reliably produce
an application crash report; consult the OS crash reports as well.

## First Desktop Setup

1. Use the bottom-left **Settings** action to open **User**, enter a username,
   nickname, and password, then save.
   Passwords must be 12–72 bytes and are stored only as cost-12 bcrypt hashes.
   Existing users can be renamed, have their password reset, or be disabled.
   Editing a user revokes their existing Web login sessions; disabled users and
   their chat metadata remain in the database and native session history is untouched.
2. Open **Projects**, choose an existing local directory, name the project, and
   check the people authorized to use it. Save the project.
   The native directory picker runs asynchronously, starts at the current path,
   and leaves that path unchanged when canceled. Only one picker opens at a time.
3. Open **Agents** to inspect Codex/Claude installation, versions, executable
   paths, and available models/levels. Blank executable paths use automatic
   discovery; a saved path overrides environment/PATH detection. Agents can be
   enabled/disabled. Saving an agent also triggers a fresh probe.
4. Open **Service** and choose the listen IP, external Web port, public origin,
   and idle release interval. Inputs save automatically on blur or Enter, and
   switches save immediately; there is no separate save button. History-cache
   changes apply immediately. Changed listener/origin/idle settings show a pending
   restart notice while the service is running. Choose **Restart service** to
   apply them; saving never restarts it automatically, because a restart interrupts
   active tasks. A stopped service uses the saved settings on its next start.
5. Use the back icon in the settings footer (or **Open Chat** in the tray)
   and sign in, or choose
   **Open in Browser** to use the Web/PWA client. With an authorized project and no chats yet, type
   directly in the composer, choose an agent/model, and send: the first message
   automatically creates a chat in the current project. Click **+** beside a
   project whenever you want another chat in that project. **+** only opens a
   local draft: no chat row is written to SQLite or added to history until Send.
   Switching away from an unsent draft or starting another discards that draft;
   empty drafts are not restored after a page reload.

### Desktop Chat

**Chat** fills the main desktop window, not a popup or a second window.
Normal launch starts the background service and opens Chat by default. The
auto-start preference remains respected: if disabled, or if starting the service
fails, settings stays available instead of forcing a service start or hiding the error.
It embeds the same React chat UI, HTTP/SSE APIs, native CLI sessions and database
as Web/PWA without an extra native navigation sidebar. Selecting **Chat** in the
control center starts the existing service if stopped; it never opens a second
database or agent manager. The **Settings** icon immediately left of **Sign out**
in the chat sidebar switches back to WebView settings in the same window.
Before sign-in, the same action is at the bottom left. Desktop chat follows the
control center's theme and language; it has no independent appearance dialog or
extra settings row. Browser/PWA preferences remain independent. Switching to settings
only hides the embedded WebView: returning to Chat keeps the same page and unsent
draft. Closing the main window hides it without stopping tasks or destroying the chat page. Dock
activation and a second launch restore that same window and selected tab.
The tray's **Settings** action selects WebView settings in the same
window. Explicit Quit shuts down the window and the service. Main-window bounds
are remembered by MyGo.

The embedded chat page loads the active local HTTP listener, ignoring a configured
public reverse-proxy URL and listener settings not yet applied by Stop/Start.
Wildcard bindings use loopback of the matching IPv4/IPv6 family. For a specific
non-loopback binding, the same HTTP server also listens on an ephemeral loopback
port so desktop pages retain a secure context; both listeners stop together.
Remote browser access still uses the configured Web URL. Authentication,
membership checks, per-chat controls and CSRF validation are unchanged; there is
no automatic desktop login. When an HTTPS public origin is configured, only
same-origin loopback requests from a loopback peer use non-Secure local HTTP cookies;
public/proxied requests retain Secure cookies.

External HTTP(S) links open in the default browser instead of navigating the chat
window. Other external schemes and unsolicited off-origin navigations are
blocked. The desktop preload only marks presentation mode; it grants no extra
native bindings or API privileges. The chat uses MyGo's existing native locale
events and theme-aware media query to follow settings without reloading or
writing Web-account preferences. Desktop skips PWA installation and Service
Worker registration because its Web assets update with the application. Web/PWA
behavior is unchanged, including its independent login and appearance settings.

Desktop chat uses WKWebView on macOS, WebView2 on Windows, and WebKitGTK on Linux.
If the runtime is missing, the native control center stays usable and shows
installation guidance; **Open in Browser** remains available. A failed page or
renderer reports an error in the control center; **Open Chat** retries loading.

### Desktop Settings

The full control center uses a separate embedded WebView with a compact sidebar
and grouped setting rows: **Service**, **User**, **Projects**, **Agents**,
**Online**, **Appearance**, and **About**. Server start/stop, listener and runtime
configuration, account management, project membership and scheduled GitHub issues,
CLI discovery/configuration, online presence, diagnostics and signed updates all
remain available. Form drafts survive section switches and background status polls.
**User**, **Projects**, and **Agents** open with overview lists. Each row shows
its name, identifying details and status; select a row to open its existing editor
and use **Back to list** to return. **New user** and **New project** open a blank
editor. Saving keeps the editor open and updates the existing item rather than
creating duplicates.
The global **GitHub CLI** path and installation/sign-in checks live below the
project overview list, not in individual project editors or a separate sidebar tab.
The executable path is collapsed by default; **Path settings** at the right of the
status row reveals the native file picker and save action.
User and Online show the same user avatars as chat; Agents show each CLI's own
icon.
Folder/executable selection, revealing local files, window/tray management, and
updater restart continue to use native APIs.

Settings assets load from a private application scheme, not the HTTP service, so
the settings page works while the server is stopped. The small `DesktopSettings`
bridge checks both the calling embedded page's identity and its exact private
document URL on every call. Chat and ordinary browser pages cannot administer the
workspace, including local chat pages in development builds. No HTTP management
API is added. Settings disallows remote network content, scripts and frames via
CSP, and blocks navigation away from its private document. Theme and language use
the same desktop preference record and update both embedded views live. A native
control-center fallback remains available if the system WebView cannot load.
Chat and settings share the primary action palette in `web/src/style.css`
(`--color-primary`, `--color-primary-hover`, `--color-on-primary` and
`--color-primary-focus`), based on the chat Send button in light and dark themes.
Primary button backgrounds, switches, checkboxes and progress indicators use those
same tokens; ordinary button text stays neutral. Settings' main and sidebar
scrollbars share the chat scrollbar rule.
Icon-only controls use circular hover, pressed and focus outlines. The settings
sidebar footer shares chat's height, with a back-to-chat icon on the left and
service status on the right; the version remains in **About**. Available updates
show a primary-color dot on the chat settings icon and next to **About**. The
About page ends with a GitHub repository icon linking to the project. Browser
settings show the running server version in the appearance dialog footer,
including before sign-in.

When an update is available, **About > View release notes** lazily loads GitHub
Releases newer than the installed version and up to and including the available
version. Each published version (including prereleases in that range) has its own
Markdown description, date and GitHub link, sorted newest first by semantic
version. The native read-only bridge fetches all API pages with bounded response
sizes and a 30-second deadline. Collapsing/reopening retains the loaded history;
refresh and retry fetch it again. Raw HTML and unsafe URLs are disabled, and
remote images become links instead of loading remote content in settings. These
descriptions are display-only and never replace authenticated updater metadata.

### Scheduled GitHub Issues

In **Projects**, enable **Scheduled GitHub issues** and choose the
interval (1–10080 minutes), editable issue labels, Codex or Claude, and one or more
responsible project members. All assignees can see and control task chats:
send messages/files, stop responses and answer permission requests, even when
project-wide chat sharing is disabled. Responsibility changes also update
existing task chats; removing membership or disabling an account immediately
revokes its controls. Regular chats remain owner-only, including shared ones.
Scheduled turns are attributed to **Spool Bot** and use the Spool logo. Manual
follow-ups and stop/approval actions retain the actual signed-in user's identity.
User messages show the sender's avatar on the right; chat-list avatar stacks show
the people (and bot) who actually operated the chat, not every authorized assignee.
Turns from before actor tracking was introduced belong to the chat's creator.
Human follow-ups atomically reserve the same issue slots so scheduled recoveries
cannot overlap them, while unrelated labels continue when labels are configured. Model, reasoning level and native permission mode
are optional. Configuration is per project; no repository/path is hardcoded.
The GitHub `owner/repo` is read automatically from the project's Git `origin`
(HTTPS or SSH); it is not entered manually. The project must point at the root
of a Git checkout with a GitHub.com `origin`. Without this Git information,
automation cannot be enabled; the backend also checks it when saving. Install
and authenticate `gh` on the service host, and ensure the repository has a `dev`
branch.

If the GitHub CLI check fails, enter a custom absolute executable path in **Projects →
Scheduled GitHub issues → GitHub CLI executable**, then select **Save GitHub CLI
path**. For example, use `/opt/homebrew/bin/gh` on macOS or
`C:\Program Files\GitHub CLI\gh.exe` on Windows, without adding quotes. This
shared setting is saved in the selected data directory's SQLite database and
applies immediately to installation checks and scheduled runs (including the
headless service using the same data directory). Saving triggers a fresh check.
The path field and save button stay hidden while checking and after successful
installation/authentication verification. They reappear if a later check fails.
The executable path is also included in scheduled task and manual continuation
instructions so the agent can use it outside the application's `PATH`.
An invalid path is rejected without changing the saved setting. A configured
executable never silently falls back to another installation; clear the field
and save to restore auto-detection.

At desktop launch, a background check verifies that `gh` can run and make an
authenticated GitHub.com API request. New issue automation cannot be enabled
until this check succeeds. The project panel shows installation, sign-in or
network guidance and a **Recheck GitHub CLI** button; existing schedules remain
editable and can still be disabled. GitHub CLI discovery checks the application's
`PATH` first, then common platform-specific installation locations: Homebrew,
MacPorts and `~/.local/bin` on macOS; system directories, Linuxbrew, Snap and Nix
profiles on Linux; and GitHub CLI's default directories, WinGet links, Scoop and
Chocolatey on Windows. This also applies to scheduled runs, so desktop launches
do not require inheriting a terminal's `PATH`. Nonstandard installations can use
the saved executable path; discovery does not run shell startup files
or change the process environment. The repository is detected in the background
when the project directory changes, with a **Recheck project Git** button for
local Git configuration changes. Existing schedules can still be disabled if
Git information is missing. Each scheduled run verifies the current origin
against the repository detected at save time before accessing GitHub.

The Go scheduler runs in both desktop and headless services while the Web
service is running (checks admitted within about 15 seconds of their due time).
It does not wake a previous chat or replay missed intervals. Stopping the
service cancels both preflight and accepted agent work and waits for actual
cleanup and progress import before releasing label reservations. Disabling/archiving a project or
changing its schedule affects future runs; it does not discard accepted work.
An update cannot start while a scheduled preflight or agent turn is running.

The Go backend, not the agent, performs admission (requirements 1–3):

- Inspect Git state and worktrees, locate the **main checkout**, and acquire
  `<data-dir>/projects/<encoded-working-directory>/issues/.issues-automation.lock`.
  Its owner includes automation ID `issues`,
  run ID, chat ID, service instance, host and PID. This is a state transaction
  lock, not an execution-wide project lock. An advisory guard also serializes
  stale-lock reclamation. Only proven-ended owners can be reclaimed;
  age alone never expires a claim. Unknown/external owners are left alone.
- Fully paginate REST issues (all states, excluding PR entries), timelines and
  comments, query paginated Development connection events, and verify actual
  linked PR state with `gh pr view --json`. Previously recorded branches are
  also checked for PRs, recovering a PR created just before a crash.
- Preserve and synchronize the project's **only** shared local ledger,
  `<data-dir>/projects/<encoded-working-directory>/issues/issues.json`.
  Status is `close` only for a closed issue, `pr` for an open
  linked PR (drafts included), `processing` for an unfinished claim, otherwise
  `open`. Merged PRs do not close still-open issues locally.
- Resume an unfinished `issues` claim only after proving its previous run has
  ended. Labels are optional and empty by default: without configured labels,
  ignore issue labels and run at most one task per project. With configured
  labels, each label can run one task concurrently, choosing the
  lowest-numbered eligible issue and prioritizing ended unfinished work within
  that label. Labels are freely editable and have no fixed count;
  matching is exact and case-sensitive.
  Spaces, punctuation and Unicode names are preserved. A multi-label issue
  reserves every matching configured label atomically. A live executor keeps its
  reservation across timer checks, even if its issue has gained a PR or closed.
  Unrelated labels continue; when filtering, no matching label means synchronization only.
  Changing labels affects future admission, never interrupts accepted work.
  Clearing labels waits for existing tasks to end before admitting a global task;
  a running global task also blocks newly configured label slots until it ends.
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

The agent records full PR URLs in the claimed JSON record's `prs` array and
includes links in its reply. The Go backend independently verifies linked and
recorded PRs and checks the recorded branch for PRs, recovering creation that
succeeded just before a crash. Issue automation does not attach PRs to chats or
write PR attachment metadata to SQLite. There is no attachment callback, shell
function or temporary credential in the agent prompt; PR synchronization does
not depend on an attachment API.

State is outside the source repository, under
`<data-dir>/projects/<encoded-working-directory>/issues/`. The encoded directory
uses the full canonical main-checkout path, replacing each non-letter/non-number
character with `-` (without collapsing or trimming). For example,
`/Users/alex/Work/muyun/test` becomes `-Users-alex-Work-muyun-test`. Linked
worktrees share their main checkout's state. A `.project-directory` identity file
in the encoded project folder rejects path-encoding collisions instead of mixing
unrelated projects. The ledger, `.issues-automation.guard`, and active
`.issues-automation.lock/owner.json` all live in the same `issues/` directory.
Each agent writes only `runs/<run-id>/issues.json`, a single-record task document,
with additional write access scoped to that run directory, not the shared state
or entire data root. Go validates the issue number, owner and field types, then
merges progress under the short state lock after actual executor cleanup. It
also imports ended task files on crash recovery. Malformed task JSON remains in
its run directory for diagnosis and does not corrupt the shared ledger or block
unrelated labels. Execution guards/locks live in the run directory; the project
lock is held only during state transactions. Use separate issue worktrees for
all concurrent code changes, including changes in the same source file; any
integration conflicts are resolved during review, never by sharing a checkout.
Do not copy or commit automation state into the repo.

The ledger is a standalone JSON object in `issues.json`, with `version: 1`
and a `records` array keyed logically by each record's `number`. Notes, owner,
branch/worktree, validation, PR history and unknown JSON fields are preserved.
The `notes` and `validation` fields are strings (multiple results use multiline
text); keep these optional fields to short validation and recovery summaries,
not full execution logs. `history`, `prs`, `files` and `categories` are arrays of strings.
Writes are atomic and reject concurrent changes, malformed data, duplicate issue
numbers and unsupported versions rather than overwriting them.

Only Go reads and writes the shared data-directory `issues.json`; agents write
their isolated single-record task documents. If it does not exist,
synchronization starts with an empty ledger and creates it after the required
GitHub reads succeed. Repo-local `docs/issues.json` copies are not used; move an
existing JSON ledger into the data directory while the old service is stopped.
Old `docs/issues.md` files, Markdown tables, embedded JSON blocks and
their contents are not read, imported, copied or updated. Existing local claims
that must remain occupied need to be recorded explicitly in JSON before enabling
the schedule.

```json
{
  "version": 1,
  "records": []
}
```

The ledger is local automation state: do not commit it to `dev`
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
  -issues-interval 15 -issues-agent codex -issues-assignees alex,bob \
  -issues-label web -issues-label server -issues-label "priority / urgent"

go run ./cmd/server -issues-project PROJECT_ID -issues-enabled=false
```

`-issues-label` can be repeated any number of times; commas inside names are
literal, not separators. Omit it to ignore issue labels and process one issue at
a time per project. The desktop Projects settings have editable label rows and
Add/Remove buttons; remove all rows for the same unfiltered, single-task mode.
Missing or empty label settings both use this default; saved nonempty lists
continue to filter by those labels.

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

Type `/` in the chat composer to open the command menu; **Tab** or **Enter**
completes `/goal`, and **Escape** dismisses the menu. Send `/goal <condition>`
to set a native goal and start work with the selected agent. Send `/goal` to
view its state, or `/goal clear` to remove it; these controls do not create
ordinary chat turns. Goal commands do not accept attachments. Codex uses its
native `thread/goal/*` API; Claude Code uses its built-in `/goal` command.
The installed CLI must support native goals. Spool does not simulate goal
state or implement a separate continuation loop; native limits and permissions
still apply. Viewing or clearing a goal requires the chat's current agent and
an idle conversation (stop the current response first if needed).

User messages and streamed agent replies render Markdown, including headings,
lists, quotes, fenced code, links, GFM tables, strikethrough and task lists.
Raw HTML is disabled and unsafe link protocols are filtered. Markdown images
are shown as links instead of fetching external images automatically; uploaded
image attachments retain their existing authenticated preview.

Message footers show the user's send time followed by a copy button on the right,
and the agent's copy button followed by its completion time on the left. Both
show the date and time in the viewer's local timezone, with seconds on hover. Copying
preserves the original Markdown, excluding tool activity and reasoning from agent
replies. Attachment-only messages have no copyable text; replies still streaming
show their completion time once the turn finishes.

The project file viewer offers **Source** and **Preview** modes for Markdown
and HTML (`.html` / `.htm`, or text detected as `text/html`). HTML previews render
in a sandboxed frame with inline styles and embedded data images; scripts,
forms, nested frames and external or project-relative resources are disabled.
Previews use the same bounded text content as source view, and show a notice when
the file is truncated. Download the original to view files that need scripts or
separate assets.

The composer's left toolbar contains a single **+** for both files and images,
followed by the native agent's permission selector. Agent/model selection and
Send remain on the right. Attached images appear as thumbnails above the text;
click to open a full-size preview. File cards show the name and a small text/code
excerpt where possible. Remove an attachment before sending with its **x**.
Paste images or files directly into the message input with **Cmd+V** / **Ctrl+V**
when the browser exposes them as clipboard files; they use the same previews,
removal controls and upload limits as the **+** picker. Ordinary text pastes stay
as text. Pasted paths, URLs and HTML do not trigger file reads or remote downloads.
Images embedded in native history can also be previewed. Uploaded originals are
downloadable only while their temporary copies exist in the current process.
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
- Attachment bytes and metadata are not stored in SQLite. Native CLIs receive
  private temporary copies of the current request's files, removed when the
  service stops. Historical originals are not rematerialized on later turns.
- Codex receives native `localImage` input; Claude receives native base64 image
  blocks. Both get file paths for regular attachments. File contents are user
  data, not system/developer instructions.
- Downloads and previews require current project access. Uploads, approvals,
  and chat controls require session control permission; writes also require same-origin
  and CSRF validation. Attachment responses are never cached by the PWA.

Permission choices are discovered by the Go service in the project's working
directory and checked again on submission. Unavailable choices show a policy or
CLI capability reason. SQLite stores only the chat's current permission
preference; reopening a chat restores it. Changing modes between turns reuses
the live CLI.

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

All entry points support `-data-dir /path`, or `SPOOL_HOME`. Production defaults
to `~/.spool`; development defaults to `~/.spool-dev`, including the `go run`
bootstrap commands above. To administer production data, use the production
server binary, or explicitly select that root with `SPOOL_HOME` / `-data-dir`.
`SPOOL_CODEX_BIN` and `SPOOL_CLAUDE_BIN`
are fallback executable overrides when no explicit agent path is saved.
The SQLite file is `db/spool.sqlite` inside that directory. The application only
uses the requested directory; it does not automatically discover or import data
from a differently named directory. The previous `~/.tern` directory is left
untouched; it is not read by Spool. An existing SQLite store can be relocated
while all service/desktop processes are stopped, preserving the database and
renaming its file to `db/spool.sqlite` (and any matching `-wal`/`-shm` sidecars
in the same `db/` directory).
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
the desktop Projects panel can enable **Share in-app sessions**
for an individual project. Members can read shared history and streams. Each
project member has two independent segmented-button groups in that project's
member list: **In-app session permissions: Read-only / Can create**, and **Shared session permissions:
Read-only / Can control sessions**. Defaults preserve personal creation and shared
control for existing/new members. Permissions are independent per project; the
global People editor has no session permission setting. Personal read-only allows
viewing existing personal chats, but blocks creating chats and operating those
chats, even when shared-chat control is allowed. Shared-chat read-only limits
other people's shared chats and shared local sessions without changing personal
permissions. Explicit task-assignee permissions are unchanged. Creating a new
chat through agent conversion also requires personal creation permission.
Without project-wide sharing, only owners and configured task assignees can
access their private chats, subject to their respective operation permissions.
Knowing another user's chat ID never bypasses current membership/access checks.

Under project sharing, **Share local sessions** separately opts into discovering
non-Spool Codex/Claude chats from the configured provider homes. It shares at
most the latest **20 local sessions per project**, ordered by native file activity,
refreshing about every 10 seconds. Matching includes project subdirectories and
linked Git worktrees, but not a separately configured private nested project.
All sessions initiated by Spool, including issue automation, are excluded from
the local window and do not consume its 20 slots. A durable provider-ID ledger
keeps them excluded even after their Spool chat adopts a different native ID.
Archived sessions and subagent sessions are also excluded. A monitor **Local session** icon distinguishes
these chats in project lists and Recents. Only metadata is indexed in SQLite;
history remains in the provider files and resumes in its original working
directory. Each scan checks file size, nanosecond modification time and file
identity; a changed generation advances the chat revision even if its timestamp
does not increase. Lists update over WebSocket, and an open chat refreshes native
history over SSE. New/deleted sessions rotate the latest-20 window while public
chat IDs stay stable. Synchronization runs while the desktop service is running;
it is eventual discovery, not instantaneous file mirroring. Disabling either sharing switch immediately blocks local chat access.
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
advertised effort support have no adjustable slider. Selections apply to the next submitted turn and are saved as the chat's current preferences; historical choices come from native sessions. Known unsupported combinations are rejected before history/task creation.
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
different agent only changes the unsent selection: no chat or native session is
created until Send, so accidental selections can be changed back freely.
Sending converts the current native session and creates an **independent Web
chat** with a new ID. The original chat and native
session remain unchanged and can both be continued. Each further agent switch
creates another independent chat from the current context; it never reopens an
older pre-switch session. Same-agent model changes reuse the live CLI. Changes
are disabled during a running turn; only users who can control a chat can switch.
The message API returns the new chat after Send. The explicit
`POST /api/chats/:id/fork` endpoint can create a converted chat without submitting
a model prompt, but the model picker never calls it.
Task chats reject cross-agent conversion because their issue claim belongs to
the original chat. Continue in that chat; same-agent model changes remain supported.
Conversion follows Magpie's native Claude/Codex carry-over format (MIT notice in
`internal/agent/MAGPIE-LICENSE`). User and
assistant messages, embedded user images, and tool results are retained. Shell
calls translate to Claude's `Bash` or Codex's `exec_command`; other tools become
historical assistant notes rather than calls to tools the target does not have.
The history viewer restores these converted notes to collapsible tool activities,
including their input and result, without rewriting the native context.
Claude's active parent chain and native compaction boundaries are respected.
Provider instructions and signed/encrypted reasoning are not transferred. Target
permission modes are discovered independently and default to that agent's normal
mode; permissions from the source agent are not inherited.

Source files remain read-only. Conversion requires available native history; if
it cannot be read or converted, Send fails without changing the chat. A failed
SQLite transaction removes the newly converted file and rolls back all metadata.
The new native session contains the converted history itself. SQLite records no
source session, parent chat, turn boundary or imported-prefix offset. New history
is read directly from the target's file, including its imported context. As in
Magpie, the imported context is not a lossless archive: original reasoning and
historical model attribution are not copied. They remain in the original chat.
The target CLIs must be installed and signed in as usual.
Like Magpie's converter, conversion to Claude rejects project paths requiring
its hashed long-path naming (over 200 encoded characters), rather than writing
a session that Claude cannot resume.

### Magpie-Compatible Session Transcripts

Every chat exposes one ordered `transcript` with Magpie's JSON structure:
`parts` plus optional `cut`, `source`, and `captured`; each part contains
`role`, `kind`, `text`, and optional `name`, `cut`, `model`, and `image_url`. The Go Codex
app-server and Claude stream-json readers normalize their native events into
those shared part kinds (`text`, `thinking`, `tool_use`, `tool_result`,
`context`, and `image`) as they arrive. Adding an agent therefore requires an
adapter from its native events to the same part type, not a new Web/API shape.
The public session's `turns` are derived from native records (or the active
in-memory request) and point into this page's transcript by part ranges.

`internal/agent.ParseSessionTranscript` also reads native Codex rollout and
Claude Code JSONL files without modifying them. It applies the same filtering
as Magpie (drops provider metadata/developer wrappers, preserves user/tool
parts, records assistant models, and omits Claude's synthetic model labels). The
parser tests use the checked-in fixtures under
`internal/agent/testdata/`; they do not require another checkout at test time.
The bundled tool fixtures cover Codex `function_call`, `custom_tool_call`,
reasoning, and paginated results, plus Claude `Bash`, `Write`, `Read`, and
`Edit` records. Claude structured patches are converted to the shared diff
text used by the Web viewer.

SQLite deliberately stores **no turn rows, attachment metadata/BLOBs, or
transcripts**. Session rows keep chat-level configuration, status, and the
provider's native session ID/path. Schema version 2 migrates existing databases
by retaining the last permission preference on the chat and removing the old
`turns`, `attachments`, and `session_history` tables. No native session is changed.

History uses an in-memory native turn index: Codex's task/turn IDs and Claude's
prompt IDs, UUID parent chains and genuine user boundaries. Opening a chat parses
and caches its entire native history. HTTP/SSE responses page that cached snapshot
backwards in file order using a 200,000-byte soft content target (text and embedded
image URLs). A page includes the entire turn that crosses the target, so a single
turn can exceed 200 KB or 400 KB. There is no turn-count, part-size or single-turn
content limit, and live output is retained in full without truncation.
The browser paints the latest page first, opens at the bottom, and automatically
fetches earlier pages in the background. Prepending history preserves the current
viewport; REST refreshes and SSE updates do not discard already loaded pages.
Switching chats aborts pending history requests. Failed background loads can be retried.
Live request IDs, timers, approvals and streamed content are process-local;
restart recovery marks only chat-level running status as interrupted. Native
files supply historical timing and models.

The Go service shares native indexes and complete decoded histories across
viewers. Cache hits still check file size, modification time and file identity;
an append, edit, replacement or deletion invalidates that file's entire previous
cache. Reads validate the source again after parsing and retry if it changed,
so an older in-flight parse cannot overwrite newer cached data or return stale content.
Concurrent readers of the same file reuse the first reader's complete parse, while
unrelated files can parse in parallel. Responses use copies of cached parts;
chat IDs, permissions and live state are never shared through the cache.

The process-local cache retains at most 64 native files, using least-recently-used
eviction of whole native sessions. Its shared decoded-content target defaults to
64 MiB. Customize **History cache size (MiB)** in the desktop Service settings;
any positive whole number within the platform's integer range is accepted, saved
in SQLite as `history_cache_mib`, and applied immediately without restarting.
Reducing the target immediately evicts least-recently-used sessions as needed.
For headless startup, pass `--history-cache-mib 128` to `cmd/server` to persist
the same setting; later starts without that flag reuse the saved value.
A single session larger than that target is cached in full after evicting other
sessions; this is a cache target, not a hard process-memory or history-size limit.
Eviction never keeps a partial history or truncates content. Evicted
sessions can be reparsed from their native sources. Shutdown
clears the cache; the next process rebuilds it on demand. No Redis, cache files or
SQLite history rows are required, and native sessions remain the sole source of truth.

Uploads are private temporary files for the current process, removed on shutdown,
not a durable download archive. They are not recreated for later requests or after
restart. Images embedded in native sessions remain previewable directly from
those records. Local paths and remote image URLs are never fetched by the viewer.

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

Authenticated pages maintain one global WebSocket at `/api/events`, with an
exact-origin handshake, the existing login cookie, and a CSRF-authenticated hello
(the token is never put in the URL). Navigation, user profiles, agent metadata, and online
presence are sent as snapshots; subsequent changes are published after successful
database writes rather than polled. Each navigation snapshot is filtered using
the connection owner's current permissions; revoked sessions are disconnected.

Chat HTTP/SSE/navigation payloads carry operator IDs only; display names and
avatar configurations live in a shared client profile cache. One batched
`GET /api/profiles` loads the signed-in user, operators in currently authorized
chats, and online identities. The WebSocket sends scoped `profiles` snapshots
before navigation/presence updates, including after reconnects and permission
changes. Durable profile revisions and HTTP request fencing keep delayed replies
or old chat SSE frames from reverting newer avatars/names. Missing identities
use an ID placeholder until the batch lookup completes; avatar rendering still
shares the bounded SVG cache rather than fetching an image for each component.

Presence is kept in memory and deduplicated by account, aggregating tab/device
connections. A visible page is online and an account with only background pages
is away. Visibility changes and normal disconnects update the list immediately;
closing one tab does not mark other connections offline. Application-level
ping/pong runs every 15 seconds, with a 45-second timeout for unresponsive clients
or half-open connections. Mobile OS suspension can therefore mark a suspended
page offline; agent task state is independent of a person's presence.

The client reconnects with capped exponential backoff and jitter, and receives
fresh authorized snapshots to recover changes missed while disconnected. Focus,
visibility restoration, and network restoration resynchronize the connection.
Navigation retains a 15-second HTTP fallback only while WebSocket is unavailable;
the UI clears stale online indicators instead of claiming disconnected users are
still online. Existing HTTP heartbeat/presence SSE endpoints remain compatible
with older clients, whose connections retain the legacy 90-second expiry.

## Navigation, Appearance, And Languages

The Web sidebar groups chats beneath collapsible projects, with compact nested
rows, running-task indicators, per-project **Show more**, and a cross-project
**Recents** section. Each project row has its own **+** to create a chat; there is
no global New Chat/agent selector above the sidebar. Clicking a project name only
expands/collapses its chat list, without changing the active chat or creating one.
Clicking a recent chat also
selects and expands its project, revealing older selected chats when necessary.
Private chats and, when enabled, shared project chats appear only for currently
authorized projects. Sidebar metadata and participant avatars update over the
global WebSocket, including chats that are not open. The active chat continues
streaming over SSE; native transcript parsing and pagination are unchanged.

The composer starts at one visible line, grows with explicit line breaks and
wrapped text up to six visible lines, then scrolls internally. It shrinks again
when text is removed or sent, and recalculates when its width changes.

Long user messages initially show up to eight lines, with **Show more** below
the text to expand and **Show less** to collapse it again. The preview adapts to
the available width without truncating Markdown or changing the copied text.

Both Web and desktop support **Light**, **Dark**, and **Follow system**, plus
**English**, **简体中文**, **日本語**, and a system-language option. Theme and language
changes apply without restarting the service or agent processes. The Web uses the
device's `prefers-color-scheme` and ordered browser languages. MyGo uses its native
appearance and locale APIs, including title bars, native controls and tray labels.

In Web, open the appearance icon beside your name in the sidebar footer. In the
desktop chat, the icon beside **Sign out** opens WebView settings; open
**Appearance** there to change theme or language for both settings and
embedded chat, including its sign-in screen. Native locale changes and the
effective light/dark appearance (including **Follow system**) update chat live
without losing drafts. Preferences are stored in SQLite: one local desktop
record shared by settings, native chrome and embedded chat, and an independent record for
each browser/PWA user. Web user
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
certificates, allow WebSocket upgrades for `/api/events`, and avoid proxy buffering
or short SSE/WebSocket timeouts. Once HTTPS public
origin is configured, use that HTTPS URL to log in: Secure cookies intentionally
will not work over an unrelated plain HTTP LAN address. iOS installation uses
Safari's Share → Add to Home Screen; not every browser emits an install prompt.
TLS certificate provisioning and reverse-proxy setup are not automated here.

### Desktop System Notifications

In the desktop Control Center, open **Appearance and language**, then enable
**System notifications**. This is a separate, local-device opt-in, disabled by
default. Enabling sends a test notification and asks for OS permission when
needed; permission errors do not save the opt-in. Use **Send test notification**
to check again. macOS requires the packaged app (`mygo dev` or `mygo build`), not
a bare `go run` executable; other platforms require their native notification
service. The OS may suppress alerts through Focus/Do Not Disturb settings.

Desktop alerts use MyGo's native notification API, not Web Push, Firebase, or
browser notification permissions. They cover task success, failure, pending
approval, and successfully delivered private/group messages, using the same
localized titles, sender/group names and content-privacy rules as Web Push.
Spool and its local service must stay running; closing the main window to the
tray is supported, quitting the app stops new alerts. No Web Push subscription
is required. Human chats remain live-only with no offline history.

Automatic alerts are suppressed while the native desktop window has focus,
including when Control Center is open. Foreground events are discarded, not
queued for replay after switching away. Alerts resume for new events when the
window loses focus, is minimized, or is hidden to the tray. Manual test
notifications bypass this focus rule so OS permissions can be checked.

Only the account signed in to the desktop Chat receives alerts. The native
host reads its own HTTPOnly cookie and rechecks session validity, current chat
access/control, task participation and actual message recipients immediately
before displaying an event. It does not grant native privileges to the chat
renderer. Changing accounts or signing out clears prior alerts on the next
desktop refresh (normally within three seconds); disabling notifications clears
them immediately. Already-displayed or in-flight OS notifications are best-effort.

Clicking an alert restores the main window and opens the relevant conversation
without reloading an existing chat page. Targets are local and account-scoped;
old notifications cannot switch accounts or bypass login. On platforms that
relaunch the app for an old notification (macOS), its target is retained until
the chat page and matching login are ready. Delivery is bounded and in-memory;
events expire after five minutes and are not replayed after quitting/restarting.

### Web Push Notifications

In the signed-in Web/PWA sidebar settings (**Appearance and language**), use
**Enable notifications**, grant the browser permission, then **Send test
notification**. Each device opts in separately. On iOS/iPadOS 16.4 or newer,
first add Spool to the Home Screen and enable notifications from that installed
app. HTTPS is required outside localhost. Desktop-native chat does not register
a Service Worker or offer this Web-only control.

Spool uses standard encrypted Web Push, not a Firebase SDK or Firebase project.
A VAPID key pair is generated automatically on first configuration and persisted
in private SQLite storage, outside management/settings API responses. Back up
the database to preserve it; replacing the keys requires devices to enable
notifications again. The public HTTPS origin is the VAPID contact URI; local
development falls back to the Spool repository URL.

Notifications have distinct titles for task success, failure, and required
approval. They cover those agent-turn events even when the page is closed,
subject to browser/OS delivery policies. Manual stops do not notify.
Recipients are the chat owner, explicit collaborators/assignees,
and the user who submitted that turn, with current project/chat access checked
again before delivery. Approval notifications additionally require control
permission. Project-wide chat sharing alone does not notify every member.
Bodies contain only a translated generic status, never prompts, replies,
project names, chat titles, or approval command details. Clicking a notification
opens its chat after normal authentication/authorization; an existing app
window is focused without reloading it.

The same device opt-in also covers successfully delivered private and group
messages. The sender never receives a notification for their own message.
Private notifications go only to the recipient's subscribed devices; selected
groups notify only members whose live connection accepted the message, and the
online room snapshots its actual recipients at send time. Multiple sockets for
one person do not duplicate notifications. Later invitees/joiners do not receive
earlier notifications, and selected-group membership, enabled accounts and login
sessions are checked again before sending. Notifications for the same people
conversation replace one another instead of accumulating on the lock screen.
Private notification titles identify the sender by display name. Group titles
identify the group (including the online room), and the body identifies the
sender. These names can appear on the lock screen; message contents are never
included. Titles and generic text follow the receiving device's language;
user-chosen names stay unchanged apart from single-line formatting and length
limits. Clicking opens that private/group conversation in an existing page
without a reload; the link is scoped to the recipient's account.

This does **not** add offline messaging or history storage. Private sends to a
fully disconnected recipient still fail; private/group message content remains
only in connected pages' memory. A subscribed phone can be alerted while another
device receives the live message, but opening a fresh page cannot recover that
message. Closed temporary groups may no longer be available. Use the already-open
page to read its retained messages. These limits are also stated in the
notification settings.

Subscriptions are tied to the current login session. Logout, session expiry,
account edits that revoke sessions, and account disabling stop future sends.
After signing in again, enable notifications again; existing permission does
not trigger another browser prompt. **Disable notifications** affects only this
device/session. Normal page loads only refresh an already enabled subscription's
language and never request permission or revive disabled notifications. Logging
out also attempts to unsubscribe the browser and close displayed notifications;
already accepted/in-flight provider messages cannot be recalled.

The server makes direct outbound HTTPS requests to browser-owned push providers
(FCM, Mozilla, Apple, or Windows); no inbound push callback is needed. Endpoints
are restricted to those providers, redirects and private-IP destinations are
blocked, and environment HTTP proxies are deliberately not used for push.
Both the server and device need access to their push provider; Chrome's FCM may
be unreliable on mainland China networks even without a Firebase SDK.
Delivery is best-effort: a bounded in-memory background queue keeps provider
latency out of agent execution, pending messages expire after five minutes,
and shutdown discards unsent jobs. Invalid subscriptions returning 404/410 are
removed. There is no durable retry queue or guaranteed delivery, so important
approvals remain visible in the app. The test button reports provider acceptance,
not confirmation that an OS displayed the notification, and is limited to one
test per device every 30 seconds.

## SQLite Storage

The data root and `db/` directory are private (0700); `db/spool.sqlite` is
created private (0600).
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

Schema version 2 migrates version 1 stores to native-session-only history,
removing legacy turn and attachment tables while retaining chat metadata and
the chat's own native session reference. Back up existing stores before an
upgrade; unsupported explicit schema versions are rejected.

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

`build/go.mod` is a module boundary, so root-level `go test ./...` and
`go vet ./...` skip build outputs, caches, and diagnostic Go files under
`build/`. Keep this tracked file in place; all other build contents remain
ignored by Git.

Package the desktop application with the pinned MyGo CLI:

```sh
go run github.com/egoist/mygo/cmd/mygo@v0.6.4 build -skip-dmg
# Release builds do not use Developer ID signing or notarization.
# Cross-build examples:
# go run github.com/egoist/mygo/cmd/mygo@v0.6.4 build -platform windows/amd64
# go run github.com/egoist/mygo/cmd/mygo@v0.6.4 build -platform linux/amd64
```

`mygo.json` points at `cmd/desktop`, builds Web assets before packaging, and enables
Linux WebView dependencies (GTK and WebKitGTK). Windows installer generation
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

# Equivalent Make entry point (defaults to patch):
# make release
# make release version=0.2.0

# Review, then push the current branch and release tag together.
git show --stat v0.2.0
git push --atomic origin HEAD v0.2.0
```

Omitting the version or passing an empty string defaults to `patch`:
`make release`, `make release version=`, `node tools/release.mjs`, and
`node tools/release.mjs ""` all prepare the next patch version. Preview it with
`node tools/release.mjs --dry-run` before creating a release commit or tag.

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
MyGo's `buildCommand` and skips its build hook: Spool builds its HTTP/PWA and
private settings entries together. The small settings bridge has an explicit typed
client, so packaging must not run the desktop app to generate a second TypeScript
client or open the user's database. The tag's
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
Web `/healthz`, embedded index/PWA manifest, actual system-WebView React rendering,
the full-window viewport, login screen and unauthenticated API guards with an HTTPS
public URL configured, all settings sections, server start/stop while settings
stays usable, user/project persistence through the native bridge, chat denial of
management calls, service settings autosave and manual-restart notices, settings
form draft preservation, native theme/language changes before
sign-in and while settings hide the chat, main-window hide/show without losing a
draft or creating another window, and clean exit. Linux runs under Xvfb
with software GL; Windows and macOS use their runner's desktop. A failed probe
blocks publication and retains its diagnostics, not the extracted executable.

The desktop's `--release-smoke <report.json>` mode always makes a new temporary
database, binds only loopback on a temporary port, disables agent discovery and
automatic update checks, bypasses the installed app's single-instance lock, and
exits after the probes. It never opens a supplied `--data-dir` or the usual
`SPOOL_HOME` database. Native window/locale state is also isolated in that
temporary root. The probe never signs in or replaces installed-app cookies.
The runner enforces a 90-second process timeout.

For release-history visual checks, `--release-smoke <report.json>` also accepts
`--smoke-release-history <fixture.json>`. The fixture uses the `ReleaseHistory`
shape (`current_version`, `target_version`, `releases`), with at least two release
entries. It changes only the smoke process's displayed version and fake updater;
no installed version, database or archive is changed. The actual native bridge
and Markdown UI produce `-release-history.png` and `-release-history-older.png`
screenshots next to the report. Without a fixture the probe uses deterministic
test notes and never contacts GitHub.

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

The WebView control center has an **About** page and the tray has **Check for
updates**. Official CI builds check shortly after launch and every 12 hours by
default. In **About**, the automatic check interval can be set to 30 minutes,
1 hour, 6 hours, 12 hours, or 24 hours; changes are saved immediately in SQLite
and reschedule the next check without restarting. Automatic installation is **opt-in**, stored locally in SQLite, and
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

The About page shows the failure stage and a **Show update log** button.
`<data-dir>/log/update.log` records checks, retries, installation results, shutdown and
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
