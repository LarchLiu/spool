<p align="center">
  <img src="./assets/logo.png" alt="Logo" width="60">
</p>
<h1 align="center">Spool</h1>

[简体中文](README.zh-CN.md)

Spool is a local AI coding workspace for individuals and teams. Use Codex and
Claude Code through Desktop, Web/PWA, or Android, with shared projects, chats,
and permissions across devices.

Tasks run on the machine hosting Spool. Project files and native CLI sessions
stay on that machine; install and sign in to Codex or Claude Code there before use.

![Spool Desktop workspace](docs/images/desktop-workspace.png)

## Features

- **Desktop, Web/PWA, and Android** — Continue the same projects and chats across devices; Android supports multiple servers.
- **Codex and Claude Code** — Independent native sessions, model and reasoning selection, streaming output, stop, and resume.
- **Rich chat and files** — Markdown, code, diffs, tool activity, images, attachments, project files, Git worktrees, and [Word/Excel previews](docs/office-preview.md).
- **Collaboration** — Project members, shared sessions, private messages, group chats, and online presence.
- **Permissions and approvals** — Per-user and project permissions, session access, command/file approvals, and interactive questions.
- **Automation** — Scheduled GitHub Issue processing in isolated worktrees, with pull requests for review.
- **Remote access** — Headless server, HTTPS, embedded Tailscale, relay pairing, and optional relay Web/PWA access.
- **Remote desktop** — Optional [voxl](https://github.com/tembo/voxl) integration, started on demand and authorized per user.
- **Notifications and preferences** — Desktop notifications, Web Push, themes, and English, Simplified Chinese, and Japanese.

![Spool Web workspace](docs/images/web-workspace-dark.png)

## Development

Requires Go **1.27.3+** and Bun **1.4.4**. Run from the repository root:

```sh
make web      # Install frontend dependencies and build assets
make dev      # Start desktop development with hot reload
make desktop  # Build the desktop binary
make server   # Build the headless server
make apk      # Build the Android APK (requires the Android toolchain)
make package  # Build desktop installers
make test     # Run tooling, frontend, Go tests, and vet
```

Build output is written to `build/`.

## Documentation

- [Deployment, Web/PWA, Tailscale, and remote desktop](docs/deployment.md)
- [Server CLI](docs/server-cli.md)
- [Android development](android/README.md)
- [Relay deployment and pairing](relay/README.md)
