<div align="center">
  <img src="web/src/assets/logo.svg" alt="AirGate" width="120" />

  <h1>AirGate Core</h1>

  <p><strong>A pluggable runtime for unified AI gateways</strong></p>

  <p>
    <a href="https://github.com/DouDOU-start/airgate-core/releases"><img src="https://img.shields.io/github/v/release/DouDOU-start/airgate-core?style=flat-square" alt="release" /></a>
    <a href="https://github.com/DouDOU-start/airgate-core/pkgs/container/airgate-core"><img src="https://img.shields.io/badge/ghcr.io-airgate--core-blue?style=flat-square&logo=docker" alt="ghcr.io" /></a>
    <a href="https://github.com/DouDOU-start/airgate-core/blob/master/LICENSE"><img src="https://img.shields.io/github/license/DouDOU-start/airgate-core?style=flat-square" alt="license" /></a>
    <img src="https://img.shields.io/badge/Go-1.25-00ADD8?style=flat-square&logo=go" alt="go" />
    <img src="https://img.shields.io/badge/React-19-61DAFB?style=flat-square&logo=react" alt="react" />
  </p>

  <p>
    <a href="README.md">中文</a> · <strong>English</strong>
  </p>
</div>

---

AirGate is **not** another monolithic gateway that hard-codes a list of AI providers. It is an open architecture where **provider capabilities are shipped as plugins** and loaded by the runtime on demand.

- **Core** (this repo) = users, accounts, scheduling, billing, rate limiting, subscriptions, admin dashboard — everything provider-agnostic.
- **Plugin** = a standalone Go process that talks gRPC to Core and implements the SDK contract for a specific upstream.

Plugins can be **released, installed, uninstalled, and hot-reloaded independently**, with zero downtime to Core or other plugins. You only ship the capabilities you need, and writing a private plugin for an internal service is a first-class workflow.

## ✨ Highlights

- **🔌 Plugin runtime** — Provider capabilities run as gRPC subprocesses (powered by hashicorp/go-plugin). Install via marketplace, GitHub Release, binary upload, or dev hot-reload — all without restarting Core.
- **🧩 Dynamic route injection** — Routes declared by a plugin are auto-registered into the HTTP gateway. Account form fields and React components are auto-mounted into the admin dashboard.
- **🎯 Smart account scheduling** — Priority + health + concurrency limit drive automatic account selection, with degraded accounts auto-quarantined.
- **💰 Accurate billing** — Token × per-model price metering in real time, with rate multipliers, user balances, subscriptions, and quotas.
- **🛡 Complete admin dashboard** — Users, groups, accounts, subscriptions, IPs, proxy pool, plugin marketplace, and settings in one place. Account import/export, auto-refresh, and admin API key authentication included.
- **📦 One-command deploy** — Multi-arch images (amd64/arm64) on `ghcr.io`. End users only need `docker compose up -d`.

## 🧩 Plugin Ecosystem

### Released plugins

