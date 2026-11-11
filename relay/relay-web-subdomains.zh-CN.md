# Relay Web 子域名、代理与证书

每台服务器使用 `https://<server-id>.web.relay.example.com/`，域名标签来自服务器
Ed25519 公钥的完整 SHA-256 标识。Relay 验证注册凭据和签名发布意愿后分配地址，
客户端验证返回地址绑定自身身份。服务器不能认领其他服务器的子域名。

## 一次性准备

准备域名并添加 DNS 记录：

| 记录 | 目标 | 代理转发 |
| --- | --- | --- |
| `relay.example.com` | 入口公网 IP | Relay `28080` |
| `*.web.relay.example.com` | 同一个入口 | Relay `28080` |
| `admin.example.com` | 管理入口 | Relay `28081` |

通配 **DNS** 不等于通配 **证书**。逐域名模式不需要 DNS API；证书验证要求公网
80/443 到达证书管理代理。保留原始 Host，支持 WebSocket 和不缓冲的 SSE。
`28082` 是独立的代理集成接口，只允许代理内部网络访问，不转发到公网域名。

下方样例统一使用 `relay.example.com`、`admin.example.com` 和
`*.web.relay.example.com`，请全部替换为自己的域名。逐域名和通配证书模式都需要通配
DNS 记录，便于新服务器自动获得可解析的地址；逐域名模式不需要 DNS API。

代理和 Relay 在同一个 Docker 网络时，样例中的 `relay` 是 Relay 的稳定网络别名。
代理运行于宿主机时，将 `relay:28080`、`relay:28081`、`relay:28082` 分别换成
`127.0.0.1:28080`、`127.0.0.1:28081`、`127.0.0.1:28082`。其他容器中的代理必须
加入相同网络，不能使用 `127.0.0.1` 指向 Relay。

通过部署环境变量 `RELAY_PUBLIC_URL` 设置公网 HTTPS 地址。Web 后缀固定为
`web.<主入口域名>`：主入口为 `relay.example.com` 时，服务器地址为
`https://<server-id>.web.relay.example.com/`，端口沿用主入口。管理页只读展示公网地址
和自动派生的后缀，不再支持 `RELAY_WEB_DOMAIN` 自定义。修改前先准备好
`*.web.<主入口域名>` 的 DNS、代理和证书，再重新部署 Relay；
新连接将使用新地址，旧会话不会迁移。
可信代理及服务器授权由管理页管理。启动时最多等待公网 HTTPS 入口就绪 5 分钟，
`/healthz` 一旦正常就立即自动检测代理 IP，无需等满 5 分钟，也可手动重试。
等待期间管理页显示“等待代理…”并禁用检测按钮；完成或超时后恢复按钮。

## 配置文件与动态管理

| 文件 | 用途 | 启动方式 |
| --- | --- | --- |
| `.env.example` | Docker Compose 的完整部署模板，含可选 Caddy | 复制为 `.env` 后执行 `docker compose up -d --build` |
| `deploy/relay.env.example` | 直接运行 Relay 二进制／systemd 的进程环境模板，不启动代理 | 由 `deploy/spool-relay.service` 的 `EnvironmentFile` 读取 |

无内置 Caddy 的 Compose 部署仍使用 `.env.example`，将 `COMPOSE_PROFILES` 留空，
另行配置自己的 Caddy、Nginx 或 Coolify。systemd 模板也需要单独安装 Relay 二进制、
服务单元和前置代理，单独复制 env 文件不会完成部署。

管理页可修改额外可信代理、生成服务器 token、停用／允许 Web 和撤销授权。
`GET /v1/settings` 返回部署地址和运行状态；`PUT /v1/settings` 只接受
`browser_origin` 与可选 `trusted_proxies` 数组，省略数组保留原值、空数组清空手动
列表。`public_url`、`web_domain` 和证书模式不能通过管理 API 修改。
`POST /v1/proxy-detection` 立即重试部署的公网地址。启动等待会检查公网 `/healthz`，
必须通过正常 TLS 校验并返回 Relay 的健康响应；就绪后仍验证独立的一次性回调，
健康响应本身不会授予代理信任。成功或等待超时后停止，不做持续周期轮询。

设置文件版本 4 仅保存运行状态：手动 CIDR、管理源、自动验证的代理 IP／源／时间。
从旧设置文件升级时保留手动 CIDR；旧公网地址、后缀和证书模式不再生效，部署时需
明确填写公网地址。旧 `RELAY_WEB_DOMAIN` 环境变量不再读取，Web 地址统一使用
`web.<主入口域名>` 后缀。自动代理信任只在验证源仍与部署公网地址相同时保留，改变或清空
公网地址会丢弃旧自动信任。环境变量修改后需要重新部署；`docker compose restart`
不会加载新的 env。

