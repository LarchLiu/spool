# 部署与远程访问

[English](deployment.md)

本文介绍安装、服务配置、Web/PWA、Tailscale 和 voxl 远程桌面。使用 `./build/`
路径的命令在仓库根目录运行；源码构建见 [README](../README.zh-CN.md#开发)。

## Linux 发布版安装

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
arm64，验证发布包需要 Bun 和 OpenSSL 3+。

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

应用菜单启动项直接运行 Spool，使用 WebKit 的默认渲染配置。
voxl 屏幕采集需要 X11/Xvfb。

Docker 验证**仅供手动运行**，不会加入普通测试或发布自动化。在仓库根目录可选执行：

```sh
sh tools/check-linux-container.sh ubuntu:24.04 native
```

`native` 会安装并加载桌面/voxl 系统库，检查 libyuv 链接；`desktop` 仅检查桌面
运行库；`voxl` 还会下载 Rust 并编译固定版本的 voxl。可复用缓存放在
`build/cache/`，每轮临时数据放在系统临时目录的 `spool/` 下并在退出时清理，每种镜像/模式的最新日志保存在
`build/artifacts/linux-compatibility/`。容器验证覆盖运行库和构建依赖，实际
X11/Wayland 桌面启动和硬件 GPU 仍需实机检查。

## 运行服务


开发运行默认使用 `~/.spool-dev`，生产构建默认使用 `~/.spool`。数据库位于
对应数据目录的 `db/spool.sqlite`。可以用 `-data-dir` 或 `SPOOL_HOME` 选择其他
数据目录；同一个数据目录同时只能运行一个 Desktop 或 server 实例。

Release 还会单独提供 `spool-server-VERSION-OS-ARCH.tar.gz`，覆盖 Linux、macOS、
Windows 的 amd64/arm64。包内已有 Web/PWA 资源，不需要 GUI、GTK/WebKit 或 voxl，
Linux 包附带 systemd 用户服务示例。安装与全部命令分组见
[Server CLI 使用说明](server-cli.md)。

使用 `spool-server --help`、`spool-server help project` 或
`spool-server project member set --help` 查看逐级帮助。无参数时显示帮助，启动服务
使用 `serve`；原来的扁平参数继续兼容。管理命令包括用户修改与启停、项目归档与成员
权限、Agent 路径与启停、服务和网络配置、Issue 自动化、GitHub CLI、Relay 和 Tailscale。
管理操作需要先停止使用同一数据目录的服务，完成后再启动，不需要 Web 管理后台。

启动 headless server 前，可以在服务停止时创建初始用户和项目：

```sh
SPOOL_NEW_PASSWORD='replace-with-a-long-unique-password' \
  ./build/spool-server user add --username alex --nickname Alex

./build/spool-server project add --name Example \
  --directory /absolute/project/path --members alex

./build/spool-server serve --host 127.0.0.1 --port 8787
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

Desktop 用户可在 **设置 → 服务 → Web** 填写隧道或反向代理提供的公网 HTTPS 地址，
完成编辑（移开焦点或按 Enter）后自动检测；**检测**按钮用于手动重试。隧道应已启动并
指向当前 Spool 的 Web 端口。检测成功后，公网地址和本次连接的代理地址会自动保存并
立即生效，无需填写 IP/CIDR 或再次应用。失败时不会新增信任或应用新的公网地址，已
保存的输入仍保留。Spool 在每次启动或重新启用普通 Web 入口时也会检测一次，运行中
不定时轮询；检测不会阻塞本机服务启动。隧道启动较晚或运行中更换代理地址后可手动重试。
成功复检会替换上次自动检测的地址；手动 **Web 可信代理**位于 **高级设置**，用于多级
代理或多个副本的补充配置。该逻辑由底层共享，Headless server 同样适用：
`serve --public-url https://chat.example.com` 启动后自动检测，停服时的 `config set`
只保存地址，下一次 `serve` 启动后再检测。CLI 日志记录检测成功或失败。

普通 HTTP 监听器仅向直接回环连接或可信 HTTPS 反向代理提供应用。远程明文请求在
展示登录页、读取密码或验证会话之前返回 403；`/healthz` 继续公开可用。配置 Public URL
不会为后端监听器启用 TLS。

反向代理访问要求 Public URL 与外部 HTTPS 源完全一致。代理必须覆盖
`X-Forwarded-Proto` 为单个 `https` 值，并保留外部 Host；如果改写 Host，则设置单个
匹配的 `X-Forwarded-Host`。上述 Caddy 示例会提供这些头。Spool 不采信不可信连接来源
声明的转发信息，并拒绝有歧义的多值。外部 HTTP 请求应由代理在携带登录数据前重定向
或拒绝。

手动 **Web 可信代理**默认为 `127.0.0.0/8,::1/128`。自动检测只管理实际连接的单 IP，
并绑定已验证的公网地址；多级代理等特殊部署可在 Desktop 高级设置中补充实际 IP CIDR，
或使用 `spool-server config set --web-trusted-proxies '192.0.2.10/32'`。留空只清除手动
条目，自动验证的地址单独管理。后端应限制在回环或受保护网络内，并只允许可信代理访问；该选项不会加密代理
到后端的连接，与出站代理设置相互独立。

普通 Web 后端还使用这份信任列表解析登录限流的客户端 IP。代理必须将
`X-Forwarded-For` 覆盖为实际客户端 IP，或在其末尾追加实际连接来源。Spool 从右向左
解析配置的可信代理，遇到第一个不可信 IP 就停止；客户端伪造的前缀不能选择其他登录桶。
多级代理需配置每一级可信代理的 CIDR，不应把所有客户端网段列为可信代理。转发信息
缺失或可信链节点格式错误时，保守地使用实际连接 peer 的额度。限额仍为每个 IP 每
10 分钟 20 次登录尝试，包含格式错误的请求；经 NAT 共用公网 IP 的用户仍共用该 IP
的额度。Relay 和 Tailscale 保留各自传输层提供的客户端身份。

Desktop 设置中的 **允许局域网访问** 会绑定所有 IPv4 接口，供已配置的反向代理连接
后端，不允许其他设备直接通过 HTTP 登录。远程访问请使用 HTTPS、Tailscale 或配对的
Relay/安全 LAN。关闭 **启用 Web 访问** 会同时停止该监听器及指向它的反向代理访问；
Desktop 聊天、Tailscale 和 Relay 仍使用各自独立的连接。

Android 原生 APK 也可以使用同一个公网地址：点击侧栏服务器 tabs 末尾的 **＋**
添加服务器并命名，通过该 tab 的 footer **设置** 入口填写 **公网 HTTPS** 地址，
点击该网络内的 **连接**。设置只编辑当前服务器，也可以重命名、启用或停用它，底部可删除服务器。
每台服务器还可以填写 Tailscale 地址，点击哪个连接方式就使用哪个，按服务器保存选择，
重启后继续使用。允许自动切换时，仅在当前连接不可用后尝试另一条。同一服务器
同时只连接一路网络，不同服务器可以各自保持连接，切换服务器必须手动点击 tab。
Tab 名称左侧的圆点显示连接状态；说明和 Tailscale 连接详情收在标题旁的信息图标中。
个人信息、登录/退出、聊天、草稿和远程桌面均跟随当前服务器。公网 HTTPS、Tailscale
和 Relay 分别保存登录态，并绑定连接方式与完整 HTTPS 源。先在各入口分别登录，
自动切换才可使用该入口；更换地址、端口或配对服务器身份后，该入口需要重新登录。
Relay 的局域网优选与中继路径验证同一个已配对身份，可以共用登录态。旧版共享会话
格式升级后需要重新登录。退出会撤销当前入口的服务端会话，并清除该服务器各入口
在本机保存的登录态；其他入口的服务端会话按正常策略到期。不同服务器档案的登录独立保存。
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
