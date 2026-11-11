# 三端通信安全审查

审查日期：2026-11-10；修复复核：2026-11-11（Asia/Shanghai）

审查基线：`4adc21681ea42f543aa294cddb99ae19cbbb1209`

CS-03 修复提交：`8118d95`；后续修复包括普通 Web 入口保护、可信代理与客户端登录限额，以及当前工作区中的 Relay Web 独立子域名。以下修复状态不表示已发布到发行版本。

## 结论

已完成 Web/PWA、Desktop、Android 各连接模式的源码审查，并结合本地隔离测试验证关键边界。原始审查确认三项需要修正的安全行为，以及一项文档已披露的高影响架构限制。四项均已修复，其中 CS-01 在当前工作区改为每台服务器独立 HTTPS 子域名，并退役共享源下的公网挂载：

| 编号 | 优先级 | 类型 | 结论 |
| --- | --- | --- | --- |
| CS-01 | P1，高影响，取决于部署 | 浏览器源隔离缺口，当前工作区已修复 | 每台服务器分配独立 HTTPS 子域名；精确 Host/Origin、`__Host-` 会话 Cookie 和独立 PWA 隔离服务器运营者；旧共享路径返回 410。 |
| CS-02 | P1，取决于非回环 HTTP 暴露 | 凭据与传输保护缺口，当前工作区已修复 | 普通入口拦截直接远程 HTTP 的页面、密码和会话请求；HTTP 后端仅允许本机或配置的 HTTPS 可信代理。 |
| CS-03 | P2，取决于地址编辑 | 会话信任边界缺口，当前工作区已修复 | Android 会话按连接方式和精确 HTTPS 源独立保存；地址或配对身份变更不继承旧会话；健康检查不收发会话 Cookie。 |
| CS-04 | P2，影响可用性 | 反向代理适配缺口，当前工作区已修复 | 普通 Public HTTPS 入口按可信代理链解析后的客户端 IP 分桶；伪造转发前缀和更换端口不改变同一客户端的限额。 |

P1 表示应在相关部署暴露前解决；P2 表示应列入近期修复。这里没有对上述行为统一套用 CVSS：地址编辑、LAN 开关、是否允许不受信任的运营者注册等前提会显著改变实际风险。原始复现测试通过只表示验证了所述风险；各项另有修复后的回归验证，见下文。

原始审查只增加报告和可重复执行的测试。后续按用户要求修复 Android 地址变更会话泄漏、普通 Web 入口接受远程明文凭据、反向代理后的共享登录限额和 Relay Web 的跨服务器同源访问；改动包括会话及传输边界、可信代理、独立子域名及代理集成、测试与说明。

## 连接方式与信任边界

