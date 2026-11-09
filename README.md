<p align="center">
  <img src="./assets/logo.png" alt="Logo" width="60">
</p>
<h1 align="center">Spool</h1>

[简体中文](README.zh-CN.md)

Android can connect to a home Spool server through an independent Go relay, with
verified LAN connections preferred automatically. Spool Desktop supplies a pairing
QR code. See [relay deployment and pairing](relay/README.md).
The relay can also publish Web/PWA access at `/web/<server-id>/` on its existing
HTTPS address. Enable it on the home server; the trusted relay adds the route
when Spool connects, with no Server ID list or relay restart. No per-server
domains or Tailscale client are required. See
[public Web access](relay/README.md#public-web--pwa-access).

> Spool is a multi-platform, multi-user workspace for local AI coding. It brings
Desktop, Web/PWA, and Android clients into one shared project and chat space,
so a team can work together from different devices with the same permissions,
presence, and project context.

![Spool Desktop workspace](docs/images/desktop-workspace.png)

Tasks and code execution run on the machine hosting the service. Project
directories, native CLI sessions, and provider login data stay with the tools
on that machine. Team members connect to that service through the clients above;
Spool is not a hosted model service. Install and sign in to Codex or Claude Code
on the service host before use.

## Features

### Multi-platform workspace

- Use one Spool service from the Desktop app, a browser/PWA, or the native Android client.
- Continue the same projects and chats across devices without copying the working directory or provider sessions.
- Use embedded Tailscale HTTPS, a trusted reverse proxy, or the opt-in relay Web gateway when the service needs to be reached remotely.

### Multi-user collaboration

- Invite people to projects with separate personal-session and shared-session permissions.
- See online presence, exchange private messages, and use online or selected-member group chats.
- Share project context with explicit project membership, session management, and file access.

### Local AI coding workspace

- Supports Codex and Claude Code, including each CLI's available models, reasoning levels, and permission modes.
- Uses an independent native CLI session for every chat, with streaming output, stop, resume, and idle release.
- Renders Markdown, code, diffs, tool activity, images, and file attachments; browse project files and Git worktrees directly.
- The file sidebar follows the latest session turn's directory, with a directory chooser when multiple checkouts are available.
- Supports the native `/goal` command, scoped command/file approvals, permission subsets, interactive questions, and MCP authorization forms on Web and Android.
- Saves agent, model, and permission selections per chat while keeping history in the native Codex or Claude session.

### Projects and permissions

- Organizes working directories, sessions, members, and agent settings by project; data is stored in local SQLite.
- Separates user, project-member, personal-session, and shared-project-session permissions.
- Binds file access to the project directory so remote requests cannot choose an arbitrary working directory.
- Provides authorized workspace file browsing, Markdown/HTML and [Word/Excel previews](docs/office-preview.md) (`.docx`/`.xlsx`), source viewing, and limited external-file references.
- Projects expose the service host's actual files and operating-system permissions, so access should be granted only to trusted users.

### Presence and notifications

- User avatars, presence, theme, and language preferences across supported clients.
- Desktop system notifications and Web Push identify tasks by session title, private messages by sender, and group messages by group and sender names.
- English, Simplified Chinese, and Japanese, with system theme and language support.

### Automation and remote access

- Scheduled GitHub Issue processing with Codex or Claude in isolated worktrees, producing PRs targeting `dev`.
- A headless Go server for a continuously running development machine or server.
- An optional embedded Tailscale (`tsnet`) HTTPS entry for Android and remote browsers on the tailnet.
- Optional [voxl](https://github.com/tembo/voxl) remote desktop, started on demand and granted per user by an administrator.

![Spool Web workspace](docs/images/web-workspace-dark.png)

## Development and build

### Requirements

- Go **1.27.3** or newer.
- Bun **1.4.4** (the pinned version in `.bun-version`).
- Codex CLI or Claude Code installed and signed in on the service host. They are optional for frontend-only work and tests that do not use an agent.
- Android development requirements are listed in the [Android documentation](android/README.md).

MyGo is pinned to `v0.6.4`. Frontend dependencies are installed in
`web/node_modules/`; production frontend assets are embedded in the Desktop and
server binaries.

Dependencies are locked in `web/bun.lock`. Build commands use `bun run --bun`
so dependency installation scripts, TypeScript and Vite all run with Bun.

### Local development

Run from the repository root:

```sh
(
  set -eu
  REPO_ROOT="$(git rev-parse --show-toplevel)"
  cd "$REPO_ROOT"
  export GOCACHE="$REPO_ROOT/build/cache/go-build"
  mkdir -p "$GOCACHE"
  make dev
)
```

`make dev` installs and builds the Web assets, then starts the MyGo development
Desktop and Vite hot reload. Go changes rebuild the application; frontend
changes use HMR. The default service port is `127.0.0.1:8787`, and the Vite
development port is `127.0.0.1:5173`.

For browser-only development, run the backend and Vite in separate terminals:

```sh
# Terminal 1
GOCACHE="$PWD/build/cache/go-build" go run ./cmd/server

# Terminal 2
bun run --bun --cwd web dev
```

Vite proxies `/api` to `127.0.0.1:8787`. Use the Go-served address when checking
PWA installation, Service Worker updates, or offline behavior.

### Build and test

```sh
# Install dependencies and build embedded frontend assets
GOCACHE="$PWD/build/cache/go-build" make web

# Build unbundled Desktop and headless server binaries
GOCACHE="$PWD/build/cache/go-build" make desktop
GOCACHE="$PWD/build/cache/go-build" make server

# Build MyGo desktop packages
GOCACHE="$PWD/build/cache/go-build" make package

# Frontend tests, Go race tests, and go vet
GOCACHE="$PWD/build/cache/go-build" make test
```

Build output is placed in `build/`. `make desktop` creates
`build/spool-desktop`, `make server` creates `build/spool-server`, and
`make package` creates platform-specific desktop applications and installers.
Tests use local mock CLIs and do not call a model for inference.

### Repository layout

- `cmd/desktop`: MyGo Desktop entry point, tray, native settings, and desktop notifications.
- `cmd/server`: headless Go server entry point.
- `internal/agent`: Codex/Claude CLI lifecycle, protocols, and session handling.
- `internal/workspace`: SQLite, authentication, project permissions, chat, presence, and service lifecycle.
- `web`: React/TypeScript Web and PWA client.
- `android`: native Android client and gomobile boundary.
- `frontend`: build-generated Web assets embedded in the Go binaries.

## Running the service

Development runs use `~/.spool-dev` by default; production builds use `~/.spool`.
The database is stored at `db/spool.sqlite` inside the selected data directory.
Use `-data-dir` or `SPOOL_HOME` to select another data directory. Only one
Desktop or server instance can own a data directory at a time.

Before starting a headless server, create the initial user and project while the
service is stopped:

```sh
SPOOL_NEW_PASSWORD='replace-with-a-long-unique-password' \
  ./build/spool-server -add-user alex -nickname Alex

./build/spool-server -add-project Example \
  -directory /absolute/project/path -grant alex

./build/spool-server -host 127.0.0.1 -port 8787
```

Chat and management APIs require an enabled account. The service does not
provide a default password, remote sign-up, or remote administration API.

### Web/PWA and HTTPS

For local access, open `http://127.0.0.1:8787`. To use a browser on another
device or install the PWA, access Spool through HTTPS and configure the exact
same Public URL in Desktop settings or the server configuration. For example,
with Caddy:

```caddyfile
chat.example.com {
    reverse_proxy 127.0.0.1:8787 {
        flush_interval -1
    }
}
```

The reverse proxy must support WebSocket upgrades and long-lived SSE connections,
and should disable response buffering. Spool still enforces login cookies, CSRF,
Origin, and project permission checks.

Desktop settings provides **Enable Web access** and **Allow LAN access**
switches. LAN access uses all IPv4 interfaces; otherwise, Web access is limited
to this computer. Turning off
Web access also stops access through a reverse proxy pointing at that
listener; desktop chat, Tailscale and Relay keep their independent connections.
The listener stays enabled for existing installations. A headless server can
persist the same setting with `-web=false` or `-web=true`.

The native Android APK can use this same address: use **+** at the end of the sidebar server tabs to add a named server. Its footer
**Settings** entry edits only that server. Enter its **Public HTTPS** address and
choose **Connect** in that network entry.
Each server may also have a Tailscale address. Click the network entry to use;
the choice is saved for that server and restored on restart. Enabled servers keep independent connections and appear as tabs at the
top of the sidebar, with a colored connection-status dot before each name. The
palette icon to the left of the sidebar close button opens device appearance and
language preferences. Automatic network switching stays within one server; changing
servers always requires selecting a tab. Each server shares one login between its
public and Tailscale routes, while different servers keep independent logins.
Existing route-specific logins migrate once, preferring the selected route.
Settings use switches on the right for server enablement and automatic switching.
Startup and server selection open the sidebar. Each network entry contains its
own Connect/Disconnect action; the bottom of Settings can delete that server.
Each tab keeps its own workspace state in memory and shows it immediately on
selection. Connected, signed-in servers receive project and recent-chat updates
over independent WebSockets even while another tab is displayed. Reconnection
and network handoff retain cached lists; workspace caches are never persisted.
Tab selection reuses the window, sidebar and existing WebSockets without
reconnecting either server or constructing a hidden chat transcript.
When available, a monitor icon immediately before Settings in the footer opens
the selected server's remote desktop. Availability arrives through the workspace WebSocket.

The Desktop Service settings separate the shared Spool service from its access
entries. Connection changes are saved without interrupting work. The bottom bar
lists pending changes for Web, Spool Relay and Tailscale separately; **Apply
changes** restarts only that entry, while **Cancel** restores its active settings.
Other entries and running agent tasks stay active. Runtime changes that require
restarting the whole Spool service are labelled separately.

### Tailscale and Android

Enable embedded Tailscale in **Service > Tailscale (tsnet)** in Desktop, or run
the following with the headless server:

```sh
./build/spool-server -tsnet -tsnet-hostname spool-server
```

The first setup requires MagicDNS and HTTPS certificates in the Tailscale admin
console, followed by node login or approval. The server prints its actual HTTPS
address after startup; enter that complete address in the selected Android
server's **Tailscale (tsnet)** entry. The
Tailscale entry still requires Spool login and does not bypass project permissions
or CSRF checks.

## Remote desktop (voxl)

voxl is an optional remote desktop component managed separately from project
permissions. A user sees **Connect to desktop** only after voxl has been installed
and enabled locally and an administrator has granted that user's remote desktop
permission. voxl starts when a viewer connects and stops after the last viewer
disconnects. Remote desktop can control the service host's full desktop, including
Spool settings, so grant it only to trusted users.

You can import an existing voxl binary or let Spool build a pinned
`voxl-server` version:

```sh
# Import an existing local voxl binary
./build/spool-server -install-voxl \
  -voxl-binary /absolute/path/to/voxl

# Grant or revoke remote desktop access for a user
./build/spool-server -remote-desktop-user alice \
  -remote-desktop=true

# Enable remote desktop
./build/spool-server -remote-desktop=true
```

Without `-voxl-binary`, Spool first searches for a local executable and then
builds `voxl-server` **0.3.3** from crates.io when needed. The native prerequisites
vary by platform: macOS needs Xcode Command Line Tools and Rust; Linux needs
X11/Xvfb, Rust, x264, libyuv, Opus, and PulseAudio libraries; Windows needs Rust
MSVC, Visual Studio C++ Build Tools, LLVM, and vcpkg.

On macOS, local installation also compiles a small native launcher with Xcode
Command Line Tools and creates `<data-dir>/voxl/bin/Voxl Helper.app`. Its native
supervisor and the locally built voxl are signed ad hoc on this Mac; no Apple
Developer ID certificate is needed. Grant **Voxl Helper** Screen Recording and
Accessibility access in System Settings > Privacy & Security, then reconnect.
Run Spool in the logged-in macOS desktop session, including when using the CLI.
The application is launched through LaunchServices only for an authorized
desktop connection. An authenticated loopback control connection supervises
voxl and revokes capture when Spool stops or its lifetime pipe closes. The last
viewer/transfer closing stops both the helper app and voxl. Installing or
enabling remote desktop does not start capture, and no login item is installed.

Existing macOS installations containing only `bin/voxl` need one local
**Install voxl** action (or `-install-voxl`). This packages the existing binary
without downloading or rebuilding Rust. The old binary is retained; shared
files remain outside the app in `voxl/shared/`. A complete helper app is reused
unchanged across ordinary Spool upgrades, including its signature. Replacing
or rebuilding the helper itself can require new macOS authorization. The
original helper (installation format 1) needs one local **Upgrade voxl**
action to apply the startup cleanup fix. This reuses its bundled engine and
atomically replaces the app after verification; a failed upgrade keeps the old
app intact. Grant the replacement helper access again if macOS requests it.
Windows and Linux continue to run voxl directly with the same on-demand lifetime.

Remote desktop uses the existing Spool HTTPS/Tailscale entry, while the voxl
helper listens only on loopback. Its file panel uses `<data-dir>/voxl/shared/`;
uploads and downloads remain protected by Spool login, remote desktop grants,
Origin, and CSRF checks. See the [voxl upstream instructions](https://github.com/tembo/voxl#usage) for platform dependencies.

Enable the viewer toolbar's **pan button** to zoom and pan locally. With it
selected, use **− / +** or **Ctrl/Command + wheel** to zoom from **100% to 500%**
relative to the desktop fitted to the window. Drag with the left or middle mouse
button to pan, or use two fingers to pinch and pan on a touchscreen. Click the
percentage to fit the desktop to the window again. Turning the pan button off
keeps the current zoom and position, disables local zoom and pan, and restores
voxl's native remote controls, including two-finger scrolling. Ordinary wheel
scrolling still scrolls the remote application in either mode. Zoom affects only
your viewer; the host resolution and other viewers stay the same. The native
keyboard, files, and fullscreen controls remain available.

## voxl copyright and licensing

Spool integrates [voxl](https://github.com/tembo/voxl), whose source code is
licensed under the [MIT License](https://github.com/tembo/voxl/blob/main/LICENSE).
The [x264](https://www.videolan.org/developers/x264.html) library used by voxl
is licensed under [GPLv2 or later](https://www.gnu.org/licenses/old-licenses/gpl-2.0.html);
builds linked with x264 must comply with the applicable GPL terms. Other
dependencies retain their respective licenses.

Installing or building voxl locally does not remove these obligations. If you
redistribute binaries containing voxl or x264, assess and provide the applicable
license notices, copyright information, and corresponding source code.
