<div align="center">
  <img src="web/src/assets/logo.svg" alt="AirGate" width="120" />

  <h1>AirGate Core</h1>

  <p><strong>可插件化的统一 AI 网关运行时</strong></p>

  <p>
    <a href="https://github.com/DouDOU-start/airgate-core/releases"><img src="https://img.shields.io/github/v/release/DouDOU-start/airgate-core?style=flat-square" alt="release" /></a>
    <a href="https://github.com/DouDOU-start/airgate-core/pkgs/container/airgate-core"><img src="https://img.shields.io/badge/ghcr.io-airgate--core-blue?style=flat-square&logo=docker" alt="ghcr.io" /></a>
    <a href="https://github.com/DouDOU-start/airgate-core/blob/master/LICENSE"><img src="https://img.shields.io/github/license/DouDOU-start/airgate-core?style=flat-square" alt="license" /></a>
    <img src="https://img.shields.io/badge/Go-1.25-00ADD8?style=flat-square&logo=go" alt="go" />
    <img src="https://img.shields.io/badge/React-19-61DAFB?style=flat-square&logo=react" alt="react" />
  </p>

  <p>
    <strong>中文</strong> · <a href="README_EN.md">English</a>
  </p>
</div>

---

AirGate 不是又一个"集成了 N 个 AI 平台"的网关，而是一套**把平台能力做成插件、运行时按需装载**的开放架构。

- **Core**（本仓库）= 用户、账号、调度、计费、限流、订阅、管理后台 —— 所有平台无关的通用能力
- **Plugin** = 一个独立的 Go 进程，通过 gRPC 实现 SDK 定义的接口，提供具体平台的转发逻辑

平台插件可以**独立发版、独立 release、独立装卸、独立热更**，Core 不重启、其他插件不受影响。这意味着你可以只装自己需要的能力，也可以为内部场景写私有插件接进来。

## ✨ 核心特性

- **🔌 插件化运行时** — 平台能力解耦为独立 gRPC 子进程（基于 hashicorp/go-plugin），支持上传安装 / GitHub Release 安装 / 开发模式热重载，零停机
- **🧩 路由动态注入** — 插件声明的 HTTP 路由由 Core 自动注册到网关，账号表单字段和前端组件自动嵌入管理后台
- **🎯 多账号智能调度** — 优先级 + 健康状态 + 并发上限自动选号，账号异常自动降级
- **💰 精确计费** — 按 token × 模型单价实时记账，支持费率倍率、用户余额、订阅与配额
- **🛡 完整管理后台** — 用户/分组/账号/订阅/IP/代理池/插件市场/系统设置一站式管理，支持账号导入导出、自动刷新、API Key 鉴权
- **📦 一键部署** — 镜像化分发到 ghcr.io，多架构（amd64/arm64），用户 `docker compose up -d` 即可

## 🧩 插件生态

### 已发布插件