| 客户端或功能 | 通信路径 | 保护与信任要求 | 审查结论 |
| --- | --- | --- | --- |
| Desktop 内嵌聊天 | 系统 WebView → 独立的 `127.0.0.1` 随机端口 HTTP | 本机边界；仍要求 Spool 登录、CSRF、项目权限；导航限制到精确源 | 未发现远程管理接口暴露；本机恶意进程不在网络隔离保证内。 |
| 本机 Web/PWA | 浏览器 → 回环 HTTP | 浏览器同源策略、登录、CSRF | 默认绑定回环，不是远程明文入口；与开放 LAN 后的风险应分开评估。 |
| Web 普通 LAN 访问 | 浏览器 → 非回环普通 Web HTTP 监听器 | 直接 HTTP 无链路加密 | CS-02 修复后拒绝远程直接 HTTP 应用访问；该端口仍与 Relay 的安全 LAN TLS 端口不同。 |
| Web/PWA、Android Public HTTPS | 客户端 → HTTPS 反向代理 → Spool HTTP 后端 | 外部 HTTPS 及精确 Public URL；HTTP 后端校验代理 peer CIDR 和 HTTPS 声明，后端应只向代理开放 | CS-02、CS-04 已修复；按可信代理链解析客户端登录限额，Origin/CSRF 检查保留。 |
| Web、Android Tailscale | HTTPS → tailnet → Spool tsnet TLS 监听器 | tailnet 网络控制及 WireGuard 传输；另有 HTTPS 主机名校验和 Spool 认证 | 未发现降级到明文或跳过 Spool 登录。服务端最低 TLS 1.2。Android 精确选择 peer，不以系统 DNS 回退。 |
| Android Relay | Android → 外层 WSS → Relay → 外层 WSS → 家庭服务 | 内层 TLS 1.3 固定服务器 Ed25519 公钥；手机客户端证书需本地批准；Relay 只转发内层密文 | 未发现仅凭 Relay 路由标识或 admission token 冒充已批准手机的路径。Relay 仍能观察连接元数据并影响可用性。 |
| Android Relay 模式下的 LAN 优选 | DNS-SD/私网地址提示 → 家庭服务安全 LAN 端口 | 与 Relay 路径固定同一服务器密钥，且要求同一手机客户端证书 | 未发现以伪造 mDNS/私网地址绕过身份校验。TLS 握手成功前不发送业务 HTTP。 |
| Android Public/Tailscale/Relay 路由切换 | 同一服务器档案按连接方式和精确 HTTPS 源保留独立会话 | 新地址需重新登录；Relay 的 LAN 与中继路径验证同一固定密钥身份 | CS-03 已在当前工作区修复；旧共享格式升级需重新登录，各入口首次使用需分别登录。 |
| Relay Web/PWA | 独立子域名 HTTPS → 前置代理/Relay Web 网关 → 固定服务器公钥的 TLS 隧道 | 需信任 Relay 与 TLS 代理；不同服务器运营者由浏览器源和会话 Cookie 隔离 | CS-01 已修复。Web 默认关闭，不要求手机设备证书；Relay 与前置代理仍可读取业务明文，不能套用 Android 原生端到端加密的信任结论。 |
| Desktop 原生设置 | 独立的内嵌设置文档 → MyGo 原生绑定 | 校验调用页面对象及固定文档地址；设置页 CSP 禁止网络连接 | 未发现 Web、Android 或普通聊天页调用本地管理绑定的 HTTP 旁路。 |
| 三端远程桌面 | 既有 Spool 路径 → 授权代理 → 本机 voxl | 独立用户授权、当前登录会话、视图 token、Origin；voxl 仅回环监听，代理注入内部 Bearer | 未发现匿名或仅持有视图 token 的访问路径。外层入口仍须满足传输策略；Relay Web 使用当前独立子域名。 |
| Web Push | Spool → HTTPS 推送服务 → 浏览器 | 标准 Web Push 载荷加密；订阅按登录会话绑定；出站拒绝私网 DNS 解析和重定向 | 已有 SSRF 及跨账号订阅边界；推送服务仍可观察投递元数据。 |
| Desktop 更新 | 系统 HTTPS/配置代理 → GitHub 发布资产 | 更新清单 Ed25519 签名、仓库/平台/URL 绑定；应用更新器校验归档签名 | 未发现仅篡改网络清单即可安装无签名更新的路径。未在本次审查中安装实际更新。 |
| Android APK 更新 | 独立系统 HTTPS → GitHub 发布资产 → 系统安装器 | HTTPS 主机白名单、重定向限制、大小/哈希、包名/版本及现有 APK 签名证书一致性 | 未发现复用 Spool Cookie 或放宽 TLS 校验的路径。未执行真机安装。 |
| 出站 HTTP(S)/SOCKS 代理 | Desktop/服务端 HTTP 客户端、tsnet 的配置代理 | 普通 HTTP/SOCKS 代理自身不提供外层机密性；目标 HTTPS 仍须校验证书；代理必须可信 | 未发现因启用代理而关闭目标 TLS 校验。回环及原生 Android 业务传输使用独立策略。 |

## CS-01：Relay Web 不能隔离互不信任的服务器运营者

**状态：当前工作区已修复。** 本节先保留原始风险和本地验证，再记录独立子域名实现与回归结果。当前部署说明见 [Relay Web 子域名、代理与证书](../relay/relay-web-subdomains.zh-CN.md)。

**原始性质：** 已知架构限制；对不受信任的多租户部署属于高风险。原文档说明所有 Web 挂载共享源，路径不是安全边界，并要求各运营者相互信任。

**原始代码位置：** `relay/web.go:20`、`:45`、`:98`、`:110`，`internal/workspace/relay_web.go:24`，`internal/workspace/auth.go:137`，`relay/server.go:139`。行号对应原始审查基线。

**触发前提：** 同一 Relay 同时托管受害者服务器与攻击者控制的 Web 服务器；受害者在该 Relay 源下保留目标服务器的有效浏览器登录，并访问另一服务器提供的页面；攻击者知道目标挂载地址。Server ID 是公开路由标识，不能当作访问凭据。

所有挂载形如 `https://relay.example/web/<server-id>/`。Cookie 的 Path、Service Worker 的 scope 和存储命名空间能减少普通功能串用，却不能建立浏览器跨源隔离。攻击者作为其服务器的运营者，可以返回自己控制的 HTML/JS；该脚本向目标服务器的路径发起请求时，浏览器视为同源请求，会附带目标路径的 Cookie，并允许读取响应。取得 `/api/me` 返回的 CSRF 后，也可以执行该登录用户有权限的写操作。`HttpOnly` 无法阻止浏览器代带 Cookie；`SameSite` 和当前 Origin 校验同样无法区分一个源下的不同路径。

**本地验证：** 两个隔离服务器经本地 Relay 挂载，使用独立浏览器配置及合成账号。在目标服务器登录后，打开另一服务器提供的脚本页面。实际观察到目标 `/api/me` 为 200，读取到预期用户；使用读到的 CSRF 调用目标登出为 200，随后 `/api/me` 为 401；脚本始终无法通过 `document.cookie` 读取 HttpOnly 会话。验证使用回环 HTTP 测试夹具；生产 HTTPS 不改变同源策略按 scheme/host/port 而非路径划分的事实。