## Docker：可选自带 Caddy

在 `relay/` 复制 `.env.example` 为 `.env`，使用 `openssl rand -hex 32` 生成管理员
凭据。逐域名证书的完整主要设置如下，其余字段保留 `.env.example` 默认值：

```dotenv
COMPOSE_PROFILES=caddy
SPOOL_RELAY_ADMIN_TOKEN=<替换为生成的管理员凭据>
RELAY_PUBLIC_HOST=relay.example.com
RELAY_ADMIN_HOST=admin.example.com
RELAY_PUBLIC_URL=https://relay.example.com
CADDY_CERTIFICATE_MODE=individual
```

从 `relay/` 目录启动：

```sh
docker compose up -d --build
```

`COMPOSE_PROFILES=caddy` 启动配套 Caddy；`COMPOSE_PROFILES=` 只启动 Relay。
Relay、Caddy 分别使用独立持久化卷保存注册记录和证书。域名始终以部署环境变量
为准，不会被旧管理页设置覆盖。Caddy 的证书方案也来自 `.env`，与 Relay 的运行
状态分开。改变这些部署配置后执行 `docker compose up -d` 更新受影响的容器。
Compose 将后端端口固定发布到宿主机 `127.0.0.1`；容器代理使用相同 Docker 网络
和服务名访问，不需要向宿主机公网地址开放后端。

默认 `CADDY_CERTIFICATE_MODE=individual`：Caddy 首次收到某子域名的 TLS 请求时，
向 `http://relay:28082/v1/tls/allow?domain=<hostname>` 查询是否允许，再申请普通证书。
首次访问可能等待几秒，证书由 Caddy 自动续期。未知、未发布、被管理员停用或撤销
的身份不会获得签发资格。临时离线保留资格，避免重连导致配置和证书反复变动。
证书签发受 CA 限额约束，大量租户宜使用通配证书。

通配模式填写：

```dotenv
CADDY_CERTIFICATE_MODE=wildcard
CLOUDFLARE_API_TOKEN=<限定到对应 DNS zone 的 token>
```

其他域名字段与上面的逐域名样例相同。配套镜像自动选择对应 Caddyfile，不需要
另写代理配置。执行 `docker compose up -d` 更新 Caddy，无需修改 Relay 管理页。

配套镜像包含 Cloudflare DNS 插件，token 需要对应 zone 的 DNS 编辑与读取权限，
仅传给 Caddy 容器。Relay 不接收、不保存 DNS 密钥。此模式用 DNS-01 自动申请和
续期 `*.web.<RELAY_PUBLIC_HOST>` 证书。其他 DNS 服务商可使用自己的含对应 DNS 插件的
Caddy 镜像和 Caddyfile；已有通配证书也可以在外部代理加载。

切换到外部代理前，停止之前的配套 Caddy，释放 80/443：

```sh
docker compose --profile caddy stop caddy
# 再清空 .env 中的 COMPOSE_PROFILES，执行 docker compose up -d
```

## 外部 Caddy / Nginx

宿主机上的代理使用发布到回环的 28080、28081、28082。另一个容器中的代理加入
Relay 的 Docker 网络，使用 `relay:28080` 等服务地址，无需发布后端公网端口。

外部 Caddy 部署时将 Relay 的 `.env` 设置为 `COMPOSE_PROFILES=`，保留对应的
`RELAY_PUBLIC_URL`；`CADDY_CERTIFICATE_MODE` 仅用于内置 Caddy，
外部代理通过自己的配置选择证书方案。

### Caddy：逐域名证书

证书申请由外部 Caddy 的以下 Caddyfile 决定：

```caddyfile
{
    email admin@example.com
    on_demand_tls {
        ask http://relay:28082/v1/tls/allow
    }
}

relay.example.com {
    reverse_proxy relay:28080
}

admin.example.com {
    reverse_proxy relay:28081
}

https:// {
    tls {
        on_demand
    }
    reverse_proxy relay:28080
}
```

主域名和管理域名按普通方式自动申请证书。服务器子域名首次访问时，Caddy 会向
Relay 查询签发资格，只有已经发布且未被停用的服务器域名才获批。无需填写
Traefik token 或 DNS API。Caddy 的 ask 请求不支持自定义 Bearer 头，因此查询
端口必须保持私有。持久化 Caddy 的 `/data`，保留 ACME 账号和证书。

### Caddy：通配证书，以 Cloudflare 为例

