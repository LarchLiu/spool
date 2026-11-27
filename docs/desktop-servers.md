# Desktop server connections

The local service is always included. Server tabs above the left sidebar's
workspace navigation switch between servers without closing their chat pages.
At launch and after a restart, every server with **Connect to this server**
enabled connects automatically, including tabs that have never been selected.
These servers keep their connections and reconnect in the background until
Desktop exits. Disabled servers stay gray and make no connection attempts.
The selected tab only controls which workspace is shown. The local service
continues to follow its **Start Spool service on application launch** setting.
The status dot is green when connected and signed in, blue when reachable but
sign-in is required, yellow while connecting, red on failure, and gray when
disconnected or disabled. Hover text explains the connection and sign-in status.
Signed-in pages use their live connection to report status. Desktop checks
signed-out servers for availability even while their pages are in the background,
restoring blue when they recover. Checks run about every 15 seconds with a
4-second timeout; a failed check turns red immediately. Failed initial connections
are checked again and reload automatically when the service becomes reachable.
Initial version/session requests have an 8-second timeout. An authenticated
WebSocket error or close reports an interruption immediately; 45 seconds without
messages also counts as an interruption. Reconnection continues without an
attempt limit, with exponential backoff from 0.5 to 30 seconds plus up to 20%
jitter. Status does not wait for a fixed number of failed retries.
Tabs scroll horizontally when they exceed the sidebar width, including
by holding and dragging the mouse. **+** scrolls with the list after the last tab.
Selecting the last server also scrolls **+** into view.
Selection and scrolling animate smoothly, respecting the system's reduced motion setting.
Selecting a tab always switches its sidebar, including disconnected or disabled
servers. Use **Connect**, **Retry**, or **Sign in** in that sidebar to continue.
The sign-in page shows the current server name beside **Settings**; its back
button returns to the server sidebar.
Without account details, the footer shows the action after **Settings**. With
account details, a plug (connect/sign in), retry arrow, or busy spinner appears
before **Settings**. Connected accounts need no connection action.

All servers share one sidebar width, preserved across server switches and desktop restarts.

Use **+** to add a server in a modal inside the main window. The display name is optional:
leave it blank to follow the name configured in the server's settings, including
later name changes. A custom display name overrides it on this desktop only.
The local tab also uses its configured server name.

Each remote server can save three independent connection configurations:

- **Public HTTPS:** the public HTTPS address used by the PWA, or an HTTP origin
  for a directly accessible local service.
- **Tailscale:** the server's HTTPS DNS origin, reached through an embedded
  desktop node. Use **Sign in to Tailscale** to enroll this node.
- **Spool Relay:** paste a server pairing code or use the QR button to read a
  pairing image locally, connect, and approve the device on the server using
  the displayed fingerprint.

**Connect** selects the visible connection mode. The active mode shows
**Disconnect**, which also cancels a pending connection or pairing approval
without removing the saved profile. Browsing another mode's tab does not change
the active connection. The information button shows connection details on demand.
Automatic switching can try other configured modes if the active route becomes
unavailable.

Logins are saved separately for every server profile, connection mode, and
destination. Public HTTPS, Tailscale, and Relay can use different Spool accounts
even when they reach the same server. Switching back restores that mode's own
session; signing out affects only that session. Changing an address starts with
a separate session, and automatic switching may require signing in to that mode.

On Windows, Linux, and macOS 14 or later, each remote route also owns a native
WebView Session, isolating cookies, local storage, caches, and service workers.
The remote desktop window uses that same Session. On the first launch after
upgrading, Spool copies only the route's existing cookies before navigating,
then removes the migrated copies from the default store. Completed migration
is recorded so signing out does not import an old login again. The local account
and its system notifications retain the existing default store. Earlier macOS
versions retain gateway cookie/path isolation because named stores there do not
persist between launches. Disposable smoke probes use memory-only Sessions.

On macOS, Spool prevents App Nap while its local service runs or a workspace is
connected/connecting, including when the window is hidden. It releases this
activity when idle or quitting and still allows normal computer sleep.

The chat's **Settings** button opens the selected remote server's connection
dialog. It can edit, disable, or delete that profile. A disabled tab remains
selectable and can be connected again from its sidebar. Local Settings continues
to manage the local service. Connection profiles are kept on this desktop,
separately from server settings and account preferences.

Profiles live in `desktop-servers.json` in the local data directory, normally
`~/.spool/` (`~/.spool-dev/` in development), overridden by `SPOOL_HOME` or
`-data-dir`. Each manually added profile has its own ID; names, addresses and
public Server IDs do not merge profiles. The system WebView stores login cookies;
the profile file stores neither account passwords nor session cookies.

Trusted entries follow their source server and use a key icon. Ordinary B,
B through A, and B through D keep separate login sessions. Changing the source
account also starts a separate destination session. They can reuse an existing
Tailscale node or Relay device connection. Relay trust automatically obtains the
target route and exchanges a login ticket using an independent client key, without
another pairing code, device approval, or target account registration. An older
public address does not replace the Relay route. Applicants can independently
choose Public HTTPS or Relay for the destination's return connection; approval
saves that route automatically. Tailscale is temporarily hidden in trust settings;
ordinary Tailscale connections remain available. Selecting a signed-in trusted entry
restores its cached page directly. On first sign-in or session expiry, its spinner
indicates source authorization, destination identity checks, a one-time ticket
exchange and session storage. The trusted workspace's **Sign in** button starts
that same flow; its controls and trusted tab remain disabled while it runs.
Enabled trusted profiles also sign in automatically when their background
connection confirms they are signed out. Completion keeps the current tab;
changes to the source account or authorization stop the exchange.
Ordinary tabs use their own login and do not
automatically borrow a trusted source.

Manage people on the destination host. An external user appears in
**Settings → People** after the first successful trusted sign-in, with the source
server name, Server ID and source user ID. Source allowlists are not imported
before sign-in. People settings control account disablement, remote desktop and
ADB access; **Settings → Projects** controls membership, personal chat creation
and shared session access. Remote chat settings manage the connection. Use
Desktop on the destination host or the [server CLI](server-cli.md) to administer
its people and permissions.
