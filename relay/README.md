# Spool Relay

An independent Go relay for Spool servers behind a home router or CGNAT. Deploy
this directory on a VPS. It does not run Spool, agents, a database, or voxl.
Both the home server and Android make outbound connections to the VPS; there
is no hole punching, Internet P2P, or port forwarding at home.
An optional Web gateway also serves browsers and installed PWAs at
`https://relay.example.com/web/<server-id>/` through the same outbound connection.

## Docker Compose

The included `compose.yaml` runs the independent relay on either a local Docker
host or a VPS. From this directory, configure it once and start it:

```sh
cp .env.example .env
chmod 600 .env
openssl rand -hex 32
# Put the generated value in SPOOL_RELAY_ADMIN_TOKEN in .env.
docker compose up -d --build
docker compose ps
curl --fail http://127.0.0.1:28080/healthz
```

Compose refuses to start if the administrative credential is missing. This is
the management credential; Spool servers use their own registration tokens.
The local `.env` is excluded from Git and the image build context.

| Setting | Default | Purpose |
| --- | --- | --- |
| `SPOOL_RELAY_ADMIN_TOKEN` | Required | Management API credential, 32–512 characters. |
| `RELAY_REQUIRE_REGISTRATION_TOKEN` | `true` | Set `false` to allow signed automatic server registration. |
| `RELAY_BIND_ADDRESS` | `127.0.0.1` | Host interface for the HTTP relay backend. |
| `RELAY_PORT` | `28080` | Host port for the HTTP relay backend. |
| `RELAY_ADMIN_PORT` | `28081` | Host loopback port for the management API. |

For automatic registration, set `RELAY_REQUIRE_REGISTRATION_TOKEN=false` in
`.env` and run `docker compose up -d`. The administrative credential remains
required. Leave the registration token empty in Spool Desktop and click
**Register**; Spool saves the server token returned by the relay.