无需在 Relay 中选择证书类型。外部 Caddy 必须包含 Cloudflare DNS
插件，可以使用项目的 `relay/deploy/caddy/Dockerfile` 构建镜像。将
`CLOUDFLARE_API_TOKEN` 仅注入 Caddy 进程或容器的环境变量，Caddyfile：

```caddyfile
{
    email admin@example.com
}

relay.example.com {
    reverse_proxy relay:28080
}

admin.example.com {
    reverse_proxy relay:28081
}

*.web.relay.example.com {
    tls {
        dns cloudflare {env.CLOUDFLARE_API_TOKEN}
    }
    reverse_proxy relay:28080
}
```

Caddy 用 DNS-01 申请及续期 `*.web.relay.example.com`，无需为每台服务器查询签发许可。
Relay 仍逐个检查域名、身份及发布权限。示例中主域名和管理域名使用普通自动
证书，因此仍应让公网 80/443 到达 Caddy；通配证书本身通过 DNS 验证。

Nginx 可配置通配 server_name 后统一转到 Relay，加载已有通配证书；使用逐域名
证书时由部署者的 ACME 工具添加具体域名和证书。Relay 不修改 Nginx 配置。

## Coolify / Traefik

关闭配套 Caddy，并提供独立只读
`RELAY_TRAEFIK_TOKEN`（至少 32 字符）。通过 `RELAY_PROXY_UPSTREAM` 指定代理能访问
的 Relay 服务地址，默认 `http://relay:28080`。两容器需共享 Docker 网络并配置
稳定服务名/别名，不能使用另一个容器内的 `127.0.0.1`。

### Relay 侧设置

Relay 的 `.env` 样例，其余字段保留原设置：

```dotenv
COMPOSE_PROFILES=
SPOOL_RELAY_ADMIN_TOKEN=<管理员凭据>
RELAY_PUBLIC_URL=https://relay.example.com
RELAY_TRAEFIK_TOKEN=<另外生成的独立凭据，至少32字符>
RELAY_TRAEFIK_RESOLVER=letsencrypt
RELAY_PROXY_UPSTREAM=http://relay:28080
```

两个凭据分别生成，不能相同。`RELAY_TRAEFIK_TOKEN` 在 Relay 与 Traefik 中填写
相同值；它不作为访问 Relay 管理页的凭据。

`RELAY_TRAEFIK_RESOLVER` 指定 Traefik 中已有的解析器名称，默认 `letsencrypt`。
填 `none` 时只启用 TLS，使用代理已加载的匹配证书，不触发证书申请。Relay 给自己的每条
子域名 HTTPS 路由显式设置它，不需要改变共享 HTTPS 入口的默认策略。解析器的
ACME 验证方式、账号、证书存储及 DNS API 凭据仍在 Traefik 中配置。

### Traefik / Coolify：逐域名证书

Coolify v4.4.7 起，在 Relay 应用的 **Advanced → Proxy → Certificate resolver**
选择 **Server setting**（确认显示的名称是 `letsencrypt`），或选择 **Custom resolver**
并填写已有解析器名称。若以 Service
部署，打开 Service 后选中对应 service application，在其设置中选择。保存后重新
部署应用或重启服务；Docker Compose 应用的这个选项作用于该应用的所有服务。

Relay 部署环境中的 `RELAY_TRAEFIK_RESOLVER` 填同一个名称。两处设置分别控制
Coolify 为主域名/管理域名生成的路由，以及 Relay 通过 HTTP Provider 生成的服务器
子域名路由。Coolify 的资源设置不会自动传给 Relay。旧版 Coolify 没有这个选项时，
在该资源的自定义标签中设置各 HTTPS 路由的 `tls.certresolver`。

Coolify 中打开 **Servers → 对应服务器 → Proxy → Configuration**，在已有 Traefik
`command` 中追加以下参数；不要替换原有 Docker/file provider、端口和证书存储：

```yaml
- --providers.http.endpoint=http://relay:28082/v1/traefik
- --providers.http.pollInterval=5s
- --providers.http.pollTimeout=3s
- --providers.http.headers.Authorization=Bearer <独立的 RELAY_TRAEFIK_TOKEN>
```

保存并重启一次代理。这里是服务器级代理配置，不是应用的 Advanced → Proxy。
Relay 主域名和管理域名继续通过 Coolify Domains 指向 28080 和 28081；不要为租户
子域名重复添加相同路由。内部 Provider 接口不使用管理员 token，也不提供管理操作。

不要为 Relay 增加 `--entrypoints.https.http.tls.certresolver=letsencrypt`：这是共享入口
默认值，会影响同入口下没有显式 TLS 配置的其他路由。如果此前仅为 Relay 添加了
该行，应删掉它；已有入口策略按原部署需要保留。上述 HTTP Provider 参数只增加
Relay 配置来源，不改变其他项目的证书选择。