该验证只执行合成账号的读取和登出。读取项目、代发指令或连接远程桌面的影响取决于被利用账号的现有权限；本次没有执行真实主机控制或远程代码执行验证。

**原始建议：** 继续使用共享源时，必须只接纳相互信任的 Web 运营者；原生 Relay 可继续保持端到端通信。若要支持不受信任的多租户 Web，应给每个服务器分配独立浏览器源，并同时处理 host-only Cookie、父域 Cookie 干扰、Service Worker、PWA、CSP 及 WebSocket Origin。至少将 Web 发布资格与原生自动注册资格分开控制，避免允许原生自助注册时无意允许不受信任的同源 Web 内容。仅修改 Cookie Path、强化现有 CSRF 或增加通用 CSP 不能解决该边界。

**验收标准：** 两个不受信任的 Web 服务器位于不同源；其中一个的脚本无法读取另一个的认证 API、获取其 CSRF、发送被接受的认证写请求，或影响其 Service Worker/认证 Cookie。

**已实现的修复：**

- 使用完整服务器公钥标识分配 `https://<server-id>.<web-domain>/`，域名后缀由部署环境变量设置，管理页只读展示。Web v2 发布意愿纳入注册签名；Host 验证返回地址绑定自身身份。旧客户端仍可连接原生通道，但不能恢复共享源 Web。
- Relay 按精确 Host 路由到对应身份，检查精确 Origin，拒绝兄弟子域、重复 Origin、嵌套旧路径及路径规范化逃逸。租户子域无法访问 Relay 的管理接口或另一服务器的内部挂载。原来的 `/web/<id>/` 公网入口返回 410 和清理站点数据响应头。
- Relay Web 使用独立的 `__Host-spool_session`，设置 Secure、HttpOnly、SameSite=Strict、Path=/ 且无 Domain。普通入口的 `spool_session` 不被此入口接受；父域无法注入合法的 `__Host-` 会话 Cookie。每个源独立管理 PWA、Service Worker、缓存和存储，并禁止 `document.domain` 放宽隔离。
- 持久化服务器的签名发布意愿及管理员的 Web 停用状态；断线、Relay 重启保留域名授权，撤销或停用取消路由资格。通过私有 28082 接口分别提供 Caddy 签发许可和带独立只读 token 的 Traefik HTTP Provider。DNS API 密钥仅由 TLS 代理持有。
- Docker Compose 通过 `COMPOSE_PROFILES=caddy` 选择启动配套 Caddy；逐域名使用内部授权查询，通配证书使用 DNS-01。也支持外部 Caddy、Traefik/Coolify 和手工配置的 Nginx。

**修复验证：** 两个真实 Spool 应用运行于不同 HTTPS 兄弟子域，无头 Chrome 先登录受害者，再加载另一运营者控制且移除了正常 Spool CSP 的页面。跨源认证读取被阻止；即使提供已知受害者 CSRF，写请求仍被拒绝；无 CORS 的表单式请求没有注销受害者；跨源 WebSocket、Service Worker 注册被阻止；localStorage 隔离；父域 `__Host-` Cookie 注入失败，普通父域 Cookie 不影响受害者登录。两边各自的登录、WebSocket、SSE、PWA、重载和离线回退继续通过。旧挂载迁移仅清理旧源缓存和存储，浏览器验证不会连带注销新子域；旧 Cookie 不被新入口接受。隔离检查结果保存在 `build/artifacts/relay-web/isolation.json`。

真实 Caddy 和 Traefik 容器验证了授权查询、HTTP Provider 动态加载、TLS 终止、Host 路由与撤销。Traefik 的运行时路由检查额外确认逐域名模式在 Relay 路由上显式选择证书解析器，通配模式使用已加载证书；无须共享入口默认值，其他项目的路由保留各自的 TLS 策略。Caddy 使用临时本地签发机构，Traefik 使用临时测试证书；未向公共 CA 申请真实证书，也未使用真实 DNS API 密钥。部署者仍须准备 DNS、代理可达性及生产证书配置。子域名隔离不改变浏览器业务需要信任 Relay 和前置 TLS 代理的事实。

| CS-01 修复检查 | 结果 |
| --- | --- |
| workspace、Server CLI、Desktop 受影响包普通测试及 vet | 通过。 |
| Relay 模块完整普通测试、vet、race；workspace Relay 生命周期定向 race | 通过。 |
| `sh tools/security-audit/run.sh repro`，设置独立 `CHROME_BIN` | 四项原始问题均断言修复后保护并通过。 |
| 管理页真实 Chrome 检查 | 通过，含部署域名只读、禁止 API 修改、启动检测、动态可信代理、凭据操作及手机布局。 |
| 实际 Caddy、Traefik 逐域名与通配模式容器 | 三个场景通过；Traefik 显式路由证书策略与其他路由隔离符合预期。 |
| Compose profiles、环境变量与镜像 | profile 开关和 DNS 密钥隔离检查通过，Relay 与配套 Caddy 镜像构建通过。 |