With the default token requirement, issue server tokens through
`http://127.0.0.1:28081/v1/registrations`, using the administrative credential
from `.env`. The [administration examples below](#registration-administration)
use the same default port for Compose and standalone deployments.

Container-based reverse proxies such as Coolify should target container port
`28080` for relay traffic and `28081` for the administrative API. `RELAY_ADMIN_PORT`
changes only the host port mapping; the container's administrative port stays
`28081`. When updating an existing deployment, update proxy targets and explicit
listener overrides to use the new ports. Existing `.env` values and saved Spool
LAN settings retain their configured ports; use `28080` for `RELAY_PORT`, `28081`
for `RELAY_ADMIN_PORT`, and `28082` for the secure LAN listener to adopt the defaults.

The relay HTTP port and the administrative port are separate. Put an HTTPS
reverse proxy in front of `127.0.0.1:28080`, as in the Caddy example below, and
enter that HTTPS origin in Spool Desktop. Android pairing requires an HTTPS
origin with a certificate trusted by the app; a LAN HTTP address does not work.
The default loopback mapping is accessible only from the Docker host. Running
the container at home does not itself create a publicly reachable endpoint.

Registrations and revocations persist in the `relay-data` named volume. The
container uses the image's non-root user, a read-only root filesystem and a
writable `/data` volume. It restarts after an unexpected exit, and `/healthz`
supplies its Docker health status. An unhealthy status alone does not trigger
an automatic restart. Logs are rotated with a maximum of three 10 MiB files.

```sh
docker compose logs -f relay
docker compose restart relay          # Restart the current configuration.
docker compose up -d --build          # Apply source or configuration changes.
docker compose down                  # Stop and remove containers; retain data.
```

Keep the named volume when updating or moving the deployment. Adding `-v` to
`docker compose down` deletes its registrations and revocations.

### Platforms deploying the Dockerfile directly

When a platform builds `relay/Dockerfile` without Compose, set these as container
**runtime** environment variables:

```dotenv
SPOOL_RELAY_ADMIN_TOKEN=your-generated-management-credential
RELAY_REQUIRE_REGISTRATION_TOKEN=false
```

The relay reads the registration setting itself at startup; it does not need to
be available during the image build. An unset or empty value defaults to `true`;
an invalid boolean prevents startup. An explicit `-require-registration-token`
command-line flag takes precedence. Keep `/data` on persistent storage and route
the public HTTPS endpoint to container port `28080`.

Build and deploy an image containing this runtime environment support before
using the setting with direct Dockerfile deployments. Earlier images only
accepted the command-line flag; their Compose file converted the variable into
that flag. When updating platform environment variables, recreate/redeploy the
container. The startup log confirms the effective setting with
`require_registration_token=false` or `require_registration_token=true`.

## VPS deployment

This directory is a standalone Go module. Copy it to another repository or
machine and build it without any other Spool source or frontend assets:

```sh
# In relay/, with Go 1.27.3 or newer
go build -trimpath -o spool-relay ./cmd/spool-relay
openssl rand -hex 32
# Save that random value as SPOOL_RELAY_ADMIN_TOKEN in your service manager.
export SPOOL_RELAY_ADMIN_TOKEN='the-generated-value'
./spool-relay -listen 127.0.0.1:28080 -data-dir ./relay-data
```

Point `relay.example.com` at the VPS and put a TLS reverse proxy in front.
For example, a Caddyfile:

```caddyfile
relay.example.com {
    reverse_proxy 127.0.0.1:28080 {
        flush_interval -1
    }
}
```

Clients use TCP 443. Let the proxy manage its certificate and HTTP challenge as
usual. WebSocket upgrades must work, with no short maximum connection lifetime.
Heartbeat pings run every 20 seconds; the home server reconnects automatically.
For direct TLS, pass `-listen :443 -tls-cert fullchain.pem -tls-key privkey.pem`.

Container build context is this directory:

```sh
docker build -t spool-relay .
docker run -d --name spool-relay --restart unless-stopped \
  --env-file relay.env -v spool-relay-data:/data \
  -p 127.0.0.1:28080:28080 -p 127.0.0.1:28081:28081 spool-relay \
  -listen 0.0.0.0:28080 -admin-listen 0.0.0.0:28081 -data-dir /data
```

`relay.env` contains `SPOOL_RELAY_ADMIN_TOKEN=...`; restrict it to the service
owner. Use TLS at the public proxy. The admin port remains bound to the VPS's
loopback address, including in this Docker example; do not proxy it publicly.
Keep the data volume: it holds server registrations, token hashes and revocations
in `registrations.json`, with owner-only file permissions. The relay never logs
application payloads or credentials. Exclude Authorization headers from logs.

Limits default to 100 home servers, 4,096 data connections total and 128 per home
server. Flags: `-max-hosts`, `-max-connections`, `-max-connections-per-host`.
`/healthz` reports process health, not whether a particular home server is online.
Deploy one relay process: arbitrary load balancing across replicas is not
supported because control and data connections must reach the same process.
Only one process may own a registration data directory.

## Background service and automatic restart

The Go executable runs in the foreground and handles SIGTERM/SIGINT. Let systemd
or Docker own the process lifecycle. The relay has no HTTP restart endpoint;
registration creation and revocation apply immediately without a process restart.

For a Linux VPS using systemd, install the supplied unit and executable:

```sh
# Run from this relay directory, after building or downloading spool-relay.
sudo install -m 755 spool-relay /usr/local/bin/spool-relay
sudo install -m 644 deploy/spool-relay.service /etc/systemd/system/spool-relay.service
sudo install -d -m 700 /etc/spool-relay
sudo touch /etc/spool-relay/relay.env
sudo chmod 600 /etc/spool-relay/relay.env
sudoedit /etc/spool-relay/relay.env
# Set SPOOL_RELAY_ADMIN_TOKEN to your generated value (see deploy/relay.env.example).

sudo systemctl daemon-reload
sudo systemctl enable --now spool-relay
sudo systemctl status spool-relay
sudo journalctl -u spool-relay -f
```

The unit starts at boot, runs as a dedicated dynamic user, persists registrations
under `/var/lib/spool-relay`, and restarts the process five seconds after an
unexpected exit. `Restart=always` also covers an unexpected clean exit. An
explicit `systemctl stop` remains stopped. The loopback relay port is intended
for the TLS reverse proxy described above; the management port stays private.

Use `sudo systemctl restart spool-relay` for deployment/configuration changes.
To enable automatic registration, run `sudo systemctl edit spool-relay` and add:

```ini
[Service]
ExecStart=
ExecStart=/usr/local/bin/spool-relay -listen 127.0.0.1:28080 -admin-listen 127.0.0.1:28081 -data-dir /var/lib/spool-relay -require-registration-token=false
```

Then run `sudo systemctl daemon-reload` and `sudo systemctl restart spool-relay`.
For Docker use the `--restart unless-stopped` command above and `docker restart
spool-relay` for an explicit restart; do not also wrap that container in the
systemd unit. Docker's restart policy handles exits, not an alive-but-unresponsive
process. The systemd unit similarly does not implement an application watchdog.
`/healthz` is available to an external monitor if one is required.

A restart interrupts current relay connections. Home Spool reconnects with
backoff, and Android restores its normal subscriptions. In-flight uploads are
not replayed, and desktop views retain their existing explicit-open behavior.
Verified LAN connections do not depend on the VPS process.

## Registration administration

Each Spool server receives its own registration token. Both registration modes
use the same token records and administration API:

| Relay setting | Enrollment |
| --- | --- |
| `-require-registration-token=true` (default) | The administrator creates a token using only a name. The first successful signed registration associates its Server ID. |
| `-require-registration-token=false` | A server may register without a token. After verifying its signed identity, the relay creates a token associated with its Server ID and returns it to Spool for local storage. |

Neither mode needs the administrator to pre-bind a Server ID. Reconnecting with
a token must prove possession of the associated server's private key. Automatically
issued tokens also work if the relay later enables the token requirement. Each
server has at most one active token record. The administrative credential remains
mandatory in both modes.

To allow automatic registration, start with:

```sh
./spool-relay -listen 127.0.0.1:28080 -data-dir ./relay-data \
  -require-registration-token=false
```

In Spool Desktop, leave the registration token empty and click **Register** to
request automatic registration. Spool saves the returned token only after the
relay confirms enrollment. Registration does not enable relay ingress. An explicitly supplied
invalid or revoked token is rejected even when automatic registration is enabled.

The administrative HTTP API listens on `127.0.0.1:28081` by default (set with
`-admin-listen`). It is separate from the public relay listener. Use it on the VPS
or through an SSH tunnel. Every request requires the administrator credential,
which stays on the VPS/operator machine and is never entered into Spool Desktop.
This is a JSON API; a browser management dashboard is not included.

```sh
# Run on the VPS, with SPOOL_RELAY_ADMIN_TOKEN set to its configured value.
# Create one credential for each server. The response returns its token once.
curl --fail-with-body http://127.0.0.1:28081/v1/registrations \
  -H "Authorization: Bearer $SPOOL_RELAY_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"home-mac"}'

# List names, registration IDs, associated Server IDs, connection counts,
# and revocation times. Unused tokens have no associated Server ID yet.
curl --fail-with-body http://127.0.0.1:28081/v1/registrations \
  -H "Authorization: Bearer $SPOOL_RELAY_ADMIN_TOKEN"

# Revoke by the registration.id returned above, not the server's identity.
curl --fail-with-body -X DELETE \
  http://127.0.0.1:28081/v1/registrations/REGISTRATION_ID \
  -H "Authorization: Bearer $SPOOL_RELAY_ADMIN_TOKEN"
```

`POST /v1/registrations` accepts `{"name":"..."}` and returns HTTP 201 with
`{"registration":{"id":"...","name":"...","created_at":"..."},"token":"..."}`.
Save the `token` in the corresponding Spool Desktop. `GET /v1/registrations`
returns `{"registrations":[...]}`; `GET /v1/registrations/{id}` returns one entry.
Automatically enrolled entries also appear here with `automatic: true` and the
associated `server_id`. The `id` is the public handle of a token record, used for
management instead of putting the secret token in a URL. Responses never include
token hashes or previously issued token values.
`DELETE /v1/registrations/{id}` returns HTTP 204 and is idempotent.

Revocation closes that server's control connection and all its current relay
data connections, and prevents reconnection. Other servers keep working. The
revocation is persisted before success is returned; storage failures return
HTTP 500 instead of claiming success. Verified LAN connections and Spool account
permissions are managed by the home server and are unaffected by relay revocation.
Clearing a revoked token does not let the same Server ID automatically enroll
again, including after restart. The administrator must issue a replacement token.

To replace a lost or leaked credential, revoke its registration, create another,
and save the new token on that Spool server. Its identity and phone pairing stay
the same. Before first registration, a token can be claimed by whoever possesses
it, so deliver each newly issued token to its intended server. If an automatic
enrollment reply was lost before Spool saved its token, retrying the same signed
identity rotates the credential on the same active record. Revoked records cannot
use this recovery path. The state file retains revoked records (up to 10,000
registrations / 8 MiB in this version).

Automatic registration admits any new signed server identity up to the configured
limits. Revocation blocks the associated identity; it is not a ban on someone
creating an entirely new key. Use the default token requirement to control which
new servers may enroll.

The previous shared relay registration secret is no longer accepted by the
broker. For migration, configure `SPOOL_RELAY_ADMIN_TOKEN` and persistent storage
on the VPS, then issue and install a separate token for every home server. On
the home server, `SPOOL_RELAY_REGISTRATION_TOKEN` still names its own issued token.

## Desktop and Android pairing

1. In **Spool Desktop → Service → Spool Relay**, enter the relay HTTPS origin
   and a free secure LAN port (default 28082). The enable switch stays at the top
   and is disabled until registration succeeds.
2. The relay address box includes **Relay registration token** with one **Register**
   button. Enter a token issued through the VPS administrative API, or leave it
   empty for automatic registration. Clicking **Register** contacts the relay,
   verifies the server identity, and saves the credential after confirmation.
   Failed or canceled attempts leave the switch disabled. Successful registration
   hides the registration form; the relay address stays visible.
3. Enable Spool Relay, then restart the Spool service. Connection status appears
   immediately to the left of the enable switch, without a separate status row.
4. Click **Show pairing QR code**. In Android, add/select a server, open its
   settings, then click **Scan pairing QR code** under **Spool Relay**.
5. Compare the address and SHA-256 identity fingerprint on both devices. Confirm,
   then log in with an ordinary Spool account. The bundled scanner requests camera
   permission; camera frames stay on the device. Manual code entry also works.

Allow the secure LAN TCP port and mDNS UDP 5353 on the home computer's local
firewall. Do not forward those ports on the router. Guest Wi-Fi isolation can
prevent LAN connectivity; the phone then uses the relay.

Headless configuration, from the main Spool repository while the service is stopped:

```sh
# Set this server's SPOOL_RELAY_REGISTRATION_TOKEN, or leave it empty for automatic enrollment.
# Register, configure and print a pairing code without starting relay ingress.
go run ./cmd/server -data-dir /path/to/spool-data \
  -relay -relay-register -relay-url https://relay.example.com -relay-lan-port 28082 -relay-pair
# Start using the saved credential.
go run ./cmd/server -data-dir /path/to/spool-data
```

Development commands must use the repository cache/scratch environment described
in `../AGENTS.md`. Compiled release binaries do not need Go at runtime.
Desktop stores the registration token in `data-dir/relay/registration-token`
with owner-only permissions; this overrides the environment value. The
registration confirmation in `data-dir/relay/registration.json` binds the relay
origin, server identity and token digest. A stored token alone does not unlock the
switch. Registration survives restart; changing the origin or identity requires
registration again. A network outage preserves confirmation; an explicit relay
rejection makes registration available again.
The stable TLS identity and admission capability are in `data-dir/relay/identity.json`.
Back up that file with your private Spool data. Replacing it requires re-pairing.

## Public Web / PWA access

The relay can publish each connected Spool server that enables Web access under
its stable Server ID:

```text
https://relay.example.com/web/<server-id>/
```

The ID is part of the URL path. One relay HTTPS address and certificate serve
all connected servers; no per-server domain, wildcard DNS, browser extension or
Tailscale client is needed. The existing HTTPS reverse proxy must preserve the
Host header and the complete `/web/<server-id>/` path, support WebSocket upgrades,
and stream SSE without buffering. The Caddy configuration above already forwards
all paths this way. Do not strip `/web/` with a path-rewriting proxy rule.

1. Upgrade both the relay and the home Spool server, then register and enable
   Spool Relay as described above.
2. In **Spool Desktop → Service → Spool Relay**, enable **Public Web / PWA access**.
3. Restart the Spool service to apply its Web setting. On connection, the relay
   verifies the registration and signed Web opt-in, then automatically adds the
   route and returns its URL. **Public Web address** appears inside the Web access
   section, with a copy icon beside it. Sign in with an
   existing Spool user account. An HTTPS browser can then install the PWA.

Web routes are runtime state in the relay's authenticated host registry. There
is no static Server ID list to configure. A new server can join while the relay
keeps running; stopping a host or revoking its registration removes its route
and closes its active tunnels. Reconnecting restores the same path if Web access
is still enabled. Relay restarts rebuild routes as hosts reconnect; enrollment
records remain persistent. Registering a server without connecting it does not
publish Web access.

For a headless Spool installation, `-relay-web` persists the same setting:

```sh
spool-server -data-dir /path/to/spool-data -relay -relay-web
# Disable public Web access while retaining native relay access:
spool-server -data-dir /path/to/spool-data -relay-web=false
```

Use the actual name of your compiled server binary. The saved relay URL and
registration are reused. Public Web access defaults to off on Spool.
Changing the home Web setting requires a Spool service restart, with no relay
configuration change or restart. A disconnected or disabled server
returns an unavailable response. An already installed PWA displays its cached
offline page, without caching account or chat data.

**Trust model:** the relay terminates browser HTTPS and can read Web requests,
login cookies, files and messages. It connects to the home server over a tunnel
with the registered server's TLS key pinned. Only enable Web access on a relay
you trust. Native Android connections retain their end-to-end pinned TLS.

All Web mounts on one relay share a browser origin. Path-scoped cookies, PWA
registrations, caches and preferences let multiple servers work in separate
tabs, but paths are **not a security boundary between untrusted server operators**.
Only co-host mutually trusted server operators. The existing relay registration
policy controls who may connect and publish Web: with required tokens, issue
credentials only to trusted operators. With automatic registration, any server
that can enroll may also opt into Web access. Its host must explicitly enable
Web and sign that choice; native-only hosts do not publish Web routes.

The Server ID is a routing identifier, not a password. Spool still enforces login,
CSRF, project membership and remote desktop permissions. The relay does not
create user accounts or expose desktop management APIs. Logout revokes the
current application session; registration revocation closes the server's native
and Web tunnels. Cookies, uploads, downloads, chat SSE, workspace WebSockets and
remote desktop viewers all use the same server prefix. Push notifications return
to that server's PWA scope.

## Routing and security

```text
Android ── pinned TLS ─────────────────────────────── home Spool LAN listener
   │                                                       ▲
   └── WSS to VPS ── opaque TLS bytes ── WSS from home ──────┘
```

- Android probes only resolved DNS-SD (`_spool._tcp`) candidates and private IP
  hints from pairing. There is no subnet scan. LAN attempts take at most 1.2
  seconds before relay fallback, before sending any HTTP request.
- Advertisements, IPs, names and SSIDs do not prove identity. TLS 1.3 verifies the
  paired Ed25519 key, hostname, signature and certificate validity before sending
  credentials. Both paths use one stable HTTPS origin and private login session.
- Login, CSRF, workspace hello, project permissions and separate desktop grants
  use the original Spool handlers. Logout/revocation closes authorized streams.
  On native Android connections, the relay sees none of the inner URLs, cookies,
  messages, files or video. The opt-in Web gateway has the trust model above.
- Every HTTP connection, SSE subscription and WebSocket has an independent data
  channel. Multiple clients and concurrent chat/events/files/desktop do not share
  a byte queue. Large binary frames use bounded buffers, without base64 encoding.
- DNS-SD updates retire idle connections. Active requests/streams finish on their
  current path. Recovery reconnects and resubscribes; it does not migrate an
  in-flight upload or replay an uncertain mutation. A disconnected desktop view
  retains the existing explicit-open behavior.
- Pairing grants relay admission, not application login. Registration tokens and
  server private keys are never in the QR. Share codes with intended clients.
  A retained code cannot restore a revoked Spool login.

## Protocol

Native WebSocket endpoints reject browser Origin headers. Bearer values are
headers, never URL parameters. Compression is disabled.

- `/v1/register`: performs the signed registration handshake and returns `ready`
  after saving the token/identity association. It does not publish the home server
  as connected or accept client traffic, and does not replace an existing host.
- `/v1/host`: requires an active per-server registration token by default. The relay sends
  a random challenge; the host signs a domain-separated challenge plus its
  admission token using its identity key. The first valid use associates the
  identity with this credential, persisted before `ready`. Later registrations
  must use that identity. Revoked credentials, duplicate active identities and
  another identity using an already claimed token are rejected. When automatic
  registration is enabled, a missing Authorization header starts identity-based
  enrollment; `ready.registration_token` contains the newly issued credential.
- `/v1/connect/<server-id>`: requires the server's admission token. The relay
  sends the host `open` with a random connection ID and one-time data secret.
- `/v1/data/<connection-id>`: the host supplies the one-time secret; the relay
  joins the sockets. Pending joins expire after 10 seconds. Control loss closes
  that host's data connections.
- `/web/<server-id>/...`: proxies HTTP, SSE and WebSocket requests only for a
  connected, authenticated server that opts into Web. Host registration signs
  `web_origin` using a separate proof domain; the relay dynamically publishes the
  route and `ready.web_url` confirms the public URL.
  Web-disabled hosts retain the original native registration protocol. Upgrade
  both components before enabling Web; older relays cannot verify the Web proof.

Binary messages carry fragments of the inner TLS byte stream, at most 64 KiB
each; they are not application-message boundaries. Control messages are at most
4 KiB. There is no destination host/port parameter or general HTTP proxy.

Standalone verification: `go test -race ./...` and `go vet ./...` in this module,
with the repository's cache/scratch wrapper. Integration coverage is in
`internal/workspace/relay_test.go`, `android/transport/spoolnet/relay_test.go`, and
Android's `RelayPairingTest`.
Web coverage is in `web_test.go`, `internal/workspace/relay_web_test.go`, and
`web/test/app-path.test.mjs` / `web/test/web-push.test.mjs`. After building the
frontend, set `CHROME_BIN` to a standalone Chromium binary and run
`go test ./internal/workspace -run '^TestRelayWebBrowser$'` from the main repository
using its cache/scratch wrapper to check two concurrent Web/PWA mounts.