| Plugin | Type | Capabilities | Repository |
|---|---|---|---|
| **gateway-openai** | gateway | OpenAI Responses / Chat Completions / ChatGPT OAuth / Anthropic protocol translation / WebSocket | [DouDOU-start/airgate-openai](https://github.com/DouDOU-start/airgate-openai) |
| **payment-epay** | extension | Multi-channel payment: EPay (Xunhu/Rainbow) / Alipay Official / WeChat Pay Official, with recharge page, order management, provider configuration | [DouDOU-start/airgate-epay](https://github.com/DouDOU-start/airgate-epay) |
| **airgate-health** | extension | AI provider health monitoring: active probing, availability/latency aggregation, public status page | [DouDOU-start/airgate-health](https://github.com/DouDOU-start/airgate-health) |

### Installing a plugin

In the admin dashboard → **Plugin Management** → choose any of:

```text
1. Marketplace → click "Install"     (pulls latest GitHub Release matching your arch)
2. Upload → drop a binary file        (good for private plugins)
3. GitHub → enter owner/repo          (good for plugins not yet listed in marketplace)
```

The marketplace **periodically syncs** the latest release of each plugin via the GitHub API (every 6 hours by default, using ETag to avoid quota cost). You can also click the refresh button on the marketplace page to sync immediately.

### Building your own plugin

Pull in [airgate-sdk](https://github.com/DouDOU-start/airgate-sdk) and implement the `GatewayPlugin` interface:

```go
type GatewayPlugin interface {
    Info() PluginInfo                    // Metadata: ID, version, account fields, frontend components
    Platform() string                    // Platform key
    Models() []ModelInfo                 // Model list + pricing (used for billing)
    Routes() []RouteDefinition           // HTTP route declarations
    Forward(ctx, req) (*ForwardResult, error)  // Actual forwarding logic
}
```

See [airgate-openai](https://github.com/DouDOU-start/airgate-openai) for a complete reference, including Makefile, release workflow, and embedded frontend.

## 🛠 Tech Stack

| Layer | Tech |
|---|---|
| Backend | Go 1.25 · Gin · Ent ORM · PostgreSQL 17 · Redis 8 |
| Frontend | React 19 · Vite · TanStack Query · Tailwind CSS |
| Plugin protocol | hashicorp/go-plugin (gRPC) |
| Deployment | Docker Compose · GitHub Container Registry · multi-arch (amd64/arm64) |
| Auth | JWT + Admin API Key |

## 🚀 Deployment

### Method 1: Docker Compose (Recommended)

For all self-hosted users — **no need to clone the repo**:

```bash
mkdir airgate && cd airgate

# Download deployment files
curl -O https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/docker-compose.yml
curl -O https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/.env.example
mv .env.example .env

# Edit two required values: DB_PASSWORD and JWT_SECRET
vim .env

# Start
docker compose up -d

# Tail logs
docker compose logs -f core
```

Once started, visit `http://<your-host>:9517` and follow the wizard to create the admin account.

**Key environment variables** (full list in [.env.example](deploy/.env.example)):

| Variable | Description | Required |
|---|---|---|
| `DB_PASSWORD` | Postgres password — do not change after first boot | ✅ |
| `JWT_SECRET` | JWT signing key, recommended `openssl rand -hex 32` | ✅ |
| `BIND_HOST` | Bind address; set `127.0.0.1` when behind a reverse proxy | ❌ |
| `PORT` | External port, default 9517 | ❌ |
| `TZ` | Timezone, default `Asia/Shanghai` | ❌ |
| `AIRGATE_IMAGE_TAG` | Image tag, default `latest`, can pin to `v0.x.y` | ❌ |
| `API_KEY_SECRET` | User API Key encryption key, hex-encoded ≥64 chars | ❌ |

### Method 2: Run from Source (Development)

Requires Go 1.25+, Node 22+, local Postgres + Redis, and the sibling [`airgate-sdk`](https://github.com/DouDOU-start/airgate-sdk) repo:

```bash
git clone https://github.com/DouDOU-start/airgate-sdk.git
git clone https://github.com/DouDOU-start/airgate-core.git
cd airgate-core

make install   # Install backend & frontend dependencies
make dev       # Start dev servers
```

See `make help` for more commands.

### Method 3: Build Your Own Image

If you want to fork and host your own image:

```bash
# Build context must be the parent directory containing airgate-sdk
docker build -f airgate-core/deploy/Dockerfile -t my-registry/airgate-core:dev ..

# Then override in .env
echo "AIRGATE_IMAGE=my-registry/airgate-core" >> .env
echo "AIRGATE_IMAGE_TAG=dev" >> .env
docker compose up -d
```

## 🏗 Architecture

```text
                     ┌──────────────────────────────────────────┐
                     │         AirGate Core (this repo)         │
                     │  ┌─────────┐  ┌─────────┐  ┌──────────┐  │
   Users / Admin ──► │  │  HTTP   │  │ Sched.  │  │ Billing  │  │
                     │  │  Router │  │ + Limit │  │ + Subs   │  │
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
                   │ (subprocess) ││ (subprocess) ││ (subprocess) │
                   └──────┬───────┘└──────┬───────┘└──────────────┘
                          │ HTTPS         │ HTTPS
                          ▼               ▼
                     OpenAI / ChatGPT   Anthropic
```

**Request lifecycle:**

```text
User request ──► Core auth ──► Core picks account ──► Plugin.Forward() ──► Upstream AI API
                                                          │
                                                          ▼
                                                    ForwardResult
                                                  ┌──────┴──────┐
                                              Token usage   Account status
                                              Core bills    Core updates account
```

## 📁 Project Structure

```text
airgate-core/
├── backend/                  # Go backend
│   ├── cmd/server/           # Entry point
│   ├── internal/
│   │   ├── server/           # HTTP routes & middleware
│   │   ├── plugin/           # Plugin lifecycle + marketplace + forwarder
│   │   ├── scheduler/        # Account scheduling
│   │   ├── billing/          # Billing & usage
│   │   ├── ratelimit/        # Rate limiting
│   │   └── app/              # Domain use cases
│   └── ent/                  # Database ORM (Ent)
├── web/                      # Admin dashboard (React + Vite)
│   └── src/
│       ├── pages/admin/      # Admin pages
│       ├── shared/api/       # API client
│       └── i18n/             # zh / en strings
├── deploy/                   # Docker deployment
│   ├── docker-compose.yml    # Production (pulls ghcr.io image)
│   ├── docker-compose.dev.yml# Development (source mount)
│   ├── Dockerfile            # Multi-stage build
│   ├── config.docker.yaml    # Image-baked default config
│   └── .env.example          # Environment template
├── .github/workflows/
│   ├── ci.yml                # PR checks
│   └── release.yml           # Tag-triggered buildx multi-arch push to ghcr.io
└── Makefile
```

## 🔧 Operations

- **Health check**: `GET /healthz` public endpoint, ready for docker / k8s
- **Persistence**: Four named volumes — `postgres_data` / `redis_data` / `airgate_plugins` / `airgate_uploads` — survive container recreation
- **Upgrade**: Edit `AIRGATE_IMAGE_TAG` in `.env` → `docker compose pull && docker compose up -d`
- **DB migrations**: Ent schema changes regenerate code via `make ent`; auto-migrate on startup
- **Plugin upgrade**: Marketplace → click refresh → uninstall old version → reinstall

## 🤝 Contributing / Feedback

- Bugs / Features: [Issues](https://github.com/DouDOU-start/airgate-core/issues)
- Plugin development docs: [airgate-sdk](https://github.com/DouDOU-start/airgate-sdk)
- Reference plugin implementation: [airgate-openai](https://github.com/DouDOU-start/airgate-openai)

## 📜 License

MIT