## CS-02：非回环普通 Web 入口接受明文凭据

**状态：当前工作区已修复。** 本节保留原始风险记录，修复策略见后文。

**原始代码位置：** `internal/workspace/ingress.go:34`，`internal/workspace/management.go:82`、`:112`，`internal/workspace/server.go:185`，`internal/workspace/auth.go:97`、`:104`。行号对应原始审查基线。

**触发前提：** 启用普通 Web 的 LAN 访问、指定非回环 `-host`，或把 HTTP 后端暴露到外部；用户在该 HTTP 地址登录，或者持有会话的客户端向它发送请求。默认 `127.0.0.1` 绑定不构成此项远程暴露。

普通监听器通过 `net.Listen` 提供 HTTP，登录入口没有检查远端明文传输，鉴权也不会拒绝明文携带的会话。`PublicURL=https://...` 只影响部分来源与 Cookie 策略，不会替监听器启用 TLS，也不会关闭原有的 HTTP 登录入口。

**本地验证：** 使用合成的非回环 `RemoteAddr`、Host 和同源 Origin，经实际 `App.ServeHTTP` 登录及读取 `/api/me`。未设置 Public URL 时，登录 200、Cookie `Secure=false`、携带该会话的明文 API 请求 200。Public URL 改为 HTTPS 后，明文登录仍为 200、Cookie 改为 `Secure=true`，显式加入该 Cookie 的明文 API 请求仍被接受。

后一项是对服务端是否接受明文 bearer 的验证，并不声称浏览器会在 HTTP 上发送带 Secure 的 Cookie；但初次登录的密码请求本身已经没有链路机密性。在未配置 HTTPS Public URL 的 LAN 场景中，浏览器会话同样缺少 Secure 保护。

**影响：** 能观察或篡改该网络链路的一方可能获取登录密码、未受保护的会话或业务数据。它不能由 CSRF、HttpOnly 或项目权限修复。与此相对，Relay 的独立安全 LAN TLS 监听器不受这项明文入口缺口影响。

**建议：** 将本机 HTTP、受信任反向代理后端、直接 LAN 访问划为明确入口策略。本机及仅向受信任代理开放的后端可以保留 HTTP；直接 LAN 业务应使用经过验证的 TLS，或引导使用已有安全配对 LAN/tsnet。配置 Public URL 不应被 UI 表述为已经加密 LAN。若采用代理头识别 HTTPS，必须限定可信代理来源，不能直接信任任意 `X-Forwarded-Proto`。

**验收标准：** 非回环、非受信任代理的明文请求无法提交密码或使用认证会话；本机 Desktop、正确配置的 HTTPS 代理以及独立安全 LAN 仍能正常使用。

**已实现的修复：**

- `ServeHTTP` 在展示应用页面、读取登录 body、消耗登录额度及验证 Cookie 前执行传输策略；不符合要求时返回 403 和 no-store。覆盖 API、文件、SSE、WebSocket 及远程桌面入口。不含认证的 `/healthz` 保留普通 HTTP 可用性检查；本机主动发起的检测另有短时、随机、一次性回调，不能访问认证接口。
- 直接 HTTP 仅允许实际 TCP peer 为回环、Host 为回环地址或 localhost、Origin 为空或匹配本机 HTTP 源且没有转发标记的请求。伪造 Host、Origin、`Forwarded` 或 `X-Forwarded-*` 无法让远程 peer 成为本机。
- TLS 入口继续可用。HTTPS 代理的 HTTP 后端请求要求：实际 peer 位于 `web_trusted_proxies` CIDR 列表、Public URL 为 HTTPS、单值 `X-Forwarded-Proto=https`，并且 Host 或单值 `X-Forwarded-Host` 匹配 Public URL。重复或逗号拼接的协议/Host 值被拒绝。可信代理必须覆盖外部传入头，并在提交密码之前拒绝或重定向 HTTP 前端请求。
- 新增 **Web 可信代理**设置和 `--web-trusted-proxies` 参数，默认只信任回环 CIDR，允许显式清空。配置保存、应用、取消与 Web 入口一同管理。代理到后端的 HTTP 仍需部署侧通过回环、受保护网络和访问控制保护；CIDR 信任不提供链路加密。
- 远程入口只签发 Secure Cookie；经代理的写请求不接受后端 HTTP 源作为浏览器 Origin。本机 Desktop、tsnet TLS 和原生 Relay/LAN 继续采用既有认证及网络身份校验。

**修复验证：** `ingress_security_test.go` 包含原漏洞失败后通过的回归，覆盖密码 body 未被读取、已有会话明文重放被拒绝、代理信任及重复头边界，以及真实本机 TLS 反向代理：HTTPS 前端可登录及使用会话，同一代理的 HTTP 前端即使收到伪造 HTTPS 头也被拒绝。本机 HTTP 登录与健康检查继续可用。检查结果与执行范围见下方验证记录。

