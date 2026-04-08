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

### 方式 1：Docker Compose（推荐）

适合所有自部署用户，**无需 clone 仓库**：

```bash
mkdir airgate && cd airgate

# 下载部署文件
curl -O https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/docker-compose.yml
curl -O https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/.env.example
mv .env.example .env

# 改三个必填项：DB_PASSWORD / REDIS_PASSWORD / JWT_SECRET
vim .env

# 启动
docker compose up -d

# 查看日志
docker compose logs -f core
```

启动完成后访问 `http://<your-host>:9517`，按引导创建管理员账号。

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

### 方式 2：源码运行（开发）

适合二次开发或想完整在容器里跑全家桶的场景。两条路任选其一：

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

启动后是干净的 core，进入管理后台 → **插件管理 → 插件市场** 即可一键安装 gateway-openai / payment-epay / airgate-health（`data/plugins` 是持久 volume，装一次后续重启不会丢）。

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

### 方式 3：自建镜像

如果你想 fork 后用自己的镜像仓库：

```bash
# 构建上下文必须是包含 airgate-sdk 的父目录
docker build -f airgate-core/deploy/Dockerfile -t my-registry/airgate-core:dev ..

# 然后在 .env 里覆盖
echo "AIRGATE_IMAGE=my-registry/airgate-core" >> .env
echo "AIRGATE_IMAGE_TAG=dev" >> .env
docker compose up -d
```

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
├── deploy/                   # Docker 部署
│   ├── docker-compose.yml    # 生产编排（拉取 ghcr.io 镜像）
│   ├── docker-compose.dev.yml# 开发编排（源码挂载）
│   ├── Dockerfile            # 多阶段构建
│   ├── config.docker.yaml    # 镜像内置默认配置
│   └── .env.example          # 环境变量模板
├── .github/workflows/
│   ├── ci.yml                # PR 检查
│   └── release.yml           # tag 触发，buildx 多架构 push 到 ghcr.io
└── Makefile
```

## 🔧 运维要点

- **健康检查**：`GET /healthz` 公开端点，docker / k8s 直接用
- **数据持久化**：`postgres_data` / `redis_data` / `airgate_plugins` / `airgate_uploads` 四个命名 volume，重建容器不丢数据
- **升级**：改 `.env` 里的 `AIRGATE_IMAGE_TAG` → `docker compose pull && docker compose up -d`
- **数据库迁移**：Ent schema 变更通过 `make ent` 生成代码，启动时自动 migrate
- **插件升级**：管理后台插件市场点刷新 → 卸载旧版本 → 重新安装

## 🤝 贡献 / 反馈

- Bug / Feature: [Issues](https://github.com/DouDOU-start/airgate-core/issues)
- 插件开发文档: [airgate-sdk](https://github.com/DouDOU-start/airgate-sdk)
- 参考插件实现: [airgate-openai](https://github.com/DouDOU-start/airgate-openai)

## 📜 License

MIT
