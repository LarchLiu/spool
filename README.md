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

### Linux release installation

Download `install.sh` from the release assets and run it as your regular user:

```sh
sh install.sh
```

After verifying the release, the script prepares WebKitGTK, desktop graphics
and tray libraries, and voxl build dependencies (Clang/libclang, esbuild,
pkg-config, x264, libyuv, Opus, PulseAudio and XCB). It supports apt-get, dnf, pacman
and zypper. Missing packages require administrator authorization through sudo or
polkit; distributions may require additional repositories for codec packages.
The script reads `/etc/os-release`, refreshes package metadata and checks all
package candidates before installing any dependency packages. Missing packages
are listed together with repository guidance. It never adds codec repositories
or performs a full system upgrade. Arch users should run `pacman -Syu` themselves
before installation. The script rechecks the libraries before replacing an
existing Spool app. It accepts an already installed WebKitGTK 4.1 or 4.0 stack
and either Ayatana AppIndicator or the older AppIndicator library; new installs
prefer 4.1/Ayatana where available.

The automatic dependency setup targets Ubuntu 22.04+, Debian 12+, Fedora,
Arch-based systems and openSUSE. Debian/Ubuntu derivatives are identified by
`ID_LIKE`; their own version numbers are never treated as Ubuntu versions.
RHEL-family systems (9+) use their own configured repositories and may need
additional development/codec repositories. Missing packages cause a clear stop,
not a partially installed dependency set. Fedora/Arch/openSUSE/RHEL and
derivatives remain experimental until verified on the specific release.
Alpine, NixOS, unrecognized distributions and immutable/OSTree variants require
manual dependency provisioning. Older Ubuntu/Debian/RHEL versions are excluded
from automatic setup. `--skip-dependencies` is available for managed environments;
it does not establish binary compatibility. Release archives target x86_64 and
arm64 and require Python 3 and OpenSSL 3+ for verification.

If Rust or Cargo is missing or older than 1.85, it installs rustup for the
current user when needed, then runs:

```sh
rustup toolchain install stable --profile minimal
rustup default stable
```

It leaves shell profiles unchanged and
preserves a working, compatible toolchain. This step needs access to
`sh.rustup.rs` and Rust's toolchain download servers.

For manual setup on Debian/Ubuntu, install the system packages with:

```sh
sudo apt-get update
sudo apt-get install --no-install-recommends \
  libwebkit2gtk-4.1-0 libgl1 libegl1 libgl1-mesa-dri libxkbcommon0 \
  libayatana-appindicator3-1 build-essential clang libclang-dev pkg-config \
  esbuild libx264-dev libyuv-dev libopus-dev libpulse-dev libxcb1-dev
```

Use `sh install.sh --skip-voxl-dependencies` to install only the desktop runtime,
or `sh install.sh --skip-dependencies` when dependencies are managed separately.
An offline Spool archive can be passed after these options; dependencies still
need network access unless already installed. voxl itself is built later with
**Install voxl** in Settings → Service → Remote desktop. Fully quit and reopen
Spool after installing libraries. Its menu entry is Applications → Development
→ Spool on Xfce.

The installer also selects rendering for the applications-menu launcher. It
checks an X11 display with `glxinfo -B` when available (with a five-second
timeout). Native Wayland and systems without `glxinfo` use accessible DRM render
or NVIDIA device nodes instead; XWayland GLX results do not determine the native
Wayland renderer.
Without usable acceleration, it adds these environment variables to the desktop
entry to avoid WebKit crashes on cloud desktops and machines without a GPU:

```sh
LIBGL_ALWAYS_SOFTWARE=1
WEBKIT_DISABLE_COMPOSITING_MODE=1
WEBKIT_SKIA_ENABLE_CPU_RENDERING=1
```

Use `sh install.sh --software-rendering` to force this mode if the graphics
driver is unstable. Use `sh install.sh --hardware-rendering` to keep WebKit's
default rendering instead. These options also work with `--skip-dependencies`.
Fully quit Spool and reopen it from the applications menu for the choice to take
effect. Directly executing the Spool binary uses the terminal's environment.
Device access is a heuristic, not a driver health test. WebKit versions may
handle these environment variables differently. voxl screen capture still needs
X11/Xvfb; these rendering options do not add native Wayland capture support.

Docker checks are **manual only** and are not part of normal tests or release
automation. From the repository root, an optional disposable-container check is:

```sh
sh tools/check-linux-container.sh ubuntu:24.04 native
```

`native` installs and loads the desktop/voxl system libraries and links a libyuv
probe. `desktop` only checks the desktop runtime; `voxl` also downloads Rust and
builds the pinned voxl version. Reusable caches stay in `build/cache/`, disposable
data in `build/tmp/`, and the latest log for each image/mode in
`build/artifacts/linux-compatibility/`. Container checks verify libraries and
build prerequisites; they do not replace graphical startup tests on real
X11/Wayland desktops or hardware GPU tests.

Manual validation for this installer change:

| System | Container architecture | Verified scope |
| --- | --- | --- |
| Ubuntu 22.04 / 24.04 | arm64 | Desktop and voxl native libraries installed; runtime load and link probes passed |
| Debian 12 | arm64 | Desktop and voxl native libraries installed; runtime load and link probes passed |
| Fedora 43 | arm64 | Desktop libraries installed and runtime load probes passed; voxl codec repositories not verified |
| openSUSE Tumbleweed | arm64 | Desktop libraries installed and runtime load probes passed; voxl codec repositories not verified |
| Arch Linux | x86_64 under arm64 emulation | Installation check blocked by pacman sandbox support in the emulation environment |

These checks did not compile voxl or run graphical desktop/GPU tests. Debian 13,
RHEL-family systems, openSUSE Leap and other derivatives have not been tested in
real containers for this change.

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

Releases also include separate `spool-server-VERSION-OS-ARCH.tar.gz` archives for
all six desktop OS/architecture targets. These contain the headless CLI and
embedded Web/PWA assets; no GUI, GTK/WebKit or voxl is needed. Linux archives
include a systemd user service example. See the [Server CLI guide](docs/server-cli.md)
for installation, local administration, all command groups and service setup.

Use `spool-server --help`, `spool-server help project`, or
`spool-server project member set --help` for nested help. Run `serve` explicitly;
without a command the CLI prints help. Existing flat flags remain compatible.

Before starting a headless server, create the initial user and project while the
service is stopped:

```sh
SPOOL_NEW_PASSWORD='replace-with-a-long-unique-password' \
  ./build/spool-server user add --username alex --nickname Alex

./build/spool-server project add --name Example \
  --directory /absolute/project/path --members alex

./build/spool-server serve --host 127.0.0.1 --port 8787
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
X11/Xvfb, Rust, x264, libyuv, Opus, PulseAudio and XCB development libraries
(`libxcb1-dev` on Debian/Ubuntu); Windows needs Rust
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