Coolify 默认已有 `letsencrypt` 的 ACME 配置；不要重复添加相同参数。独立部署
Traefik 时，需要自行配置入口和解析器，例如在静态 `traefik.yaml` 中使用：

```yaml
entryPoints:
  http:
    address: ":80"
  https:
    address: ":443"

certificatesResolvers:
  letsencrypt:
    acme:
      email: admin@example.com
      storage: /traefik/acme.json
      httpChallenge:
        entryPoint: http

providers:
  http:
    endpoint: http://relay:28082/v1/traefik
    pollInterval: 5s
    pollTimeout: 3s
    headers:
      Authorization: "Bearer <独立的 RELAY_TRAEFIK_TOKEN>"
  file:
    directory: /etc/traefik/dynamic
    watch: true
```

独立部署还需要挂载静态配置及动态配置目录、发布 80/443，并持久化
`/traefik/acme.json` 所在目录。Relay 的 HTTP Provider 只输出服务器子域名路由；
主域名和管理域名单独放到 `/etc/traefik/dynamic/spool-entry.yaml`：

```yaml
http:
  routers:
    spool-relay-entry:
      rule: 'Host(`relay.example.com`)'
      entryPoints: [https]
      service: spool-relay-entry
      tls:
        certResolver: letsencrypt
    spool-admin-entry:
      rule: 'Host(`admin.example.com`)'
      entryPoints: [https]
      service: spool-admin-entry
      tls:
        certResolver: letsencrypt
  services:
    spool-relay-entry:
      loadBalancer:
        servers:
          - url: http://relay:28080
    spool-admin-entry:
      loadBalancer:
        servers:
          - url: http://relay:28081
```

上述两个路由各自指定解析器。Coolify 已通过 Domains 配置这两个域名时，
不要再添加这份入口动态配置。

入口名称使用 Coolify 默认的 `http`、`https`；Relay 的 HTTP 路由重定向到 HTTPS。
更换解析器时，先在 Traefik 中准备好对应配置，再修改 Relay 环境变量并重建 Relay
容器；主域名/管理域名按需同步调整 Coolify 资源设置或上面的动态配置。只修改
Relay 的选择不会修改其他项目的路由，也不会创建 Traefik 解析器。

### Traefik / Coolify：通配证书，以 Cloudflare 为例

Relay 部署环境设置 `RELAY_TRAEFIK_RESOLVER=none`，自己的子域名路由显式输出
`tls: {}`，使用代理已加载的证书。此设置不创建或签发通配证书。
Coolify 中被通配证书覆盖的 Web 资源选择 **None (use your own certificates)**；
主入口 `relay.example.com` 和管理域名 `admin.example.com` 不在
`*.web.relay.example.com` 的覆盖范围内，继续使用各自的解析器或匹配证书。
若处于同一个 Compose 应用，资源选项会
同时生效，可拆开资源分别选择，或保留 `letsencrypt`：Traefik 对已有匹配证书的域名
直接复用，对没有匹配证书的域名正常申请。

保留 HTTP Provider 配置，给 Traefik 增加一个独立的 DNS 解析器，原有
`letsencrypt` 解析器可继续服务其他应用。

在 Coolify 的同一个 Proxy → Configuration 中，将以下环境变量加入 Traefik 服务：

```yaml
environment:
  CF_DNS_API_TOKEN: <限定到对应 DNS zone 的 Cloudflare token>
```

在该服务已有 `command` 中追加：

```yaml
- --certificatesresolvers.spooldns.acme.email=admin@example.com
- --certificatesresolvers.spooldns.acme.storage=/traefik/spool-dns-acme.json
- --certificatesresolvers.spooldns.acme.dnschallenge.provider=cloudflare
```

在 **Servers → 对应服务器 → Proxy → Dynamic Configurations** 新建
`spool-wildcard.yaml`。这份样例使用 Coolify 文档的低优先级占位路由申请通配
证书，不替换共享代理的全局默认证书：

```yaml
http:
  routers:
    spool-wildcard-certificate:
      rule: 'HostRegexp(`[a-z0-9-]+\.web\.relay\.example\.com`)'
      entryPoints: [https]
      priority: 1
      service: spool-wildcard-placeholder
      tls:
        certResolver: spooldns
        domains:
          - main: web.relay.example.com
            sans:
              - '*.web.relay.example.com'
  services:
    spool-wildcard-placeholder:
      loadBalancer:
        servers: []
```

