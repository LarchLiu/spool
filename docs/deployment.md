# Deployment and remote access

[简体中文](deployment.zh-CN.md)

This guide covers installation, service setup, Web/PWA access, Tailscale, and
voxl remote desktop. Commands using `./build/` paths assume the repository root.
For source builds, see the [README](../README.md#development).

## Linux release installation

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
arm64 and require Bun and OpenSSL 3+ for verification.

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

The applications-menu launcher starts Spool directly with WebKit's default
rendering configuration. voxl screen capture requires X11/Xvfb.

Docker checks are **manual only** and are not part of normal tests or release
automation. From the repository root, an optional disposable-container check is:

```sh
sh tools/check-linux-container.sh ubuntu:24.04 native
```

`native` installs and loads the desktop/voxl system libraries and links a libyuv
probe. `desktop` only checks the desktop runtime; `voxl` also downloads Rust and
builds the pinned voxl version. Repository-managed reusable caches stay in
`build/cache/`, disposable runs use the system temporary directory's `spool/`
namespace and are removed on exit, and the latest log for each image/mode stays in
`build/artifacts/linux-compatibility/`. Container checks verify libraries and
build prerequisites; they do not replace graphical startup tests on real
X11/Wayland desktops or hardware GPU tests.

## Running the service


Development runs use `~/.spool-dev` by default; production builds use `~/.spool`.
The database is stored at `db/spool.sqlite` inside the selected data directory.
Use `-data-dir` or `SPOOL_HOME` to select another data directory. Only one
Desktop or server instance can own a data directory at a time.

Releases also include separate `spool-server-VERSION-OS-ARCH.tar.gz` archives for
all six desktop OS/architecture targets. These contain the headless CLI and
embedded Web/PWA assets; no GUI, GTK/WebKit or voxl is needed. Linux archives
include a systemd user service example. See the [Server CLI guide](server-cli.md)
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

./build/spool-server serve --host 127.0.0.1 --port 59001
```

Chat and management APIs require an enabled account. The service does not
provide a default password, remote sign-up, or remote administration API.

### Web/PWA and HTTPS

For local access, open `http://127.0.0.1:59001`. To use a browser on another
device or install the PWA, access Spool through HTTPS and configure the exact
same Public URL in Desktop settings or the server configuration. For example,
with Caddy:

```caddyfile
chat.example.com {
    reverse_proxy 127.0.0.1:59001 {
        flush_interval -1
    }
}
```

The reverse proxy must support WebSocket upgrades and long-lived SSE connections,
and should disable response buffering. Spool still enforces login cookies, CSRF,
Origin, and project permission checks.

In Desktop, open **Settings > Service > Web**, enter the public HTTPS address
provided by your tunnel or reverse proxy, then leave the field or press Enter.
Saving a public URL or HTTP port starts a preflight; **Detect** retries the check.
When the candidate listen address is free, a temporary listener exposes only the
one-time detection callback. When the current Web listener owns that address, the
preflight uses its callback instead. A port occupied by another process fails
preflight. Point the tunnel or reverse proxy at the candidate Web port before
testing. Preflight never changes the running entry or its trusted proxies, and
both successful and failed checks retain the saved input and pending changes.
Click **Apply changes** to activate the configuration. Spool releases any temporary
listener before starting the real entry, then checks again and updates automatic
proxy trust only after verification. Detection failure keeps the applied URL and
port; a listener bind failure leaves the saved changes pending. Editing again
cancels the previous check, whose result cannot overwrite newer settings.
Spool also checks on ordinary Web entry startup, without blocking local startup
or polling periodically. Retry manually if the tunnel starts later or its address
changes during operation. A successful real-entry recheck replaces the previous
detected address. Manual **Web trusted proxies** under **Advanced settings** can supplement
multiple hops or replicas. The shared backend also handles headless servers:
`serve --public-url https://chat.example.com` detects after starting; offline
`config set` saves the address for detection on the next `serve`. CLI logs report
detection success or failure.

The ordinary HTTP listener serves the application only to direct loopback clients
or a trusted HTTPS reverse proxy. Remote HTTP requests receive 403 before the
login page, password body or session is processed; `/healthz` remains public.
Setting Public URL does not enable TLS on the backend listener.

For proxy access, set Public URL to the exact external HTTPS origin. The proxy
must overwrite `X-Forwarded-Proto` with a single `https` value and preserve the
public Host, or set a single matching `X-Forwarded-Host` when rewriting Host.
Caddy supplies these headers in the example above. Spool ignores claims from
untrusted peers and rejects ambiguous forwarded values. HTTP frontend requests
must be redirected or rejected by the proxy before they carry login data.

Manual **Web trusted proxies** defaults to `127.0.0.0/8,::1/128`. Automatic
detection manages one exact connecting IP bound to the verified public origin.
For additional hops, configure only their actual IP CIDRs in Desktop advanced
settings or with `spool-server config set --web-trusted-proxies '192.0.2.10/32'`.
An empty value clears manual entries; verified addresses are managed separately.
Keep the backend on loopback or a protected network, and restrict access to
trusted proxies; this setting does not encrypt
the proxy-to-backend connection. It is separate from outbound proxy settings.

The ordinary Web backend also uses this trust list to resolve client IPs for
login limits. The proxy must overwrite `X-Forwarded-For` with the actual client
IP or append the address it connected from. Spool walks the chain from right to
left through configured trusted proxies and stops at the first untrusted IP;
client-supplied prefixes cannot select a different login bucket. For multiple
proxy hops, configure each trusted hop's CIDR, never all client networks. Missing
or malformed trusted-hop information falls back to the connection peer's quota.
The limit remains 20 login attempts per IP per 10 minutes, including malformed
requests. Users sharing a public IP through NAT still share that IP's quota.
Relay and Tailscale retain their transport-supplied client identities.

Desktop settings provides **Enable Web access** and **Allow LAN access**
switches. LAN access binds all IPv4 interfaces so a configured reverse proxy can
reach the backend; it does not permit direct HTTP login from another device.
Use HTTPS, Tailscale or paired Relay/LAN for remote access. Otherwise, Web access
is limited to this computer. Turning off
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
servers always requires selecting a tab. Public HTTPS, Tailscale and Relay keep
separate logins, bound to each entry's exact HTTPS origin. Sign in on each entry
before automatic switching can use it. Changing an address, port or paired server
identity requires signing in there again. Relay's verified LAN and relayed paths
share the same pinned identity and login. Upgrading from the former shared-session
format requires signing in again. Signing out revokes the active session and clears
all locally saved logins for that server; other route sessions expire under the
server's normal policy. Different server profiles retain independent logins.
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
Other entries and running agent tasks stay active. Runtime idle timeout and
history cache size changes apply immediately when saved, without restarting
services. Existing idle agents use the new timeout measured from their last
completed turn; running tasks remain active.

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
