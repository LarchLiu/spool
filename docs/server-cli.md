# Spool Server CLI

`spool-server` runs the same workspace backend as Desktop, with embedded Web/PWA
assets. It needs no GUI, GTK, WebKit, X11 or voxl. Install and authenticate Codex
and/or Claude Code on the service machine, as the OS user running Spool. Git and
an authenticated GitHub CLI are also needed for Issue automation.

## Installation and releases

Releases include `spool-server-VERSION-OS-ARCH.tar.gz` for Linux, macOS and Windows,
with amd64 and arm64 variants. Windows uses `spool-server.exe`. These are separate
from the Desktop archives and do not use the Desktop `install.sh`. Each server
archive has a detached `.sig`; `SHA256SUMS` covers all published assets. The
signature is Ed25519 over the archive's binary SHA-256 digest, using the same
trusted release public key as Desktop. Verify with a public key obtained through
a trusted channel before installing. For example, with OpenSSL 3:

```sh
openssl dgst -sha256 -binary spool-server-VERSION-linux-amd64.tar.gz > archive.sha256
openssl pkeyutl -verify -pubin -inkey trusted-release-public-key.pem -rawin \
  -in archive.sha256 -sigfile spool-server-VERSION-linux-amd64.tar.gz.sig
tar -xzf spool-server-VERSION-linux-amd64.tar.gz
mkdir -p ~/.local/bin
install -m 755 spool-server ~/.local/bin/spool-server
```

No Go, Bun or separate frontend directory is needed at runtime. To build from
source, follow the repository's cache/scratch environment instructions and run
`make server`. It builds production Web assets and `build/spool-server`.
`RELEASE_TAG=vVERSION bun tools/package-server.mjs linux-amd64` creates a static
headless release archive after the frontend has been built and `mygo.json` has
the matching version. The packaging script manages its own build scratch space.

## Help and data directories

```sh
spool-server --help
spool-server help project
spool-server project member set --help
spool-server help issues set
spool-server version
```

Every command and command group has `-h`/`--help`; `help` accepts a nested command
path. Help, version and syntax-error handling do not open the database. Running
without arguments prints help. Options belong after their command; the global
`--data-dir PATH` works before or after the command. Boolean values use
`--option=true` or `--option=false`. Text values can be cleared with `--option=''`.
Commands do not accept positional resource names: use the documented selectors.

Production data defaults to `~/.spool`, development builds to `~/.spool-dev`.
`SPOOL_HOME` overrides the default and `--data-dir` overrides both. The database
is `<data-dir>/db/spool.sqlite`. Only one Desktop/server/administration command
may own a data directory at a time. **Stop the service before administration,
then restart it.** After an unclean crash the lease can take 30 seconds to expire.
One-shot management commands do not start background agent discovery.

Commands return JSON except help, version and foreground service output.
Errors go to stderr. Exit codes are 0 for success/help, 2 for invalid command
syntax/missing required options, and 1 for operational errors. Configuration
output can contain a credential-bearing proxy URL; keep it private.

## Initial setup and serving

```sh
SPOOL_NEW_PASSWORD='replace-with-a-long-unique-password' \
  spool-server user add --username alice --nickname Alice
spool-server project add --name Example --directory /srv/example --members alice
spool-server serve --host 127.0.0.1 --port 8787
```

The service stays in the foreground; SIGINT/SIGTERM saves active turns and shuts
it down. Visit `http://127.0.0.1:8787` locally, or use an SSH tunnel. Remote
browser/PWA access should use an HTTPS reverse proxy with a matching
`--public-url https://chat.example.com`, embedded Tailscale, or the trusted Relay
Web gateway. The proxy must support WebSocket and streaming responses. Spool
login and project permissions apply to all network entries.

`serve` accepts the same network/runtime options as `config set`. Explicit
options persist; omitted options keep their saved values.

## Local administration

| Command | Purpose |
| --- | --- |
| `config show`, `config set` | HTTP, public origin, idle timeout, history cache, proxy, Tailscale and Relay settings |
| `user list/add/update/enable/disable` | Login accounts and password resets |
| `project list/show/add/update/archive/unarchive` | Local directories, sharing and file access |
| `project member set/remove` | Individual membership and shared/personal chat permissions |
| `agent list/set/refresh` | Codex/Claude enablement, executable paths and discovery |
| `github show/set/check` | GitHub CLI override and authentication check |
| `issues show/set` | Per-project Issue automation and last-run diagnostics |
| `relay status/register/unregister/devices/revoke/pair` | Relay credentials and Android pairing |
| `tailscale login/enroll` | Browser enrollment or Auth Key enrollment |