| CS-02 修复检查 | 结果 |
| --- | --- |
| `go test ./internal/workspace ./cmd/server ./cmd/desktop ./internal/i18n` | 四个受影响包的完整测试通过。 |
| 上述四个包的 `go vet` | 通过。 |
| workspace 的 Web/HTTPS/Origin、TLS、Realtime、Relay Web、入口应用/取消/恢复及远程桌面等定向 race 检查 | 通过。 |
| Web 设置自动保存、客户端桥、快照、Tailscale/入口设置四个测试文件 | 48 项通过；TypeScript `check` 通过。 |
| `sh tools/security-audit/run.sh web-ingress`（CS-02 修复时） | 明文登录及有效 Cookie 重放均 403；当时 CS-04 的共享代理限流仍可复现，后续修复结果见 CS-04。 |

本轮没有执行全仓库测试、真实公网代理部署或发行二进制安装；本地 TLS 反向代理集成覆盖了转发与认证行为。

**部署兼容性：** 标准本机 Caddy 示例继续适用。共享后端在运行中保存新的公网 HTTPS 地址、启动普通 Web 入口时各检测一次，不做定时轮询。Desktop 完成网址编辑即保存，**检测**按钮用于重试，手动 CIDR 配置折叠在 **高级设置**。Headless server 的 `serve --public-url ...` 启动后自动检测；停服时 `config set` 只保存，下一次启动再检测。代理仍须正确设置转发头，仅有 Public URL 或检测失败不能新增信任。直接 LAN HTTP 登录被关闭，应改用 HTTPS、tsnet 或安全配对 LAN。

**自动检测的边界：** 后端向用户指定的 HTTPS 源发送无登录凭据的请求，保留 TLS 证书校验且不跟随重定向。随机回调路径只在普通 Web 监听器短时有效，使用独立且不向外发送的响应证明，不能靠反射请求通过验证；Desktop 私有入口、Relay 和 Tailscale 不接受该回调。验证 HTTPS 转发信息后，单独保存实际 TCP peer 的单 IP CIDR（IPv4 `/32`、IPv6 `/128`）及验证源，不推断网段。成功复检替换旧自动地址，不修改手动 CIDR，自动信任只对已验证的公网地址有效。成功时在原 App 和监听器内更新公网地址与信任策略，保留运行中的工作及其他待应用更改。新地址保存会取消旧自动检测任务；停止/重启入口会取消其请求，关闭服务等待所属任务结束。失败不新增信任、不阻塞本机启动，已保存输入保留用于重试；已有策略继续有效。运行中更换代理地址后需手动重试或重新启动 Web 入口，多级代理或多个副本可能需手动补充。

**检测功能验证：** `web_proxy_detection_test.go` 覆盖真实本机 TLS 反向代理、证书验证、重复检测、独立响应证明、回调入口隔离、精确 IPv4/IPv6 地址及配置变更/停止/重启/取消，并验证保存地址自动触发、旧任务取消、停服配置等待启动、启动复检替换旧自动地址及保留待应用的手动更改。四个受影响 Go 包的完整测试和 vet、检测与 Web 入口相关定向 race、`web-ingress` 审查回归通过。相关前端五个测试文件 55 项及 TypeScript 检查通过；`web/test/web-proxy-detection-smoke.mjs` 使用真实无头 Chrome 与模拟原生桥验证中文按钮、失焦保存后由底层自动检测（前端不发送检测命令）、自动生效及失败重试。尚未对真实 ngrok 或 Cloudflare Tunnel 部署进行验证。

## CS-03：Android 地址变更会把旧会话投射到新源

**状态：当前工作区已修复。** 本节先保留原始审查行为和影响，再说明修复策略及验证。

**原始代码位置：** `android/transport/spoolnet/server_session.go:87`、`:113`、`:178`，`android/transport/spoolnet/probe.go:108`，`android/app/src/main/java/com/cloudgeek/spool/android/MainActivity.java:941`、`:970`，`ConnectionRouter.java:91`。行号对应原始审查基线。

**触发前提：** 同一个 Android 服务器档案已经登录；用户更改或新增 Public HTTPS/Tailscale 地址，或更换可共享登录的配对身份，同时至少保留一个旧 origins 列表条目；新目标是与原服务器不应共享凭据的一方，并且其 HTTPS 证书有效。需要有配置变更，此项不是任意网页能自动修改地址的证明，也不是不具备域名证书的网络中间人攻击。

`newServerSessionJar` 只要发现新旧 origins 集合有任意交集，就沿用旧 Cookie。该 Cookie 随后被绑定到新客户端的精确 origin。精确匹配因此限制的是修改后的目标，而没有证明新目标有资格接收原来的会话。

连接流程先做健康检查，再读取 `/api/me`，最后仅比较账号的 user ID。健康检查也使用带 jar 的 HTTP 客户端，因此 Cookie 在 `/healthz` 就会发送。`/me` 账号比较发生得太晚，而且账号标识不构成服务器的密码学身份证明。

