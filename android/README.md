# Spool Android Connection Preview

An Android 8.0+ shell that loads the **server-hosted** Spool Web UI. It does not
bundle React assets or run Spool/agents on the phone. All implementation lives in
this directory, including an independent Go module.

## Connection Path

```text
Android WebView: https://spool.example.ts.net
    -> app-only HTTP CONNECT proxy on a random 127.0.0.1 port
    -> tsnet.Dial (embedded userspace Tailscale node)
    -> server's embedded tsnet HTTPS listener
       (or Tailscale Serve -> Spool Server at 127.0.0.1:8787)
```

The CONNECT proxy does not terminate TLS, rewrite Host/Origin, or handle Spool
cookies itself. The WebView retains the real HTTPS origin for login, CSRF,
WebSocket and EventSource. The proxy requires a per-run random credential and
permits only the selected server's exact host and port. There is no direct
network fallback, shared auth key, JavaScript/native bridge, or system VPN.

The Go layer registers an Android network-interface getter before starting
tsnet. The native side supplies ConnectivityManager LinkProperties on startup
and network changes, avoiding Go netlink interface enumeration denied to Android
application UIDs. This is still a prototype: actual device/provider networking,
WebView proxy authentication and tailnet enrollment need the device checks below.

## Server Setup

Desktop and the headless Go server can provide HTTPS using an embedded tsnet
node, without installing a system Tailscale daemon or configuring Serve.
In Desktop, open **Service > Tailscale (tsnet)**, enable it, optionally stage an
Auth Key, then start/restart the service. Without a key, use the displayed
Tailscale browser login button. Copy the assigned HTTPS address into the Android
app; the APK does not need rebuilding. Auth Keys are not bundled into the APK.

For the headless server:

```sh
./build/spool-server -tsnet -tsnet-hostname spool-server
```

Sign in using the login URL printed by the server and approve its node if
required. Enable **MagicDNS and HTTPS certificates** in the Tailscale admin
DNS settings. Spool prints the actual HTTPS URL and automatically saves it as
the Public origin. Persistent node state stays in `<data-dir>/tsnet/`.
The flags are saved; later launches reuse them and the enrolled node identity.
For unattended enrollment, inject `TS_AUTHKEY` via the process environment or a
secret manager, not a command-line argument. Disable with `-tsnet=false`.

Alternatively, install and sign in to system Tailscale on the server, and run
Spool with its normal loopback listener. Expose it **within the tailnet**, not
with Funnel:

```sh
tailscale serve --bg http://127.0.0.1:8787
```

For the Serve option, set Spool's **Public origin** to the exact HTTPS origin returned by Serve, for
example `https://spool.example.ts.net`, and apply the service settings. Tailnet
policy must permit the phone's embedded node to reach that server/port. A Spool
user and authorized project must already exist; Tailscale login does not replace
Spool login. The server must remain running and awake. The same APK works with
either option: enter the chosen server's HTTPS URL, without rebuilding the app.

## Build

Requirements:

- Go 1.27.3+, with the existing shared module cache.
- JDK 17+ (the scripts also detect Android Studio's bundled JDK on macOS).
- Android SDK Platform **36.1**, Build Tools **36.1.0**, and accepted SDK licenses.
- Android NDK **29.0.14206865**. Set `ANDROID_NDK_HOME` to use another compatible
  installed NDK. No NDK is installed implicitly by the build script.
- Network access on the first build for pinned Go, Gradle and Maven dependencies.

Set `ANDROID_HOME` if the SDK is not at `~/Library/Android/sdk`. The optional
`sh android/tools/install-ndk.sh` installs the fixed official NDK into that SDK
on macOS, checking its published checksum. It downloads about 1 GB, never
overwrites an existing NDK, and cleans its owned scratch directory. Allow at
least 8 GiB of free space for the download, extraction and subsequent builds;
the installer checks this before downloading.

Run from the repository root:

```sh
# Go AAR (arm64 phone + x86_64 emulator), debug APK, Java tests, and Android lint.
sh android/tools/build.sh

# Go proxy and configuration tests. They bind disposable loopback ports.
sh android/tools/run.sh go -C android/transport test -race ./...
sh android/tools/run.sh go -C android/transport vet ./...

# Without an NDK: generate the real Java bindings, compile Android resources
# and all Java sources, then run the origin-policy JUnit tests. Requires Python 3.
# This is a compile check, NOT a substitute for the AAR/APK build.
sh android/tools/check-java.sh
```

The Gradle wrapper is pinned to 8.13 with distribution SHA-256 verification;
AGP is 8.13.2, AndroidX WebKit is 1.18.0. Go dependencies and their checksums are
in `transport/go.mod` and `transport/go.sum`.

Outputs:

- `build/android/spoolnet.aar`
- `build/android/gradle/app/outputs/apk/debug/app-debug.apk`
- `build/android/gradle/app/reports/tests/testDebugUnitTest/index.html`
- `build/android/gradle/app/reports/lint-results-debug.html`

The debug application ID is `com.cloudgeek.spool.android.dev`. Its debug key is
created in `build/android/debug.keystore`, not the developer's Android home.
This preview is not a signed production release.

For an arm64-only iteration:

```sh
SPOOL_ANDROID_TARGET=android/arm64 sh android/tools/build.sh :app:assembleDebug
```

After generating the AAR, open `android/` in Android Studio, or use
`sh android/gradlew :app:assembleDebug`. Do not run a bare Gradle command if it
would bypass the repository's scratch/cache wrapper. Gradle and Go build caches
live under `build/cache/`; every command wrapper owns and cleans a separate
`build/tmp/android.XXXXXX/` run. `GOPATH` and `GOMODCACHE` are not changed.

## Device Verification

Install the debug APK using Android Studio or your normal `adb install` workflow.
Keep disposable emulator profiles, logs and screenshots under the repository's
run/artifact directories if creating them for this test.

1. Enter the server HTTPS origin, without a path, and tap **Connect**.
2. Tap **Sign in to Tailscale**. Authenticate in the external browser, then return
   to Spool. Approve `spool-android` in the Tailscale console if required.
3. Wait for the TLS-verified `/healthz` check. The app then sets the WebView proxy
   before loading any remote page.
4. Sign in with the **Spool** account. Confirm reload retains the session and a
   mutation (for example creating a chat) passes CSRF validation.
5. Start a turn and confirm tokens appear incrementally (SSE), presence/sidebar
   updates arrive (WebSocket), and selecting/uploading an attachment works.
6. Switch Wi-Fi/mobile data, background/foreground the app, and verify recovery.
7. Disconnect/reconnect. Try an invalid certificate and an unavailable server:
   both must fail, not load through a bypass. External links require confirmation
   and open outside the app; they never receive its proxy credentials.

If the health check succeeds but the page reports a proxy authentication error,
record the Android System WebView version and visible error. The Go tests verify
HTTP proxy authentication and byte-transparent TLS, but do not substitute for
testing Chromium's proxy-auth callback on the installed WebView provider.

### Connection Diagnostics

The connection page shows Tailnet enrollment state, the selected server's node
path (Direct, DERP with region, or Peer Relay), round-trip latency, target node IP,
the HTTPS check result, WebView loading state, and the sample time. The browser
toolbar retains a compact path/latency line; **Connection status** opens the full
live details without reloading the page.

Diagnostics use the embedded node's local API, not a separately installed
Tailscale client. A successful DISCO ping determines the path; a peer's home DERP
field alone is never treated as evidence of relayed traffic. The origin's exact
MagicDNS name must match a visible peer. Custom aliases or names not recognized
in the peer map report an unknown target rather than probing another node.

A separate worker bounds each diagnostic request to four seconds, then waits
five seconds before the next probe. Polling pauses in the background, drops
results from previous connection attempts, and stops on disconnect. Failed
samples clear the previous path/latency. Node reachability and latency do not
prove that ACLs, TLS, or Spool authentication permit an HTTPS request. HTTPS
errors therefore remain visible independently of a successful node probe.

## Scope And State

- The APK includes only the native shell and networking. UI updates come from
  the server; an offline server cannot provide a usable chat UI.
- No notifications, foreground/background service, camera/microphone capture,
  native downloads, or automatic APK updates are included. File downloads show
  an explicit unsupported notice rather than bypassing the tunnel.
- The system document picker handles file uploads; no storage permission is
  requested. WebView file/content navigation is disabled.
- tsnet state is kept in the app's private `noBackupFilesDir`; backups and device
  transfer are disabled. Before starting tsnet, the Go layer creates a private
  `tsnet/logs` directory and sets `TS_LOGS_DIR` for the backend's socket-stat
  logger, which does not use `tsnet.Server.Dir`. Without it, Android's missing
  HOME and inaccessible system/temp directories can cause a native Go panic
  when connecting. The native preferences save only the server origin.
- Disconnect closes the node and proxy but does **not** log out of Spool or
  forget the node identity. Use Spool's logout to end the Web session; clear the
  app's Android storage to discard the embedded node credentials, and remove
  the old node in the Tailscale console when appropriate.
- The OS may stop this foreground-only prototype in the background. Agent tasks
  remain on the server, but an active UI connection is not guaranteed.