独立部署时，把这份 YAML 放入上面的 `/etc/traefik/dynamic/` 目录，并将等价的
`spooldns` 解析器加入静态配置。`spool-dns-acme.json` 所在目录需要持久化。
Cloudflare token 需要对应 zone 的 DNS 编辑与读取权限，只提供给 Traefik。

保存主配置并重启一次代理，等待日志显示证书申请成功。占位路由的优先级为 1，
实际服务器由 Relay 输出的精确 Host 路由接管；没有发布的域名不会获得服务器
内容。Provider 在 `RELAY_TRAEFIK_RESOLVER=none` 时显式设置 `tls: {}`，使用已加载的通配证书，不继承
入口的逐域名签发设置。

已有通配证书时，可以省去 DNS 解析器和占位路由，改用文件 Provider 加载：

```yaml
tls:
  certificates:
    - certFile: /certs/relay-fullchain.pem
      keyFile: /certs/relay-privkey.pem
```

证书文件路径是 Traefik 容器内的路径，需要挂载并自行管理续期。

Provider 每次返回完整快照。临时离线的发布记录保留；关闭 Web 后下一次认证连接
更新发布意愿；撤销、注销、管理员停用 Web 立即取消 Relay 入口和证书授权，
Traefik 在下次读取时移除路由。移除路由不承诺删除已经签发的证书。

无需在 Relay 中选择代理类型，也没有 `RELAY_PROXY_TYPE` 设置。Caddy 和 Traefik
各自使用对应的内部接口；`RELAY_TRAEFIK_TOKEN` 只用于 Traefik 获取配置，与是否
启动自带 Caddy、是否使用通配证书相互独立。
`RELAY_TRAEFIK_RESOLVER` 只引用 Traefik 中的解析器，作用于 Relay 的逐域名路由；
它不是 token，也不修改共享代理入口。Caddy 不使用这个选项。

## 安全边界与迁移

- 公网域名只能访问对应身份，租户域名不能访问其他 `/web/<id>/` 路径或管理接口。
- Web 使用 `__Host-spool_session`、Secure、HttpOnly、Path=/，不设置父域 Domain。
  普通入口与原生客户端仍使用原来的会话名；Web 不接受旧的普通会话 Cookie。
  Relay 剥离租户响应的 `Clear-Site-Data`，防止它清除兄弟子域的登录 Cookie。
- Origin 精确匹配当前子域名，写请求仍验证 CSRF；WebSocket 也检查来源。
  兄弟子域属于 same-site，不能依靠 SameSite 代替 Origin/CSRF。
- 每个来源独立管理 PWA 身份、Service Worker、缓存和浏览器存储。禁止使用
  `document.domain` 放宽边界。原生 Relay 的固定公钥和设备配对策略保持有效。
- 旧公网 `/web/<id>/` 返回 410 和清理站点数据的响应，不再承载任何租户页面。
  用户从 Spool 设置打开新地址、重新登录并重新安装 PWA。已缓存的旧 PWA 可能在
  访问网络前显示旧页面，建议卸载旧安装；新子域不会继承其 Cookie 和存储。
  自动清理只涉及旧源的缓存和存储，不清理 Cookie，以免浏览器连带注销兄弟子域。
  旧 Cookie 无法用于新入口，可由用户清理旧站点数据或等待它自然到期。
- 管理员停用 Web 绑定服务器身份，自行注销、自动重新注册或更换 token 都会继承
  停用状态；管理员在当前授权记录上允许 Web 后才解除。此状态跨 Relay 重启保留。
- Web v2 发布意愿纳入注册签名。旧客户端仍可使用原生连接，无法恢复共享源发布。

子域名隔离解决不同服务器运营者之间的 CS-01 风险。Relay 及前置 TLS 代理仍能
读取浏览器业务明文，使用者依旧需要信任这两者；原生 Android 使用内层端到端 TLS。

参考：[Caddy On-Demand TLS](https://caddyserver.com/docs/automatic-https#on-demand-tls)、
[Traefik HTTP Provider](https://doc.traefik.io/traefik/providers/http/)、
[Traefik TLS 证书](https://doc.traefik.io/traefik/reference/routing-configuration/http/tls/tls-certificates/)、
[Coolify 代理配置](https://coolify.io/docs/core/networking/proxy/traefik/overview)、
[Coolify 资源证书解析器](https://coolify.io/docs/core/networking/proxy/traefik/certificate-resolver)、
[Traefik 入口 TLS 默认值](https://doc.traefik.io/traefik/reference/install-configuration/entrypoints/)、
[Coolify 通配证书](https://coolify.io/docs/core/networking/proxy/traefik/wildcard-certs)。