Use command help for every available option and its defaults. Project selectors
accept an ID or an unambiguous name. User selectors accept a username or ID.
`project list` includes local paths, member IDs and both permission maps.
Account edits revoke existing login sessions; disabling retains history.
Archiving a project retains its files and chats. There are no destructive
database/file deletion commands.

```sh
spool-server user update --user alice --nickname 'Alice Smith'
SPOOL_NEW_PASSWORD='another-long-unique-password' \
  spool-server user update --user alice --password
spool-server user disable --user alice
spool-server user enable --user alice

spool-server project member set --project Example --user bob \
  --access read-only --own-access read-only
spool-server project update --project Example --shared-chats=true
spool-server project member remove --project Example --user bob

spool-server config set --idle-minutes 30 --history-cache-mib 128
spool-server agent set --backend codex --path /usr/local/bin/codex --enabled=true
spool-server agent refresh --timeout 1m
spool-server github set --path /usr/local/bin/gh
spool-server github check
spool-server issues set --project Example --owner alice --enabled=true \
  --interval 60 --labels 'bug,enhancement' --backend codex
spool-server issues set --project Example --enabled=false
```

Updates preserve omitted fields. `project update --members` replaces the member
list; individual member commands preserve existing permissions unless explicitly
changed. Issue repositories are detected from Git origin. Enabling automation
requires a working authenticated `gh`; owner/assignees must be project members.
`issues show` prints persisted next/last run times, errors and the latest task.
For labels containing commas, use repeated `--label 'UI, v2' --label 'backend'`
instead of `--labels`; both forms replace the saved filter.
`agent refresh` prints discovery failures in its per-agent JSON; `github check`
exits nonzero if GitHub CLI is not ready.

## Tailscale and Relay

```sh
spool-server config set --tsnet=true --tsnet-hostname build-host
spool-server tailscale login
# Or use an Auth Key, enroll once and exit:
SPOOL_TSNET_AUTH_KEY='tskey-auth-...' spool-server tailscale enroll --timeout 1m
spool-server serve
```

`tailscale login` prints a URL and stays running so browser authorization can
finish. `tailscale enroll` saves the node identity, then exits; the Auth Key is
not persisted. Enable MagicDNS and HTTPS certificates in Tailscale first.

```sh
spool-server config set --relay-url https://relay.example.com
# Token optional if the relay permits automatic registration:
SPOOL_RELAY_REGISTRATION_TOKEN='...' spool-server relay register --name build-host
spool-server config set --relay=true --relay-web=true
spool-server relay pair
spool-server relay devices
spool-server relay revoke --device FULL_FINGERPRINT
```

`relay pair` temporarily owns the running service, prints an invitation, and
requires local fingerprint confirmation. After pairing it stays serving until
interrupted. Stop it before other management commands. Relay credentials stay
in private files, outside JSON settings/status. See [relay deployment](../relay/README.md)
for the independent VPS component.

## Linux systemd user service and updates

The Linux archive includes `spool-server.service`, also available in
[`deploy/`](../deploy/spool-server.service). Install the executable first and
finish initial configuration, then:

```sh
mkdir -p ~/.config/systemd/user
install -m 644 spool-server.service ~/.config/systemd/user/spool-server.service
systemctl --user daemon-reload
systemctl --user enable --now spool-server
journalctl --user -u spool-server -f
```

The unit runs as your development user, uses `~/.spool`, and optionally reads
`~/.config/spool/server.env` for environment variables. Save explicit agent paths
if your Node/agent installation is outside its PATH. For operation after logout,
an administrator can run `sudo loginctl enable-linger USERNAME`.

For administration or a manual binary update, `systemctl --user stop spool-server`,
run the relevant CLI commands or verify/install the new matching archive, then
`systemctl --user start spool-server`. Server has no built-in self-updater.

## Compatibility

Existing flat flags (for example `spool-server -host 127.0.0.1 -port 8787`) still
work. `spool-server legacy --help` lists them. Bare `spool-server` now prints
help; use `spool-server serve` in service units. The structured CLI does not
include voxl or Desktop-only UI/update operations.
