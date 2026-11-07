<p align="center">
  <img src="./assets/logo.png" alt="Logo" width="60">
</p>
<h1 align="center">Spool</h1>

[English](README.md)

> Spool 是一个面向多端和多人协作的本地 AI 编程工作区。它把 Desktop、
Web/PWA 和 Android 客户端接入同一个项目与聊天空间，让团队成员可以从不同
设备协作，并共享一致的权限、在线状态和项目上下文。

![Spool Desktop workspace](docs/images/desktop-workspace.png)

Spool 的任务和代码执行发生在服务所在的机器上，项目目录、原生 CLI 会话和
登录凭据也由这台机器上的工具管理。团队成员通过上述客户端连接服务；Spool
不是云端模型服务，使用前需要在服务主机上安装并登录 Codex 或 Claude Code。

## 功能特性

### 多端工作区

- 通过 Desktop、浏览器/PWA 或原生 Android 客户端访问同一个 Spool 服务。
- 在不同设备之间继续使用同一个项目和聊天，不需要复制工作目录或 Agent 会话。
- 服务需要远程访问时，可以使用内置 Tailscale HTTPS 或可信的反向代理。

### 多人协作

- 邀请成员加入项目，并分别控制个人会话和共享会话的权限。
- 查看在线状态，进行私聊，或使用在线群聊和指定成员群聊。
- 共享项目上下文，同时明确管理项目成员、会话控制权和文件访问权限。

### 本地 AI 编程工作区

- 支持 Codex 和 Claude Code，可读取各自 CLI 提供的模型、推理等级和权限模式。
- 每个聊天使用独立的原生 CLI 会话，支持流式输出、停止、恢复和空闲释放。
- 支持 Markdown、代码块、Diff、工具活动、图片和文件附件；可以直接浏览项目文件和 Git worktree。
- 支持 `/goal` 原生目标命令，以及命令、文件和额外权限请求的逐次审批。
- Agent 切换、模型选择和权限设置按聊天保存，历史记录由 Codex/Claude 的原生会话提供。

### 项目与权限

- 用项目组织工作目录、会话、成员和 Agent 配置；数据存储在本地 SQLite。
- 支持用户、项目成员、个人会话和项目共享会话的独立权限控制。
- 项目文件访问始终绑定到项目目录，远程请求不能自行指定工作目录。
- 支持浏览已授权会话的工作区文件、Markdown/HTML 和 [Word/Excel 预览](docs/office-preview.md)（`.docx`/`.xlsx`）、代码查看和有限的外部文件引用。
- 项目共享的是服务主机上的实际文件和操作系统权限，因此应只向可信用户授予项目访问权限。

### 在线状态与通知

- 用户头像、在线状态、主题和语言设置可在支持的客户端中使用。
- 支持 Desktop 系统通知和 Web Push，任务通知显示会话标题，私聊通知显示发送者，群聊通知显示群名和发送者。
- 支持 English、简体中文和日本語，并可跟随系统主题和语言。

### 自动化与远程连接

