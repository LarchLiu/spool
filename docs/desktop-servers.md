# Desktop server connections

The local service is always included. Server tabs above the left sidebar's
workspace navigation switch between servers without closing their chat pages.
The status dot is green when the server is reachable, including before sign-in,
yellow while connecting, red on failure, and gray when disconnected.
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

The chat's **Settings** button opens the selected remote server's connection
dialog. It can edit, disable, or delete that profile. A disabled tab remains
selectable and can be connected again from its sidebar. Local Settings continues
to manage the local service. Connection profiles are kept on this desktop,
separately from server settings and account preferences.