**本地验证：** 在隔离会话目录保存合成会话，保留原 Tailscale 地址，把 Public 地址替换成另一个 `.example.test` 域名；通过内存 RoundTripper 观察实际 jar 及 `Client.probeOnce`。新目标的 `/healthz` 和 `/api/me` 都收到旧 `spool_session`，即使 `/api/me` 返回 401。此验证不进行外部网络请求，也不使用真实 Cookie。

**影响：** 攻击者控制的新 HTTPS 端点可以得到旧 bearer，并尝试在它可到达的原服务器入口重放。若原服务器所有可达入口还要求独立的设备证书或网络授权，实际利用还需要满足这些额外条件。普通 Public HTTPS 会话不具备原生 Relay 的设备证书绑定。

**建议：** 首先让健康检查使用无认证客户端，避免无必要的凭据投递；但仅去掉 `/healthz` 的 Cookie 不能解决 `/me` 或后续 API 泄漏。共享会话的授权范围应绑定到一个已验证且不可变的服务器身份；配置地址或配对身份发生变化时，新源默认重新登录，或由原受信任服务器对新增源提供可验证授权，再启用共享。先带旧 Cookie 请求新源，再检查响应中的账号/服务器 ID 不能作为修复。

**验收标准：** 在保留一个旧地址的情况下替换另一个地址，新目标在获得有效授权或重新登录前，任何健康检查、API、SSE、WebSocket 都收不到旧凭据；正常、已授权的同服务器切换和退出登录行为继续工作。

**已实现的修复：**

- `server_session.go` 使用带版本的会话记录，按 `https`、`tsnet`、`relay` 分别保存签发源与 Cookie；仅加载连接方式和精确 HTTPS 源均相同的记录，源包含端口。保留其他旧地址不能授权新地址。移除或替换的入口记录会被丢弃，重新添加也不能恢复旧登录。
- Relay 的稳定源来自固定服务器公钥，已验证的 LAN 和中继路径仍共用同一会话；更换配对服务器身份不会继承该会话。不同连接方式即使使用相同 URL，也保留独立登录。
- 旧共享格式无法确认 Cookie 来自哪个入口，升级时清空并要求重新登录；该记录损坏或版本未知时同样不回退到旧文件。更早的独立入口记录只迁移到其原有连接方式与精确源。
- `probe.go` 的健康检查复用既有 TLS 传输及禁止重定向策略，但不使用 Cookie jar，既不发送登录 Cookie，也不接受健康响应的 Set-Cookie。后续 `/me`、SSE、WebSocket 使用已隔离的当前入口会话。
- 退出登录仍清除该档案所有本机入口会话，并保留空版本记录防止旧文件复活；已关闭传输的迟到响应不能写回会话。请求会撤销当前入口的服务端会话，其他入口的服务端令牌只在本机被遗忘，按服务端正常策略到期。

**修复验证：** 地址替换和健康检查回归先在旧实现上失败，修复后通过。`session_security_test.go` 覆盖 Public/Tailscale 地址替换、同源不同连接方式隔离、配对身份变更、移除入口、共享格式升级和健康响应 Cookie；本地 TLS 夹具验证新源在健康检查、`/api/me`、SSE 与 WebSocket 请求中均收不到旧 Cookie。原有会话测试已改为验证各入口独立登录、切换后保留各自登录及档案退出。Android transport 模块完整普通测试、vet、race 检查通过，Java 地址/路由/恢复策略 32 项通过；原 Android overlay 复现现已改为修复回归并通过。未构建 APK 或进行真机安装测试。

## CS-04：普通反向代理后的登录限额被所有用户共享

**状态：当前工作区已修复。** 本节保留原始审查行为和影响，再说明修复策略。

**原始代码位置：** `internal/workspace/server.go:190`，`internal/workspace/auth.go:182`。行号对应原始审查基线。

**触发前提：** 普通 Spool Public HTTPS 入口采用反向代理，多个外部访问者到达后端时具有同一个 `RemoteAddr` 主机 IP。这包括 README 示例中代理到回环后端的常见部署。

登录限流只按 `RemoteAddr` 的主机部分建桶，窗口为 10 分钟、限额 20 次；限额在 JSON/密码校验之前消耗。普通入口不从受信任代理链解析真实客户端 IP，因此改变来源端口或外部访问者并不会分桶。成功登录也不重置该桶。

**本地验证：** 一个代理后客户端发出 20 个缺少 JSON Content-Type 的请求，均返回 415；随后另一个客户端携带正确账号密码登录，返回 429。两者使用不同的合成转发 IP 和不同代理来源端口，仍共享同一个回环 IP 桶。

**影响：** 单一访问者能消耗共享限额，使其他用户在该窗口内无法登录；已有会话不会因此直接失效。此项是登录可用性问题，不是密码暴力破解绕过。