| 插件 | 类型 | 能力 | 仓库 |
|---|---|---|---|
| **gateway-openai** | gateway | OpenAI Responses / Chat Completions / ChatGPT OAuth / Anthropic 协议翻译 / WebSocket | [DouDOU-start/airgate-openai](https://github.com/DouDOU-start/airgate-openai) |
| **gateway-claude** | gateway | Claude Messages API 网关：OAuth 授权、TLS 指纹、用量监控 | [DouDOU-start/airgate-claude](https://github.com/DouDOU-start/airgate-claude) |
| **gateway-kiro** | gateway | Kiro (AWS CodeWhisperer) 反代网关，兼容 Anthropic Messages API | [DouDOU-start/airgate-kiro](https://github.com/DouDOU-start/airgate-kiro) |
| **airgate-playground** | extension | AI 对话插件：网页聊天、多模型切换、会话管理 | [DouDOU-start/airgate-playground](https://github.com/DouDOU-start/airgate-playground) |
| **airgate-studio** | extension | 面向图片、视频、音频等多模态内容生成的统一创作中心 | [DouDOU-start/airgate-studio](https://github.com/DouDOU-start/airgate-studio) |
| **payment-epay** | extension | 多渠道支付：易支付（虎皮椒/彩虹）/ 支付宝官方 / 微信支付官方，含充值页、订单管理、服务商配置 | [DouDOU-start/airgate-epay](https://github.com/DouDOU-start/airgate-epay) |
| **airgate-health** | extension | AI 提供商健康监控：主动探测、可用率/延迟聚合、对外公开状态页 | [DouDOU-start/airgate-health](https://github.com/DouDOU-start/airgate-health) |

### 安装插件

打开管理后台 → **插件管理** → 三种方式任选：

```text
1. 插件市场 → 点击「安装」    （从 GitHub Release 自动拉取，匹配当前架构）
2. 上传安装 → 拖入二进制文件   （适合内部插件）
3. GitHub 安装 → 输入 owner/repo（适合未列入市场的插件）
```

市场会**定时从 GitHub API 同步**每个插件的最新 release 版本（默认 6h，使用 ETag 不消耗 API 配额），也可以在市场页点刷新按钮手动同步。

### 写一个自己的插件

只需依赖 [airgate-sdk](https://github.com/DouDOU-start/airgate-sdk)，实现 `GatewayPlugin` 接口的几个方法即可：

```go
type GatewayPlugin interface {
    Info() PluginInfo                    // 元信息：ID、版本、账号字段、前端组件
    Platform() string                    // 平台键
    Models() []ModelInfo                 // 模型列表 + 单价（用于计费）
    Routes() []RouteDefinition           // HTTP 路由声明
    Forward(ctx, req) (*ForwardResult, error)  // 实际转发逻辑
}
```

参考 [airgate-openai](https://github.com/DouDOU-start/airgate-openai) 完整范例，含 Makefile、release workflow、前端嵌入。

## 🛠 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.25 · Gin · Ent ORM · PostgreSQL 17 · Redis 8 |
| 前端 | React 19 · Vite · TanStack Query · Tailwind CSS |
| 插件协议 | hashicorp/go-plugin (gRPC) |
| 部署 | Docker Compose · GitHub Container Registry · 多架构 (amd64/arm64) |
| 鉴权 | JWT + 管理员 API Key |

## 🚀 部署

两条路任选其一。两者都可以在生产使用。

| 路径 | 适用场景 | 你需要自备 |
|---|---|---|
| **1A. 裸金属 install.sh** | 已经有 PostgreSQL + Redis，想要最轻量的部署，喜欢 systemd | PostgreSQL 15+ / Redis 7+ |
| **1B. Docker Compose** | 干净的服务器，想一把梭把 pg + redis + core 全部用容器跑起来 | 仅 Docker |

> ⚠️ 二选一，不要混用。如果选 1A 又跑了 1B 的 docker compose，会出现两套数据库实例互相打架。

### 方式 1A：裸金属安装（systemd，自备 PostgreSQL + Redis）

```bash
curl -sSL https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/install.sh | sudo bash
```

[install.sh](deploy/install.sh) 会：

1. 检测 OS / 架构（linux/amd64 或 linux/arm64）
2. 从 GitHub Releases 下载对应平台的 `airgate-core-{os}-{arch}` 二进制（前端 SPA 与翻译文件已经 `//go:embed` 进 binary，单文件即可运行）
3. 安装到 `/opt/airgate-core/airgate-core`，sha256 校验
4. 创建系统用户 `airgate` 与目录 `/etc/airgate-core` / `/var/lib/airgate-core`
5. 安装 systemd 服务 `airgate-core.service`

脚本**不会**自动启动服务，也**不会**写 `config.yaml` —— 是为了让你审查一遍后再启动：

```bash
sudo systemctl start airgate-core
sudo systemctl enable airgate-core

# 然后浏览器访问 http://<your-host>:9517，向导会引导你输入：
#   - PostgreSQL 连接（已运行的 pg 实例）
#   - Redis 连接（已运行的 redis 实例）
#   - 管理员账号
# 所有信息会写入 /etc/airgate-core/config.yaml
```

进入管理后台后到 **插件管理 → 插件市场** 按需安装 gateway-openai / gateway-claude / gateway-kiro / airgate-playground / airgate-studio / payment-epay / airgate-health 等插件（`/var/lib/airgate-core/plugins` 持久化）。

**升级 / 卸载**：

```bash
# 升级到最新版本（保留配置和数据）
curl -sSL https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/install.sh | sudo bash -s -- upgrade

# 安装指定版本
curl -sSL https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/install.sh | sudo bash -s -- -v v0.1.0

# 卸载（默认保留 /etc/airgate-core 与 /var/lib/airgate-core）
curl -sSL https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/install.sh | sudo bash -s -- uninstall -y
```

**常用命令**：

```bash
sudo systemctl status airgate-core    # 状态
sudo journalctl -u airgate-core -f    # 日志
sudo systemctl restart airgate-core   # 重启
```

### 方式 1B：Docker Compose（自带 PostgreSQL + Redis）

```bash
mkdir airgate && cd airgate
curl -sSL https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/docker-deploy.sh | bash

# 检查生成的文件后启动
docker compose up -d
docker compose logs -f core
```

[docker-deploy.sh](deploy/docker-deploy.sh) 只准备文件 —— 不会替你 `up -d`，方便你审查后再启动：

1. 检查 docker / docker compose 依赖
2. 在当前目录创建 `data/{postgres,redis,plugins,uploads}` 子目录
3. 下载 `docker-compose.yml`
4. 用 `openssl rand` 生成 `DB_PASSWORD` / `REDIS_PASSWORD` / `JWT_SECRET` 写入 `.env`（权限 600）

启动后访问 `http://<your-host>:9517`，安装向导**自动跳过 DB / Redis 配置**（环境变量已就绪），只需要建管理员账号即可。

所有持久化数据落在 `./data/`，备份直接 `tar czf backup.tgz data .env` 即可。

**升级 / 卸载**：

```bash
# 升级到最新版本（在 docker-compose.yml 所在目录执行）
# 默认 AIRGATE_IMAGE_TAG=latest，直接拉新镜像即可；如固定了版本，先在 .env 改 AIRGATE_IMAGE_TAG
docker compose pull
docker compose up -d
docker compose logs -f core

# 升级到指定版本
sed -i 's/^AIRGATE_IMAGE_TAG=.*/AIRGATE_IMAGE_TAG=v0.1.0/' .env
docker compose pull && docker compose up -d

# 卸载（保留数据）：停止并删除容器，./data 与 .env 不动
docker compose down

# 彻底卸载（连同数据一起删，操作前务必备份）
docker compose down
rm -rf data .env docker-compose.yml
```

> 升级前建议先 `tar czf backup-$(date +%F).tgz data .env` 备份一次。Core 启动时会自动执行数据库 migrate，无需手动操作。

**关键环境变量**（完整列表见 [.env.example](deploy/.env.example)）：

| 变量 | 说明 | 是否必填 |
|---|---|---|
| `DB_PASSWORD` | Postgres 密码，首次启动后请勿修改 | ✅ |
| `REDIS_PASSWORD` | Redis 鉴权密码，建议 `openssl rand -hex 24`；不会持久化，可随时改后重启 | ✅ |
| `JWT_SECRET` | JWT 签名密钥，建议 `openssl rand -hex 32` | ✅ |
| `BIND_HOST` | 监听地址，反向代理后部署时改 `127.0.0.1` | ❌ |
| `PORT` | 对外端口，默认 9517 | ❌ |
| `TZ` | 时区，默认 `Asia/Shanghai` | ❌ |
| `AIRGATE_IMAGE_TAG` | 镜像版本，默认 `latest`，可固定到 `v0.x.y` | ❌ |
| `API_KEY_SECRET` | 用户 API Key 加密密钥，hex 编码 ≥64 字符 | ❌ |

### 反向代理：Caddy + 自动 HTTPS（可选）

如果想让 core 走 `https://your-domain` 而不是裸 `http://host:9517`，最省事的方案是用 [Caddy](https://caddyserver.com/)：自带 Let's Encrypt 自动签发与续期，配置只有十几行。下面以 Ubuntu / Debian 为例，1A / 1B 部署都适用。

**前置条件**

1. 域名 A 记录已经指向本机公网 IP；
2. 防火墙 / 安全组放行 **80** 和 **443**（HTTP-01 验证 + HTTPS）；
3. 9517 端口可以保留对外，也可以只允许本机访问 —— 由 Caddy 统一在 443 接收外部流量。

**安装 Caddy**

```bash
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
  | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
  | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update && sudo apt install -y caddy
```

其它系统参考 [官方安装文档](https://caddyserver.com/docs/install)。安装完 Caddy 会以 systemd 服务跑起来，配置文件位于 `/etc/caddy/Caddyfile`。

**配置 `/etc/caddy/Caddyfile`**

把下面内容覆盖进去，改掉域名和邮箱即可：

```caddyfile
airgate.example.com {
    encode zstd gzip

    reverse_proxy 127.0.0.1:9517 {
        # 关闭响应缓冲，保证 SSE / 流式输出实时返回
        flush_interval -1

        header_up Host                {host}
        header_up X-Real-IP           {remote_host}
        header_up X-Forwarded-For     {remote_host}
        header_up X-Forwarded-Proto   {scheme}

        # 大模型请求耗时较长，放宽超时
        transport http {
            read_timeout  30m
            write_timeout 30m
            dial_timeout  10s
        }
    }

    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Content-Type-Options    "nosniff"
        Referrer-Policy           "strict-origin-when-cross-origin"
        -Server
    }
}
```

如果 core 跑在 docker compose 里、Caddy 跑在宿主机，`127.0.0.1:9517` 保持原样即可（compose 默认已经把 9517 映射到宿主机）。

**应用 / 验证**

```bash
sudo caddy fmt --overwrite /etc/caddy/Caddyfile   # 格式化（可选）
sudo systemctl reload caddy                       # 热加载，不会断连
sudo journalctl -u caddy -f                       # 看证书签发日志
```

第一次 reload 后 Caddy 会自动向 Let's Encrypt 申请证书，几秒到几十秒后日志里出现 `certificate obtained successfully`，浏览器访问 `https://airgate.example.com` 即可。证书自动续期，无需人工干预。

**几个常见坑**

- **`flush_interval -1` 不能省**：默认会缓冲响应，SSE / 流式接口会变成"一次性返回"。
- **超时一定要放宽**：大模型推理动辄几分钟，Caddy 默认反代超时不够。
- **80 端口必须开**：Let's Encrypt 用 HTTP-01 验证，80 不通就签不到证书。调试期可在文件最上面加一段 `{ acme_ca https://acme-staging-v02.api.letsencrypt.org/directory }` 切到 staging，避开正式环境的速率限制。
- **想关掉 9517 直连**：把 [deploy/docker-compose.yml](deploy/docker-compose.yml) 里 `core.ports` 改成 `127.0.0.1:9517:9517`，外网就只能从 Caddy 进来；裸金属部署同理，在 `config.yaml` 里把监听地址改成 `127.0.0.1`。

### 方式 2：源码开发

适合二次开发或贡献者。两条路任选其一：

**A. 全容器（推荐，宿主机零依赖）**

宿主机只需要 Docker。父目录同时克隆 [`airgate-sdk`](https://github.com/DouDOU-start/airgate-sdk) 与 [`airgate-core`](https://github.com/DouDOU-start/airgate-core)：

```bash
mkdir airgate && cd airgate
git clone https://github.com/DouDOU-start/airgate-sdk.git
git clone https://github.com/DouDOU-start/airgate-core.git

cd airgate-core
docker compose -f deploy/docker-compose.dev.yml up
```

[deploy/docker-compose.dev.yml](deploy/docker-compose.dev.yml) 会拉起 postgres + redis，构建 sdk / core 前端，最后用 `go run ./cmd/server` 启动 core，全部跑在容器里。访问 `http://localhost:9517` 即可。

**B. 宿主机直跑**

需要 Go 1.25+、Node 22+、本地 Postgres + Redis，以及兄弟目录 [`airgate-sdk`](https://github.com/DouDOU-start/airgate-sdk)：

```bash
git clone https://github.com/DouDOU-start/airgate-sdk.git
git clone https://github.com/DouDOU-start/airgate-core.git
cd airgate-core

make install   # 安装前后端依赖
make dev       # 启动前后端开发服务器
```

更多命令见 `make help`。

> ⚠️ **不要使用 dev compose 上生产**。它用 `go run` 启动、源码 bind-mount 进容器、密码全部硬编码（`airgate` / `airgate-dev`），仅供本地开发。生产请走方式 1A 或 1B。

## 🏗 架构

```text
                     ┌──────────────────────────────────────────┐
                     │         AirGate Core (本仓库)            │
                     │  ┌─────────┐  ┌─────────┐  ┌──────────┐  │
   用户/管理员  ────► │  │  HTTP   │  │  调度   │  │   计费   │  │
                     │  │  路由   │  │  限流   │  │  订阅    │  │
                     │  └────┬────┘  └────┬────┘  └────┬─────┘  │
                     │       │  Plugin Manager (gRPC)  │        │
                     │       └────────────┬─────────────┘       │
                     └────────────────────┼─────────────────────┘
                                          │ go-plugin
                          ┌───────────────┼───────────────┐
                          ▼               ▼               ▼
                   ┌──────────────┐┌──────────────┐┌──────────────┐
                   │ gateway-     ││ gateway-     ││ payment-     │
                   │ openai       ││ claude       ││ epay         │
                   │ (子进程)     ││ (子进程)     ││ (子进程)     │
                   └──────┬───────┘└──────┬───────┘└──────────────┘
                          │ HTTPS         │ HTTPS
                          ▼               ▼
                     OpenAI / ChatGPT   Anthropic
```

**请求生命周期**：

```text
用户请求 ──► Core 鉴权 ──► Core 选账号 ──► Plugin.Forward() ──► 上游 AI API
                                              │
                                              ▼
                                         ForwardResult
                                       ┌──────┴──────┐
                                  token 用量      账号状态反馈
                                  Core 计费       Core 更新账号
```

## 📁 项目结构

```text
airgate-core/
├── backend/                  # Go 后端
│   ├── cmd/server/           # 入口
│   ├── internal/
│   │   ├── server/           # HTTP 路由 + 中间件
│   │   ├── plugin/           # 插件生命周期 + 市场 + 转发
│   │   ├── scheduler/        # 账号调度
│   │   ├── billing/          # 计费与用量
│   │   ├── ratelimit/        # 限流
│   │   └── app/              # 业务用例（按领域拆分）
│   └── ent/                  # 数据库 ORM (Ent)
├── web/                      # 管理后台 (React + Vite)
│   └── src/
│       ├── pages/admin/      # 管理页面
│       ├── shared/api/       # API 客户端
│       └── i18n/             # zh / en 文案
├── deploy/                       # 部署
│   ├── install.sh                # 裸金属安装脚本（systemd，curl | sudo bash）
│   ├── docker-deploy.sh          # docker compose 部署准备脚本（curl | bash）
│   ├── airgate-core.service      # systemd unit
│   ├── docker-compose.yml        # 生产编排（拉取 ghcr.io 镜像）
│   ├── docker-compose.dev.yml    # 开发编排（源码挂载）
│   ├── Dockerfile                # 多阶段构建
│   ├── config.docker.yaml        # 镜像内置默认配置
│   └── .env.example              # docker 部署的环境变量模板
├── .github/workflows/
│   ├── ci.yml                    # PR 检查
│   └── release.yml               # tag 触发：多架构镜像 + 跨平台二进制
└── Makefile
```

## 🔧 运维要点

- **健康检查**：`GET /healthz` 公开端点，docker / k8s 直接用
- **二进制完全自包含**：前端 SPA 与翻译文件均通过 `//go:embed` 打进二进制，install.sh 部署只有一个文件，没有额外的静态资源目录需要管理
- **数据持久化**：
  - **裸金属（1A）**：`/var/lib/airgate-core/{plugins,uploads}` + `/etc/airgate-core/config.yaml`，PostgreSQL / Redis 由用户自行管理
  - **Docker（1B）**：所有数据落在 `./data/{postgres,redis,plugins,uploads}` 四个 bind mount，备份只需 `tar czf backup.tgz data .env`
- **升级**：
  - 裸金属：`curl -sSL .../install.sh | sudo bash -s -- upgrade`
  - Docker：改 `.env` 里的 `AIRGATE_IMAGE_TAG` → `docker compose pull && docker compose up -d`
- **数据库迁移**：Ent schema 变更通过 `make ent` 生成代码，core 启动时自动 migrate
- **插件升级**：管理后台插件市场点刷新 → 卸载旧版本 → 重新安装

> **Docker 存量用户从 named volume 迁移**：旧版 compose 使用 `postgres_data` / `redis_data` / `airgate_plugins` / `airgate_uploads` 四个命名 volume，新版改为 `./data/*` bind mount。迁移步骤：
> ```bash
> docker compose down
> mkdir -p data/postgres data/redis data/plugins data/uploads
> docker run --rm -v <project>_postgres_data:/from -v $(pwd)/data/postgres:/to alpine cp -a /from/. /to/
> docker run --rm -v <project>_redis_data:/from    -v $(pwd)/data/redis:/to    alpine cp -a /from/. /to/
> docker run --rm -v <project>_airgate_plugins:/from -v $(pwd)/data/plugins:/to alpine cp -a /from/. /to/
> docker run --rm -v <project>_airgate_uploads:/from -v $(pwd)/data/uploads:/to alpine cp -a /from/. /to/
> curl -O https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/docker-compose.yml
> docker compose up -d
> # 验证一切正常后再删除旧的命名 volume
> docker volume rm <project>_postgres_data <project>_redis_data <project>_airgate_plugins <project>_airgate_uploads
> ```
> `<project>` 是 docker compose 自动生成的项目前缀（默认是当前目录名），`docker volume ls` 可以查看实际名字。

## 🤝 贡献 / 反馈

- Bug / Feature: [Issues](https://github.com/DouDOU-start/airgate-core/issues)
- 插件开发文档: [airgate-sdk](https://github.com/DouDOU-start/airgate-sdk)
- 参考插件实现: [airgate-openai](https://github.com/DouDOU-start/airgate-openai)

## 📜 License

MIT
