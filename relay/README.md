# Spool Relay

An independent Go relay for Spool servers behind a home router or CGNAT. Deploy
this directory on a VPS. It does not run Spool, agents, a database, or voxl.
Both the home server and Android make outbound connections to the VPS; there
is no hole punching, Internet P2P, or port forwarding at home.
An optional Web gateway also serves browsers and installed PWAs at
`https://<server-id>.web.relay.example.com/` through the same outbound connection.

## Docker Compose

The included `compose.yaml` runs the independent relay on either a local Docker
host or a VPS. From this directory, configure it once and start it:

```sh
cp .env.example .env
chmod 600 .env
openssl rand -hex 32
# Put the generated value in SPOOL_RELAY_ADMIN_TOKEN in .env.
# Set RELAY_PUBLIC_HOST, RELAY_ADMIN_HOST and RELAY_PUBLIC_URL; configure DNS.
# COMPOSE_PROFILES=caddy includes Caddy; leave it empty for an existing proxy.
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
| `RELAY_PORT` | `28080` | Host port for the HTTP relay backend. |
| `RELAY_ADMIN_PORT` | `28081` | Host loopback port for the management API. |
| `COMPOSE_PROFILES` | Empty in Compose; `caddy` in example | Start the optional bundled Caddy. |
| `RELAY_PUBLIC_URL` | Empty | Deployment service HTTPS origin; read-only in the dashboard. |
| `CADDY_CERTIFICATE_MODE` | `individual` | Bundled Caddy only: `individual` or `wildcard`. |
| `RELAY_PUBLIC_HOST` / `RELAY_ADMIN_HOST` | Required for bundled Caddy | Public service and management hostnames. |
| `CLOUDFLARE_API_TOKEN` | Empty | Bundled Caddy wildcard DNS credential; never passed to Relay. |
| `RELAY_TRAEFIK_TOKEN` | Empty | Independent read-only token for Traefik config discovery. |
| `RELAY_TRAEFIK_RESOLVER` | `letsencrypt` | Existing Traefik resolver for Relay routes; `none` uses loaded certificates. |
| `RELAY_PROXY_UPSTREAM` | `http://relay:28080` | Upstream URL exposed to Traefik; use a stable Docker network alias. |
| `RELAY_PROXY_PORT` | `28082` | Host loopback port for private proxy integration. |

See [Web subdomain deployment](relay-web-subdomains.zh-CN.md) before
enabling public Web, including both certificate modes and external proxy setup.

For automatic registration, set `RELAY_REQUIRE_REGISTRATION_TOKEN=false` in
`.env` and run `docker compose up -d`. The administrative credential remains
required. Leave the registration token empty in Spool Desktop and click
**Register**; Spool saves the server token returned by the relay.