**建议：** 为普通 Web 入口增加显式可信代理 CIDR 策略，从连接 peer 开始解析受信任的 X-Forwarded-For 链，行为与 Relay 现有实现保持一致；同时保留全局资源保护并考虑账号维度限流，避免直接将客户端自报头作为 key。也可先在入口代理按真实来源限流，但后端共享的 20 次限额仍需处理。

Relay 已通过受控元数据通道传递解析后的客户端 IP，并具有代理信任链测试；本项不应误报为当前 Relay 原生/Web 入口的同一个问题。

**验收标准：** 可信代理后的两个独立客户端不共享低额 IP 登录桶；伪造转发头或更换来源端口无法绕过同一客户端的限额；没有可信代理配置时忽略转发头。

**已实现的修复：**

- 普通 HTTP 后端登录使用 `web_security.go` 的客户端地址解析，复用 `web_trusted_proxies` CIDR 设置。从实际 TCP peer 开始，仅在当前节点可信时读取右侧下一个 `X-Forwarded-For` IP，遇到第一个不可信 IP 停止；支持多行头、多级可信代理及 IPv4/IPv6 规范化。
- 不使用 `X-Real-IP` 或 `Forwarded` 选择登录桶。不可信 peer、空/无效代理配置、缺失转发信息或可信链节点格式错误时保留实际 peer。可信链最多解析 64 个节点；过长的不可信前缀不会让客户端改用代理的回退桶。
- 每个 IP 的 10 分钟 20 次限额及既有桶数量保护继续保留，格式错误的登录仍消耗额度。没有增加全代理共享的 20 次额度；同一 NAT 出口下的用户仍共用公网 IP 桶。代理需要覆盖或追加实际来源，且不能将所有客户端网段列为可信代理。
- 只为普通 HTTP 后端解析 Web 代理头；TLS 入口保留其传输提供的地址，Relay 控制通道提供的 `relay-client/<ip>` 身份继续独立分桶，客户端 HTTP 头不能覆盖它。

**修复验证：** `login_limit_test.go` 中的本机 HTTP 反向代理回归先在旧实现上失败，修复后通过：一个客户端耗尽额度后仍为 429，其他 IPv4/IPv6 客户端使用正确密码登录为 200；改变来源端口、映射 IPv4 表示或转发头前缀（包括过长、错误格式前缀）不能绕过已耗尽的客户端桶。单元测试覆盖代理链信任与保守回退，既有 Relay Web 登录隔离回归继续通过。审查 overlay 已改为断言 CS-04 的修复行为。

| CS-04 修复检查 | 结果 |
| --- | --- |
| `go test ./internal/workspace` | 受影响包完整测试通过。 |
| `go vet ./internal/workspace` | 通过。 |
| workspace 的客户端地址解析、HTTPS 代理限流、Relay Web 限流、传输策略及入口配置应用/取消等定向 race 检查 | 通过。 |
| `sh tools/security-audit/run.sh web-ingress` | 明文登录及有效 Cookie 重放均 403；代理后独立客户端登录为 200，耗尽额度的客户端伪造前缀及更换端口后仍为 429。 |

本轮未执行全仓库测试或真实公网代理部署；本机反向代理集成使用合成前端 IP 和实际后端 HTTP 连接，覆盖转发链与登录行为。

## 已核对的防护

- 登录会话为 32 字节随机 token，数据库保存哈希；Cookie 有 HttpOnly、SameSite=Strict，TLS/正确配置的远程 HTTPS 使用 Secure；写接口要求会话绑定的 CSRF。
- WebSocket 先经应用级 Origin 检查，再验证首条 hello 的 CSRF，并持续处理会话撤销；SSE 和远程桌面也复核授权。`realtime.go` 中 WebSocket 库的 `InsecureSkipVerify` 跳过的是库内置 Origin 检查，前置应用检查仍存在，不是 TLS 证书校验开关。
- `relay/identity.go` 中 TLS 的 `InsecureSkipVerify` 配有自定义 `VerifyConnection`，检查固定 Ed25519 公钥、主机名、有效期及证书签名；不能仅凭关键词判定为“关闭证书验证”。原生设备侧另有 TLS 客户端私钥证明、本地批准及撤销。
- Android API 只允许当前源下的相对 `/api/` 路径，拒绝重定向、跨源附件请求和路径逃逸；tsnet 只能拨号到所选 peer 的 Tailscale IP，证书校验仍使用原 HTTPS 主机名。
- Desktop 设置绑定校验调用者页面对象及文档地址，普通聊天与远程客户端没有管理权限；外链在外部打开，WebView 内导航限制到精确源。
- 远程桌面代理剥离 Spool Cookie、CSRF、Referer 和视图 token 后再转发本机 helper，内部密码只由服务端注入；退出或撤权会关闭相关连接。
- Web/PWA Service Worker 排除 `/api/`，认证 API 使用 no-store；HTML 文件预览使用无许可的 sandbox iframe；Web Push 的不可信 endpoint 经出站地址限制和无重定向客户端处理。

