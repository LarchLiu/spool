<p align="center">
  <img src="./assets/logo.png" alt="Logo" width="60">
</p>
<h1 align="center">Spool</h1>

[English](README.md)

Android 现可通过独立部署在 VPS 上的 Go 中继连接家里的 Spool，自动优先使用
经过身份验证的局域网连接。Spool Desktop 提供配对二维码，手机可直接扫码确认。
部署与配对步骤见 [中继说明](relay/README.md)。
Web/PWA 也可通过中继现有 HTTPS 地址的 `/web/<服务器 ID>/` 访问，无需为每台
服务器配置域名，也无需在浏览器设备上安装 Tailscale。在 Spool 开启后，可信中继
会在连接时自动添加路由，无需配置服务器 ID 列表或重启中继。中继会解密 Web 流量，
Android 仍使用端到端加密。配置步骤见
[公网 Web 访问](relay/README.md#public-web--pwa-access)。

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
- 文件边栏默认跟随会话最后一轮的工作目录；有多个工作区时可通过目录名后的下拉框手动切换。
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

### Linux 发布版安装

从发布附件下载 `install.sh`，使用普通用户运行：

```sh
sh install.sh
```

脚本验证发布包后，会准备 WebKitGTK、桌面图形和托盘运行库，以及 voxl
构建依赖（Clang/libclang、esbuild、pkg-config、x264、libyuv、Opus、
PulseAudio 和 XCB）。支持 apt-get、dnf、pacman 和 zypper；缺失的系统包通过 sudo
或 polkit 请求管理员授权。脚本先读取 `/etc/os-release`，刷新软件包元数据并
检查所有候选包；缺包时统一列出缺项和软件源提示，不会先安装部分依赖。
脚本不会自动添加编解码软件源或升级整个系统。Arch 用户应先自行执行
`pacman -Syu`。依赖重新检查通过后才会替换已有 Spool 应用。
已有 WebKitGTK 4.1 或 4.0 均可，托盘兼容 Ayatana AppIndicator 和旧版
AppIndicator；新安装优先选择 4.1/Ayatana。

自动依赖安装面向 Ubuntu 22.04+、Debian 12+、Fedora、Arch 系和 openSUSE。
Debian/Ubuntu 衍生发行版通过 `ID_LIKE` 识别，不会把 Mint 等系统自己的版本号
当成 Ubuntu 版本。RHEL 系 9+ 使用其已配置的软件源，可能需要额外的开发库和
编解码软件源。Fedora、Arch、openSUSE、RHEL 及衍生系统在具体版本完成验证前
均视为实验支持。Alpine、NixOS、未知发行版和不可变/OSTree 系统需要自行准备
依赖；旧版 Ubuntu/Debian/RHEL 不做自动安装。自管环境可用
`--skip-dependencies`，但跳过检查不代表二进制兼容。发布包仅提供 x86_64 和
arm64，验证发布包需要 Python 3 和 OpenSSL 3+。

如果 Rust 或 Cargo 缺失或版本低于 1.85，脚本会在必要时为当前用户安装 rustup，
再执行：

```sh
rustup toolchain install stable --profile minimal
rustup default stable
```

已有可用且兼容的工具链会保留，不修改 shell 配置文件。
此步骤需要访问 `sh.rustup.rs` 和 Rust 工具链下载服务器。

Debian/Ubuntu 也可以手动安装系统依赖：

```sh
sudo apt-get update
sudo apt-get install --no-install-recommends \
  libwebkit2gtk-4.1-0 libgl1 libegl1 libgl1-mesa-dri libxkbcommon0 \
  libayatana-appindicator3-1 build-essential clang libclang-dev pkg-config \
  esbuild libx264-dev libyuv-dev libopus-dev libpulse-dev libxcb1-dev
```

不需要 voxl 时使用 `sh install.sh --skip-voxl-dependencies`，仅安装桌面运行依赖；
自行管理依赖时可用 `sh install.sh --skip-dependencies`。这些选项之后可以传入
离线 Spool 压缩包；缺失依赖仍需要联网获取。voxl 本体稍后通过
**设置 → 服务 → 远程桌面 → 安装 voxl** 编译安装。补齐系统库后需要完全退出
并重新打开 Spool；Xfce 的菜单入口是 **Applications → Development → Spool**。

安装脚本还会自动选择应用菜单启动时的渲染方式。X11 会话已有 `glxinfo` 时，
通过 `glxinfo -B` 检查渲染器（最多等待 5 秒）；原生 Wayland 或没有检测工具时，
检查当前用户能否访问 DRM render 或 NVIDIA 设备，避免用 XWayland 的 GLX
结果判断原生 Wayland 的渲染器。没有可用加速时，会在桌面启动项中加入以下变量，
避免无 GPU 的云桌面出现 WebKit 崩溃：

```sh
LIBGL_ALWAYS_SOFTWARE=1
WEBKIT_DISABLE_COMPOSITING_MODE=1
WEBKIT_SKIA_ENABLE_CPU_RENDERING=1
```

有 GPU 但驱动不稳定时，可用 `sh install.sh --software-rendering` 强制启用；
使用 `sh install.sh --hardware-rendering` 则保留 WebKit 的默认渲染方式。
这些选项也可以搭配 `--skip-dependencies`。完全退出 Spool 后，从应用菜单重新
打开即可生效。终端直接执行 Spool 二进制时，使用的是终端自身的环境变量。
设备可访问不代表驱动一定稳定，各 WebKit 版本对这些变量的处理也可能不同。
voxl 屏幕采集仍然需要 X11/Xvfb，渲染参数不会增加原生 Wayland 采集支持。

Docker 验证**仅供手动运行**，不会加入普通测试或发布自动化。在仓库根目录可选执行：

```sh
sh tools/check-linux-container.sh ubuntu:24.04 native
```

`native` 会安装并加载桌面/voxl 系统库，检查 libyuv 链接；`desktop` 仅检查桌面
运行库；`voxl` 还会下载 Rust 并编译固定版本的 voxl。可复用缓存放在
`build/cache/`，临时数据放在 `build/tmp/`，每种镜像/模式的最新日志保存在
`build/artifacts/linux-compatibility/`。容器验证覆盖运行库和构建依赖，实际
X11/Wayland 桌面启动和硬件 GPU 仍需实机检查。

本次安装脚本改动的手动验证记录：

| 系统 | 容器架构 | 已验证范围 |
| --- | --- | --- |
| Ubuntu 22.04 / 24.04 | arm64 | 桌面与 voxl 原生系统库安装、加载和链接检查通过 |
| Debian 12 | arm64 | 桌面与 voxl 原生系统库安装、加载和链接检查通过 |
| Fedora 43 | arm64 | 桌面库安装和加载检查通过；未验证 voxl 编解码软件源 |
| openSUSE Tumbleweed | arm64 | 桌面库安装和加载检查通过；未验证 voxl 编解码软件源 |
| Arch Linux | arm64 上仿真 x86_64 | 安装检查受仿真环境的 pacman 沙箱支持限制，尚未验证完成 |

上述检查未编译 voxl，也未运行图形桌面/GPU 测试。Debian 13、RHEL 系、
openSUSE Leap 和其他衍生发行版本次尚未做真实容器验证。

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

Android 原生 APK 也可以使用同一个公网地址：点击侧栏服务器 tabs 末尾的 **＋**
添加服务器并命名，通过该 tab 的 footer **设置** 入口填写 **公网 HTTPS** 地址，
点击该网络内的 **连接**。设置只编辑当前服务器，也可以重命名、启用或停用它，底部可删除服务器。
每台服务器还可以填写 Tailscale 地址，点击哪个连接方式就使用哪个，按服务器保存选择，
重启后继续使用。允许自动切换时，仅在当前连接不可用后尝试另一条。同一服务器
同时只连接一路网络，不同服务器可以各自保持连接，切换服务器必须手动点击 tab。
Tab 名称左侧的圆点显示连接状态；说明和 Tailscale 连接详情收在标题旁的信息图标中。
个人信息、登录/退出、聊天、草稿和远程桌面均跟随当前服务器。同一服务器的两路网络
共用登录态，切换网络无需重复登录；不同服务器的登录独立保存。升级时优先沿用当前
连接的已有登录，退出会同时清除该服务器两路网络的登录态。
设置中的服务器连接和自动切换选项使用右侧开关。
断开、连接中和重试时，footer 保留当前服务器最近登录的姓名与头像，显示连接或重试入口。
这份显示缓存不包含令牌、CSRF 或权限；网络错误不会清除它，明确退出或登录被拒绝后才会清除。
tsnet 的 HTTPS 恢复正常后会继续验证 Spool 会话、刷新工作区资料。连接圆点仅表示网络连通状态；
tsnet 详情只显示节点、链路和 HTTPS 信息，Spool 登录由 footer 独立处理。
主题和语言通过侧栏顶栏关闭按钮左侧的调色板图标打开。
应用启动或切换服务器时默认显示边栏，点击会话后再进入聊天。两路网络放在同一个卡片中，
各自提供连接或断开按钮；Tailscale 登录图标位于该网络标题右侧。
每个 tab 在内存中保留各自的项目、最近会话、选择、展开、滚动和草稿状态，切换时立即显示。
已连接且已登录的服务器各自订阅工作区 WebSocket，即使没有显示该 tab，也会更新它的项目和会话列表。
切换 tab 会复用窗口、边栏和原有 WebSocket，不重连服务器，也不提前构建隐藏的聊天记录。
重连和切换网络期间保留已有列表，获取新数据后再刷新；工作区缓存不写入持久化存储。
远程桌面可用时，在 footer 设置左侧显示显示器图标，点击即可连接当前服务器。
可用状态通过工作区 WebSocket 推送，并随权限、服务状态和服务器切换更新。

### Tailscale 与 Android

Desktop 可以在 **Service > Tailscale (tsnet)** 中启用内置 Tailscale；headless
server 可以使用：

```sh
./build/spool-server -tsnet -tsnet-hostname spool-server
```

首次使用需要在 Tailscale 管理控制台启用 MagicDNS 和 HTTPS certificates，并
完成节点登录或批准。服务启动后会打印实际的 HTTPS 地址，把这个完整地址填入
Android 客户端对应服务器的 **Tailscale (tsnet)** 地址框即可。
Tailscale 入口仍然要求登录 Spool，且不会绕过项目权限或
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
Opus、PulseAudio 和 XCB 开发库（Debian/Ubuntu 对应 `libxcb1-dev`）；
Windows 需要 Rust MSVC、Visual Studio C++ Build Tools、
LLVM 和 vcpkg。

macOS 安装时还会用 Xcode 命令行工具在本机编译一个小型原生启动器，并生成
`<data-dir>/voxl/bin/Voxl Helper.app`。应用包包含原生监督程序和本机编译的 voxl，
在本机进行 ad-hoc 签名，不需要 Apple Developer ID 证书。请在系统设置 > 隐私与
安全性中，为 **Voxl Helper** 授予屏幕录制和辅助功能权限，授权后重新连接。
使用 CLI 时也需要在已登录的 macOS 桌面会话中运行 Spool。
只有已获授权的远程桌面连接到来时，Spool 才通过 LaunchServices 启动 helper。
带认证的本机回环控制连接负责监督 voxl；Spool 停止或寿命管道关闭时停止采集。
最后一个查看者和文件传输结束后，helper 和 voxl 都会退出。安装或启用远程桌面
不会启动采集，也不会注册登录启动项。

旧版 macOS 安装如果只有 `bin/voxl`，请在本机点击一次 **安装 voxl** 或执行
`-install-voxl`，即可将已有二进制包装成应用包，无需下载或重新编译 Rust。
原二进制会保留，共享文件继续放在应用包外的 `voxl/shared/`。普通 Spool 升级
会原样复用完整的 helper 应用包及其签名；替换或重新编译 helper 本身仍可能需要
重新授权。第一版 helper（安装格式 1）需要在本机点击一次 **升级 voxl**，
以应用启动失败时的进程清理修复。升级会复用应用包内的引擎，验证完成后原子
替换应用包；升级失败会保留原应用。如 macOS 提示，请给新版 helper 重新授权。
Windows 和 Linux 继续直接运行 voxl，并使用相同的按需启停规则。

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