Open `http://127.0.0.1:28081/` on the Docker host and enter the administrative
credential from `.env`. The dashboard generates server tokens, lists server
status and registration IPs, and revokes authorization. The
[administration examples below](#registration-administration)
use the same default port for Compose and standalone deployments.

Container-based reverse proxies such as Coolify should target container port
`28080` for relay traffic and `28081` for the administrative API. `RELAY_ADMIN_PORT`
changes only the host port mapping; the container's administrative port stays
`28081`. When updating an existing deployment, update proxy targets and explicit
listener overrides to use the new ports. Existing `.env` values and saved Spool
LAN settings retain their configured ports; use `28080` for `RELAY_PORT`, `28081`
for `RELAY_ADMIN_PORT`, and `10095` for the secure LAN listener to adopt the defaults.
Compose already binds both listeners inside the container. When deploying the
Dockerfile directly, add `-admin-listen 0.0.0.0:28081` to the start command to
let a separate proxy container reach management; keep its host mapping private.

The relay HTTP port and the administrative port are separate. Put an HTTPS
reverse proxy in front of `127.0.0.1:28080`, as in the Caddy example below, and
enter that HTTPS origin in Spool Desktop. Android pairing requires an HTTPS
origin with a certificate trusted by the app; a LAN HTTP address does not work.
The default loopback mapping is accessible only from the Docker host. Running
the container at home does not itself create a publicly reachable endpoint.

Set `RELAY_PUBLIC_URL` in the deployment environment. Web addresses use
`<server-id>.web.<public hostname>` automatically. The dashboard displays the
origin and derived suffix read-only; changing the origin requires redeployment
and matching proxy/DNS configuration. Startup waits up to five minutes for the public
HTTPS entry before detecting the proxy once; **检测** retries it. The button stays
disabled while waiting or detecting. Manual trusted CIDRs remain editable under
**高级设置**.

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
RELAY_PUBLIC_URL=https://relay.example.com
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
export RELAY_PUBLIC_URL=https://relay.example.com
./spool-relay -listen 127.0.0.1:28080 -data-dir ./relay-data
```

Point `relay.example.com` at the VPS and put a TLS reverse proxy in front.
For a public relay without a public dashboard, a Caddyfile is:

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
After a network change it takes over its previous control connection once that
connection fails a three-second liveness probe, without waiting for the
heartbeat. A second running instance of the same identity is refused instead.
For direct TLS, pass `-listen :443 -tls-cert fullchain.pem -tls-key privkey.pem`.
Proxy trust is detected at startup from `RELAY_PUBLIC_URL`. Add explicit extra
CIDRs in the dashboard when necessary. Forwarded
chains are read from right to left, stopping at the first untrusted address. Other
forwarding headers and client cookies cannot choose the login rate-limit key.

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
loopback address, including in this Docker example. To publish the dashboard,
use the dedicated routes in the management section below.
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
# Set SPOOL_RELAY_ADMIN_TOKEN and RELAY_PUBLIC_URL (see deploy/relay.env.example).

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

The management dashboard and HTTP API listen on `127.0.0.1:28081` by default
(set with `-admin-listen`), separately from the public relay listener. Open
`http://127.0.0.1:28081/` on the VPS, or forward it to your own computer:

```sh
ssh -N -L 28081:127.0.0.1:28081 user@your-vps
# Open http://127.0.0.1:28081/ on your computer.
```

Enter `SPOOL_RELAY_ADMIN_TOKEN` to sign in. It stays only in the current page's
memory; refreshing or signing out requires entering it again. No browser cookies
or persistent storage grant management access. Remote browser access requires
HTTPS. This credential is never entered into Spool Desktop.

Give each token a server name, select **生成 token**, then copy the once-only
result into that Spool server's registration settings. The list shows online,
offline, unused, unregistered and revoked entries, historical registration IPs,
Server IDs and active connection counts. **撤销** asks for confirmation, then
disconnects the selected server and disables its token. Inactive records remain
visible. The list refreshes every ten seconds; proxy detection does not run on a
timer.

### Public management and proxy detection

Use separate HTTPS hostnames for the dashboard and public Relay traffic. The
management hostname routes to port `28081`; the public Relay hostname routes
to port `28080`, which serves native protocol traffic, the Web gateway and
proxy-check callbacks. Application JavaScript must not share the dashboard's
browser origin. Relay also blocks tenant Web content on the saved HTTPS
management origin after dashboard setup.

For example, publish management at `https://relay-admin.example.com` and the
public service at `https://relay.example.com`:

```caddyfile
relay-admin.example.com {
    reverse_proxy 127.0.0.1:28081
}

relay.example.com {
    reverse_proxy 127.0.0.1:28080 {
        flush_interval -1
    }
}
```

For Docker-based proxies, replace the loopback targets with the relay container's
address and the same container ports. Keep the host's management mapping private.
For ngrok or Cloudflare Tunnel, configure the management hostname to forward to
`28081` and the service hostname to forward to `28080`. Forward the original
Host, set `X-Forwarded-Proto: https`, and replace or safely append
`X-Forwarded-For`. Forwarding only to the management port cannot verify the
public Relay listener.

Set `RELAY_PUBLIC_URL=https://relay.example.com` in the deployment environment.
The dashboard displays it read-only and records its management origin separately.
Startup waits up to five minutes for a valid HTTPS `/healthz` response and
immediately validates a one-time callback without credentials or redirects. It
persists and immediately trusts only the observed TCP peer as an exact /32 or
/128. Direct TLS needs no proxy entry. **检测** retries; there is no
periodic probing. A failed retry preserves the verified result for the same
origin. Redeployment with a different or empty URL discards old automatic trust.
The callback `/.well-known/spool-relay/proxy-check/*` must reach port 28080.
See [deployment configuration and migration](relay-web-subdomains.zh-CN.md#配置文件与动态管理)
for environment precedence and settings-file migration.

The Web gateway routes individual servers by subdomain. Those hostnames
can share the same public IP and backend port `28080`. Configure the base
hostname as the detection address and route its callback to that port too.
One verified peer covers hostnames using the same proxy path and backend process;
DNS alone does not prove that two hostnames use the same proxy. Extra ingress
paths or different proxy peers need their own explicit trusted CIDRs. This
setting configures proxy trust independently from DNS and HTTPS routing.

**额外可信代理** is for explicitly trusted extra hops in a multi-proxy chain.
The detected peer remains separate and is replaced automatically after a
successful startup check. Manual CIDRs persist; no proxy-trust environment variable or startup flag is needed. These settings and server
registrations use the same persistent data directory/volume; the administrator
credential stays in the process environment. Back up this volume when migrating.

### Administrative API

The API remains available to scripts. Every API request requires a Bearer
administrator credential; browser callers must be same-origin. Tokens and
credentials are never accepted in URL query strings.

`GET /v1/settings` returns the deployment URL and subdomain suffix, saved
management origin, manual CIDRs, verified peer/origin/time, and detection status.
`PUT /v1/settings` accepts only `browser_origin` and optional
`trusted_proxies` (an array of CIDRs). Omitting the array preserves it; an empty
array clears manual entries. Deployment and detection-owned fields are read-only.
`POST /v1/proxy-detection` retries the deployment URL and returns HTTP 202; poll
`GET /v1/settings` for completion.
`PATCH /v1/registrations/<id>` with `{"web_blocked":true}` stops Web publication
without revoking native registration; `false` allows the host to opt in again.
The host reconnects to update its displayed URL; existing streams close.

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

Spool may supply an optional name during registration (up to 128 UTF-8 bytes,
without control characters). A blank name preserves the pre-issued token's name;
new automatic registrations default to `Spool-<first 12 characters of Server ID>`.
The relay records `registration_ip` from the **Spool Server's successful
registration request**, never from the administrator creating a token, a phone,
or a client-supplied JSON field. It persists this IP and returns it through both
management GET endpoints. Ordinary reconnections and token recovery preserve it.
Older records have no IP until an explicit registration succeeds; an ordinary
reconnect cannot reconstruct their historical registration IP.

For example, inspect names and registration IPs through the management listener:

```sh
curl -fsS https://relay-admin.example.com/v1/registrations \
  -H "Authorization: Bearer $SPOOL_RELAY_ADMIN_TOKEN" \
  | jq '.registrations[] | {id, name, server_id, registration_ip}'
```

By default the IP comes from the socket peer. The dashboard can detect the
nearest proxy automatically as described above. For extra manual trust,
use the dashboard advanced settings to add actual proxy CIDRs, for example `127.0.0.1/32,::1/128` for a local proxy. Do not copy unrelated private
ranges or trust all addresses. The relay follows
`X-Forwarded-For` from right to left through trusted hops, stopping at the first
untrusted address. A Cloudflare + Coolify chain requires both trusted proxy hops
and correctly preserved forwarding headers to reach Spool's public egress IP.
Otherwise the recorded address is the nearest untrusted proxy. Configure the
reverse proxy to sanitize incoming forwarding headers. `CF-Connecting-IP` and
`X-Real-IP` alone are not accepted as proof of a client's address.

Desktop's **Unregister** action authenticates with the saved registration token
and a fresh signature by the server identity. It disconnects that server, marks
the record with `unregistered_at`, and disables the old token. Repeating the
same operation is safe even after a relay restart or a lost response. Other
registrations remain active. A self-unregistered server may register again
(with a new token when tokens are required); administrative revocations still
block automatic enrollment. Inactive records remain in the management list.
The relay reads version 1 stores and writes version 2 on the next mutation.
Older relay binaries reject version 2, preventing a downgrade from accidentally
reactivating self-unregistered credentials. Keep the updated relay when reusing
this volume.

Revocation closes that server's control connection and all its current relay
data connections, and prevents reconnection. Other servers keep working. The
revocation is persisted before success is returned; storage failures return
HTTP 500 instead of claiming success. Verified LAN connections and Spool account
permissions are managed by the home server and are unaffected by relay revocation.
Clearing a revoked token does not let the same Server ID automatically enroll
again, including after restart. The administrator must issue a replacement token.

If Spool loses its local registration record but keeps its identity, registering
again with the original active token restores enrollment without interrupting
the existing host. To replace a lost or leaked credential, create a new token
and register it on that same Spool server. The signed `/v1/register` request
retires the identity's previous active credential and binds the new one in a
single durable write, then disconnects the old host and its relay streams.
The previous record is retained with `unregistered_at`; other servers remain
connected. A failed write leaves the old credential and connections active.
Retrying the new token preserves the recovered registration. The server identity
and phone pairing stay the same; ordinary `/v1/host` connections cannot perform
this replacement. For a leaked credential, administrators can still revoke it
immediately before issuing a replacement.

If the identity key is also lost, obtain a new token and pair phones again.
An already claimed token cannot bind a different identity; the administrator
must retire the old server's record separately. Before first registration, a
token can be claimed by whoever possesses it, so deliver each newly issued
token to its intended server. If an automatic enrollment reply was lost before
Spool saved its token, retrying the same signed
identity rotates the credential on the same active record. Revoked records cannot
use this recovery path. The state file retains revoked and unregistered records (up to 10,000
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
   and a free secure LAN port (default 10095). The enable switch stays at the top
   and is disabled until registration succeeds.
2. The relay address box includes **Relay registration token** with one **Register**
   button. Enter a token issued through the VPS administrative API, or leave it
   empty for automatic registration. Clicking **Register** contacts the relay,
   verifies the server identity, and saves the credential after confirmation.
   Failed or canceled attempts leave the switch disabled. Successful registration
   hides the registration form and shows **Unregister**; the relay address stays
   visible. An optional server name can be entered with the registration token.
   Spool remembers the token and confirmed registration separately for each relay
   origin (scheme, hostname and port), including after an app restart. Disable the
   relay before editing its address. Switching back to a registered origin restores
   its credential and hides the registration form without enrolling again. Old
   single-relay credentials are imported automatically for their original address.
   A rejected or revoked token requires registration again only for that relay;
   temporary network failures preserve the saved registration.
   **Unregister** contacts that relay first, then removes only its local
   credential and switches relay off. A network failure preserves the credential
   for retry. The local Spool service keeps running.
3. Enable Spool Relay, then restart the Spool service. Connection status appears
   immediately to the left of the enable switch, without a separate status row.
4. Expand **Pair Android phone** using the chevron on its right to generate a QR code. In Android, add/select a server, open its
   settings, then click **Scan pairing QR code** under **Spool Relay**.
5. Compare the address and server fingerprint, then confirm on the phone. The
   app creates its own private device key and shows its device fingerprint.
6. Desktop opens a **Device approval** dialog. Compare the phone's name and
   device fingerprint, then choose **Allow device** or **Reject device**.
   Only an approved phone can reach Spool's login. Sign in with an ordinary Spool
   account. The scanner keeps camera frames on the phone; manual code entry uses
   the same approval flow.

Each invitation expires after five minutes and is bound to the first requesting
device key. Refreshing or collapsing the code, or leaving the pairing settings,
cancels requests that have not been approved. An approved phone can still collect
its result using the same private key until the original invitation expires,
even after another invitation is generated. Stopping the service clears pending
requests and uncollected results. Another phone needs a new invitation and
separate approval. Copying a saved connection code alone cannot authorize a
different phone.

Expand **Paired devices** to see each authorized phone and its full fingerprint;
the collapsed row shows the device count.
**Revoke device** immediately closes its LAN and native relay connections,
including HTTP, chat SSE, workspace WebSocket and remote desktop streams, and
rejects reconnects. Other paired devices remain authorized. Approval and
revocation persist in `data-dir/relay/devices.json` with owner-only permissions.
Only public device keys are stored on Spool; neither the Relay VPS nor Desktop
receives a phone's private key. These management operations are available only
through the private Desktop bridge or local server command, never the Web API.

Allow the secure LAN TCP port and mDNS UDP 5353 on the home computer's local
firewall. Do not forward those ports on the router. Guest Wi-Fi isolation can
prevent LAN connectivity; the phone then uses the relay.

Headless configuration, from the main Spool repository while the service is stopped:

```sh
# Set this server's SPOOL_RELAY_REGISTRATION_TOKEN, or leave it empty for automatic enrollment.
# Register, start Spool, print a five-minute invitation, and approve locally.
go run ./cmd/server -data-dir /path/to/spool-data \
  -relay -relay-register -relay-name 'Home Mac' -relay-url https://relay.example.com -relay-lan-port 10095 -relay-pair
# Compare the displayed phone fingerprint and type yes to approve.
# Spool keeps running after approval. On later launches, reuse saved credentials:
go run ./cmd/server -data-dir /path/to/spool-data
# While the service is stopped, list/revoke devices using the compiled binary:
spool-server -data-dir /path/to/spool-data -relay-devices
spool-server -data-dir /path/to/spool-data -relay-revoke <device-fingerprint>
```

Development commands must use the repository cache/scratch environment described
in `../AGENTS.md`. Compiled release binaries do not need Go at runtime.
Desktop stores each origin's token and confirmed server identity in
`data-dir/relay/registrations.json` with owner-only permissions. It migrates the
previous `registration-token` and `registration.json` files once, associating them
with the original relay. An unconfirmed legacy/environment token belongs only to
the origin configured at migration. A stored token alone does not unlock the
switch. Registration survives restart and switching between saved origins;
changing identity requires registration again. A network outage preserves
confirmation; an explicit relay rejection makes registration available again.
The stable TLS identity and admission capability are in `data-dir/relay/identity.json`.
Back up that file with your private Spool data. Replacing it requires re-pairing.

## Public internet access

Each Web-enabled server has an isolated browser origin:

```text
https://<server-id>.web.relay.example.com/
```

The relay operator configures its public HTTPS URL in the deployment environment
and certificates in the front proxy. The Web suffix is always `web.<public hostname>`.
Server IDs produce stable DNS labels. Only a credential-authenticated, signed
v2 opt-in publishes a route.
Native enrollment by itself never publishes Web content.

See [subdomain deployment and migration](relay-web-subdomains.zh-CN.md)
for optional bundled Caddy, external proxies, Coolify HTTP provider setup,
DNS API credentials, and certificate modes. `COMPOSE_PROFILES=caddy` starts
the bundled proxy; an empty value keeps the relay independent. Individual
certificates need no DNS API; the bundled wildcard mode supports Cloudflare DNS.
Certificate credentials belong to the proxy, never the relay data store.

1. Upgrade both Relay and Spool, configure DNS and the proxy once, then register.
2. Enable **Public internet access** in Spool's Relay settings and apply changes.
3. Open the returned **Public Web address**, sign in, and optionally install its PWA.

Publishing intent survives temporary host disconnects and relay restarts, so
certificate authorization does not churn. Requests still require a connected
host. Disabling Web updates the record on the next authenticated host connection;
revocation, unregistration, or the administrator's **停用 Web** removes publishing
qualification. Native identities remain separate from browser accounts.

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
Web/PWA does not participate in phone pairing or require a device private key.
The home server uses separate TLS admission modes: native connections require an
approved client certificate, pairing connections can access only the invitation
endpoint, and gateway connections can access only the locally enabled
`/web/<server-id>/` mount. The Web exception is unavailable on the secure LAN
listener. Disabling Web blocks new gateway requests, including on an existing
HTTP connection; restarting applies the routing change and closes old streams.
Native device revocation does not revoke a separate browser login.

Each server now has its own host-only `__Host-spool_session` cookie,
PWA identity, Service Worker, caches, and storage. Exact Origin and CSRF checks
also reject same-site sibling-domain requests. The gateway strips parent-domain
Cookie attributes, and the browser rejects parent-domain `__Host-` cookie
injection. Web domains cannot route to another server by changing a path.

Legacy public `/web/<server-id>/` mounts return 410 and a site-data clearing
header; they never serve tenant HTML. Open the new address, sign in again, and
reinstall the PWA. Existing cached legacy installations should be removed.
Older clients retain native connectivity but cannot publish shared-origin Web.

The Server ID is a routing identifier, not a password. Spool still enforces login,
CSRF, project membership and remote desktop permissions. The relay does not
create user accounts or expose desktop management APIs. Logout revokes the
current application session; registration revocation closes the server's native
and Web tunnels. Cookies, uploads, downloads, chat SSE, workspace WebSockets and
remote desktop viewers all use the server's own origin. Push notifications return
to that server's PWA scope.

Login limits use the client's IP address reported by the relay over the host's
authenticated control channel, for both Web and native connections. Different
source ports or forged HTTP forwarding headers do not reset a client's limit;
clients sharing a public IP share the limit. Upgrade both the relay and the
home server to apply this behavior. Older components remain compatible, but
retain the shared tunnel-address limit until both are upgraded. The home
server's separate Public HTTPS endpoint keeps its existing behavior.

The gateway reuses a tunnel for consecutive requests from the same client
address, keeping up to four idle for 30 seconds. Requests from different
addresses never share one. Idle tunnels count toward the connection limits and
are released first when a server reaches its limit.

## Routing and security

```text
Android ── pinned mutual TLS ────────────────────── home Spool LAN listener
   │                                                       ▲
   └── WSS to VPS ── opaque TLS bytes ── WSS from home ──────┘
```

- Android probes only resolved DNS-SD (`_spool._tcp`) candidates and private IP
  hints from pairing. There is no subnet scan. LAN attempts take at most 1.2
  seconds before relay fallback, before sending any HTTP request.
- Advertisements, IPs, names and SSIDs do not prove identity. TLS 1.3 verifies the
  paired Ed25519 key, hostname, signature and certificate validity before sending
  credentials. Both paths use one stable HTTPS origin and private login session.
- Every native connection also proves possession of the paired phone's Ed25519
  private key using TLS 1.3 client authentication. Authorization is checked at
  the handshake and on every request; TLS session tickets are disabled. A relay
  operator who knows the Server ID and admission token cannot impersonate an
  authorized phone. Enabling Web intentionally retains the trusted-gateway
  access described above.
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
- Version 2 QR codes contain the relay URL, pinned server key, relay admission
  token, LAN hints and a short-lived invitation. No server/device private key or
  relay registration token appears in them. Once approved, the saved phone code
  drops the invitation and requires its separately stored private device key.
  Pairing does not replace Spool account login or restore a revoked account.
- Upgrade Spool and Android together. Old version 1 codes and clients must pair
  again; there is no legacy device-authentication bypass. The standalone relay's
  opaque native tunnel and Web gateway protocol do not require an update for
  this device-authentication change.

## Protocol

Native WebSocket endpoints reject browser Origin headers. Bearer values are
headers, never URL parameters. Compression is disabled.

- `/v1/register`: performs the signed registration handshake and returns `ready`
  after saving the token/identity association. It does not publish the home server
  as connected or accept client traffic. Retrying the same token preserves an
  existing host; an unused token retires that identity's previous active
  credential and disconnects its host only after the replacement is persisted.
- `/v1/unregister`: requires the registration token and a challenge signature
  specific to unregistration. Invalidates that credential, closes its connections,
  and returns `unregistered` only after persisting the result.
- `/v1/host`: requires an active per-server registration token by default. The relay sends
  a random challenge; the host signs a domain-separated challenge plus its
  admission token using its identity key. The first valid use associates the
  identity with this credential, persisted before `ready`. Later registrations
  must use that identity. Revoked credentials, duplicate active identities and
  another identity using an already claimed token are rejected. When automatic
  registration is enabled, a missing Authorization header starts identity-based
  enrollment; `ready.registration_token` contains the newly issued credential.
- `/v1/connect/<server-id>`: requires the server's admission token. The relay
  sends the host `open` with a random connection ID, one-time data secret and
  `client_ip` metadata resolved from the connection and configured trusted proxies.
- `/v1/data/<connection-id>`: the host supplies the one-time secret; the relay
  joins the sockets. Pending joins expire after 10 seconds. Control loss closes
  that host's data connections.
- `https://<server-id>.web.<public-hostname>/...`: proxies HTTP, SSE and WebSockets
  for an opted-in authenticated host. Registration signs `web_version=2`,
  `web_origin`, the challenge and admission token; `ready.web_url` returns the
  isolated assignment. The home verifies its identity label before accepting it.
- Private proxy listener (default loopback 28082): `GET /v1/tls/allow?domain=...`
  implements Caddy ask; `GET /v1/traefik` returns a complete routing snapshot
  and requires the independent `RELAY_TRAEFIK_TOKEN`. Neither endpoint is served
  by the public or administrative listener.

Binary messages carry fragments of the inner TLS byte stream, at most 64 KiB
each; they are not application-message boundaries. Control messages are at most
4 KiB. There is no destination host/port parameter or general HTTP proxy.

Standalone verification: `go test -race ./...` and `go vet ./...` in this module,
with the repository's cache/scratch wrapper. Integration coverage is in
`internal/workspace/relay_test.go`, `android/transport/spoolnet/relay_test.go`, and
Android's `RelayPairingTest`.
For the embedded dashboard, from the repository root run
`sh tools/run.sh relay-admin-browser env CHROME_BIN=/path/to/chrome bun relay/test/admin-smoke.mjs`.
It uses an isolated real Relay fixture, checks authentication, token issuance and
revocation, automatic proxy verification and mobile layout, and retains screenshots
under `build/artifacts/relay-admin/`. The fixture certificate bypass belongs only
to that disposable browser; the production verifier still validates TLS normally.
Web coverage is in `web_test.go`, `internal/workspace/relay_web_test.go`, and
`web/test/app-path.test.mjs` / `web/test/web-push.test.mjs`. After building the
frontend, set `CHROME_BIN` to a standalone Chromium binary and run
`go test ./internal/workspace -run '^TestRelayWebBrowser$'` from the main repository
using its cache/scratch wrapper to check two concurrent Web/PWA origins.