## 验证记录与复现

可复用审查脚本及测试保存在 [tools/security-audit](../tools/security-audit/)。其中审查探针通过 Go overlay 使用既有隔离测试夹具，不在普通 `go test ./...` 中执行，也不改写产品源文件。Android 的常规安全回归同时保存在 transport 模块测试中。

从仓库根目录执行：

```sh
# 四项原始审查问题的修复回归；CHROME_BIN 换成实际路径。
CHROME_BIN='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' \
  sh tools/security-audit/run.sh repro

# Android 配置变更修复回归，不需要浏览器。
sh tools/security-audit/run.sh android

# 普通 Web 明文入口与反向代理登录限流修复回归。
sh tools/security-audit/run.sh web-ingress

# 本次修复的完整 transport 模块验证。
sh tools/run.sh android-session-check sh -eu -c '
  go -C android/transport test ./...
  go -C android/transport vet ./...
  go -C android/transport test -race ./...
'

# 相关现有 Go race 回归、Web 测试、Android Java 策略测试。
sh tools/security-audit/run.sh regression
sh tools/security-audit/run.sh web
sh tools/security-audit/run.sh java
```

脚本通过 `tools/run.sh` 在系统临时目录的 `spool-<user>/spool/` 命名空间内创建独立运行目录，隔离临时状态并在结束时清理；Go 使用工具默认缓存并保留调用方显式覆盖及现有 GOMODCACHE。审查脚本使用已缓存 Go 模块（`GOPROXY=off`），overlay 由 Bun 生成。Java 模式需要现有 JUnit/Hamcrest 缓存、SDK 36.1 及 JDK 17+，支持 `JAVA_HOME`、`ANDROID_HOME`/`ANDROID_SDK_ROOT`。缺少依赖应使用项目正常安装流程，不应另建系统临时缓存。浏览器及本地端口测试需要环境允许本地监听与启动测试进程。

以下日志记录原始审查基线上的行为，所有修复后的结果以上节验证为准：

| 验证 | 结果 | 证据 |
| --- | --- | --- |
| 本轮四项审查行为复现 | 全部成功；其中普通 HTTP 有两个 Public URL 子用例；含真实无头 Chrome 同源行为及实际 Android 健康请求路径 | `build/artifacts/communication-security/repro.log` |
| 本轮 Android 地址/路由/恢复策略 | 32 项通过 | `build/artifacts/communication-security/java.log` |
| 前一轮同一基线的 Go 安全相关 race 回归 | workspace 相关测试，tailnet、netproxy、appupdate、Relay 两包、Android transport 两包及 Desktop 相关测试均通过 | `build/artifacts/communication-security/regression.log` |
| 前一轮同一基线的 Web 回归 | 429 项通过、0 失败；此前沙箱本地监听失败已重跑排除 | `build/artifacts/communication-security/web.log` |
| 本轮脚本/补充文件检查 | Shell 语法及 diff 空白检查通过 | `sh -n`、`git diff --check` |

上述回归日志来自原始审查基线，未将它们作为修复后全部子系统通过的证明。Android 修复涉及的 transport 检查、Android overlay 回归和 Java 策略测试已另行重跑；普通 Web 传输修复按实际改动选择 workspace、Server CLI、Desktop、本地化及设置页检查，后续代理限流修复检查 workspace 与对应审查探针。测试只使用合成数据；每条通过结果都仅代表其已覆盖的断言，不是对三端产品的全面安全认证。

## 修复顺序与审查范围

1. Relay Web 独立子域名方案已在当前工作区实现。部署时同时升级 Relay 和 Spool，准备 DNS 与代理，使用新地址重新登录并重新安装 PWA；卸载旧共享源的 PWA。管理员可独立停用 Web 发布，使用者仍须信任 Relay 与前置 TLS 代理。
2. 普通明文 LAN 登录入口已关闭，保留明确的本机/可信 HTTPS 代理后端策略；部署需核对代理实际 CIDR 和转发头，并保护代理到后端链路。
3. Android 配置变更时的会话授权范围及健康检查会话隔离已在当前工作区修复，升级需重新登录；发行前仍需通常的 Android 构建及设备验证。
4. 普通 Public HTTPS 已按可信代理链解析真实客户端并独立分桶，保留既有桶数量保护；代理需正确传递真实来源，多级部署需配置每一级可信代理。账号维度和额外全局资源限额仍可作为后续增强。

审查覆盖当前仓库中这些入口、API/SSE/WebSocket、会话共享、配对/发现、远程桌面代理、推送及更新通信。没有访问公网目标、真实服务端数据库、开发者 provider 会话或真实手机数据；没有在 Windows/Linux/macOS/Android 的全部发行二进制、真机网络和外部反向代理配置上逐一实测。MyGo、Tailscale、浏览器、voxl 及 provider CLI 的上游实现与完整依赖漏洞扫描不属于本次完成的范围；本报告关于它们的结论限于仓库中的集成方式。