- 可按计划读取 GitHub Issues，使用 Codex 或 Claude 在隔离 worktree 中处理任务，并创建指向 `dev` 的 PR。
- 支持 headless Go server，适合把服务部署在持续运行的开发机或服务器上。
- 内置可选的 Tailscale (`tsnet`) HTTPS 入口，Android 和远程浏览器可以直接连接 tailnet。
- 可选的 [voxl](https://github.com/tembo/voxl) 远程桌面按需启动，并且需要管理员为具体用户单独授权。

![Spool Web workspace](docs/images/web-workspace-dark.png)

## 开发与编译

### 环境要求

- Go **1.27.3** 或更高版本。
- Bun **1.4.4**（版本固定在 `.bun-version`）。
- 本地安装并登录 Codex CLI 或 Claude Code；只开发前端或运行不涉及 Agent 的测试时可以暂不安装。
- Android 开发环境和 native 客户端要求见 [Android 文档](android/README.md)。

MyGo 固定使用 `v0.6.4`。前端依赖安装在 `web/node_modules/`，生产构建后的资源会嵌入 Desktop 和 server 二进制。

依赖由 `web/bun.lock` 锁定。构建命令统一使用 `bun run --bun`，确保依赖安装脚本、TypeScript 和 Vite 都通过 Bun 运行。

### 本地开发

在仓库根目录运行：

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

`make dev` 会安装并构建 Web 资源，然后启动 MyGo 开发桌面和 Vite 热更新。修改
Go 代码会自动重新编译，修改前端代码会触发 HMR。默认服务端口是
`127.0.0.1:8787`，Vite 开发端口是 `127.0.0.1:5173`。

只运行 Web 开发环境时，可以在两个终端分别执行：

```sh
# 终端 1
GOCACHE="$PWD/build/cache/go-build" go run ./cmd/server

# 终端 2
bun run --bun --cwd web dev
```

Vite 会把 `/api` 请求代理到 `127.0.0.1:8787`。检查 PWA、Service Worker 或
离线行为时，请使用 Go server 提供的地址，不要使用 Vite 地址。

### 构建与测试

```sh
# 安装依赖并构建嵌入式前端资源
GOCACHE="$PWD/build/cache/go-build" make web

# 构建未打包的 Desktop 和 headless server
GOCACHE="$PWD/build/cache/go-build" make desktop
GOCACHE="$PWD/build/cache/go-build" make server

# 构建 MyGo 桌面安装包
GOCACHE="$PWD/build/cache/go-build" make package

# 前端测试、Go race test 和 go vet
GOCACHE="$PWD/build/cache/go-build" make test
```

构建结果位于 `build/`。`make desktop` 生成 `build/spool-desktop`，
`make server` 生成 `build/spool-server`，`make package` 生成对应平台的
桌面应用和安装包。测试使用本地 Mock CLI，不会调用模型推理。

### 项目结构

- `cmd/desktop`：MyGo Desktop 入口、托盘、原生设置和桌面通知。
- `cmd/server`：headless Go server 入口。
- `internal/agent`：Codex/Claude CLI 生命周期、协议和会话处理。
- `internal/workspace`：SQLite、认证、项目权限、聊天、在线状态和服务生命周期。
- `web`：React/TypeScript Web 与 PWA 客户端。
- `android`：原生 Android 客户端及 gomobile 接口。
- `frontend`：构建时生成并嵌入 Go 二进制的 Web 资源。

## 运行服务

开发运行默认使用 `~/.spool-dev`，生产构建默认使用 `~/.spool`。数据库位于
对应数据目录的 `db/spool.sqlite`。可以用 `-data-dir` 或 `SPOOL_HOME` 选择其他
数据目录；同一个数据目录同时只能运行一个 Desktop 或 server 实例。

启动 headless server 前，可以在服务停止时创建初始用户和项目：

```sh
SPOOL_NEW_PASSWORD='replace-with-a-long-unique-password' \
  ./build/spool-server -add-user alex -nickname Alex

./build/spool-server -add-project Example \
  -directory /absolute/project/path -grant alex

./build/spool-server -host 127.0.0.1 -port 8787
```

聊天和管理 API 都要求登录账户；服务不会提供默认密码、远程注册或远程管理 API。

### Web/PWA 与 HTTPS

本机访问可以直接打开 `http://127.0.0.1:8787`。要从其他设备使用浏览器或安装
PWA，应通过 HTTPS 反向代理访问，并在 Desktop 设置或 server 参数中配置完全一致的
Public origin。例如 Caddy：

```caddyfile
chat.example.com {
    reverse_proxy 127.0.0.1:8787 {
        flush_interval -1
    }
}
```

反向代理需要支持 WebSocket 升级、SSE 长连接，并关闭响应缓冲。登录 Cookie、
CSRF、Origin 和项目权限检查仍由 Spool 服务负责。

### Tailscale 与 Android

Desktop 可以在 **Service > Tailscale (tsnet)** 中启用内置 Tailscale；headless
server 可以使用：

```sh
./build/spool-server -tsnet -tsnet-hostname spool-server
```

首次使用需要在 Tailscale 管理控制台启用 MagicDNS 和 HTTPS certificates，并
完成节点登录或批准。服务启动后会打印实际的 HTTPS 地址，把这个完整地址填入
Android 客户端即可。Tailscale 入口仍然要求登录 Spool，且不会绕过项目权限或
CSRF 检查。

## Remote desktop (voxl)

voxl 是可选的远程桌面组件，与项目权限分开管理。只有在本机 Service 设置或
headless CLI 中安装并启用 voxl，并且管理员给某个用户授予远程桌面权限后，用户
才会看到 **Connect to desktop**。打开连接时 voxl 才启动；最后一个查看者断开
后会停止。远程桌面可以控制服务主机的完整桌面，包括 Spool 设置，因此只应授予
可信用户。

可以导入已有的 voxl，也可以让 Spool 构建固定版本的 `voxl-server`：

```sh
# 导入本机已有的 voxl
./build/spool-server -install-voxl \
  -voxl-binary /absolute/path/to/voxl

# 给用户授予或撤销远程桌面权限
./build/spool-server -remote-desktop-user alice \
  -remote-desktop=true

# 启用远程桌面功能
./build/spool-server -remote-desktop=true
```

如果不传 `-voxl-binary`，Spool 会先查找本机已有的可执行文件，找不到时从
crates.io 构建 `voxl-server` **0.3.3**。各平台还需要 voxl 的原生依赖：macOS
需要 Xcode Command Line Tools 和 Rust；Linux 需要 X11/Xvfb、Rust、x264、libyuv、
Opus、PulseAudio 等库；Windows 需要 Rust MSVC、Visual Studio C++ Build Tools、
LLVM 和 vcpkg。macOS 还需要为 Spool 或 voxl 授予 Screen Recording 和
Accessibility 权限。

远程桌面通过现有 Spool HTTPS/Tailscale 入口访问，voxl helper 只监听本机回环地址。
文件面板使用 `<data-dir>/voxl/shared/`，上传和下载仍受 Spool 登录、远程桌面授权、
Origin 和 CSRF 检查保护。更多平台依赖可参考 [voxl upstream instructions](https://github.com/tembo/voxl#usage)。

## voxl 版权与许可证

Spool 集成的 [voxl](https://github.com/tembo/voxl) 源代码采用
[MIT License](https://github.com/tembo/voxl/blob/main/LICENSE)。voxl 构建中使用的
[x264](https://www.videolan.org/developers/x264.html) 采用
[GPLv2 或更高版本](https://www.gnu.org/licenses/old-licenses/gpl-2.0.html)；
链接了 x264 的构建需要遵守适用的 GPL 条款。其他依赖保留各自的许可证。

本地安装或编译 voxl 不会自动移除这些义务。如果重新分发包含 voxl 或 x264 的
二进制文件，请一并评估并提供适用的许可证声明、版权信息和对应源代码。
