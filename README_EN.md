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
| **gateway-claude** | gateway | Claude Messages API gateway: OAuth authorization, TLS fingerprinting, usage monitoring | [DouDOU-start/airgate-claude](https://github.com/DouDOU-start/airgate-claude) |
| **gateway-kiro** | gateway | Kiro (AWS CodeWhisperer) reverse proxy gateway compatible with Anthropic Messages API | [DouDOU-start/airgate-kiro](https://github.com/DouDOU-start/airgate-kiro) |
| **airgate-playground** | extension | AI chat plugin: web chat, multi-model switching, conversation management | [DouDOU-start/airgate-playground](https://github.com/DouDOU-start/airgate-playground) |
| **airgate-studio** | extension | Unified creation center for multimodal image, video, and audio generation | [DouDOU-start/airgate-studio](https://github.com/DouDOU-start/airgate-studio) |
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

Pick one. Both are production-ready.

| Path | Best for | You provide |
|---|---|---|
| **1A. Bare-metal install.sh** | You already run PostgreSQL + Redis, want the leanest setup, prefer systemd | PostgreSQL 15+ / Redis 7+ |
| **1B. Docker Compose** | A clean server, want pg + redis + core all containerized | Docker only |

> ⚠️ Pick one — do NOT mix. Running both 1A and 1B gives you two database instances fighting each other.

### Method 1A: Bare-metal install (systemd; bring your own PostgreSQL + Redis)

```bash
curl -sSL https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/install.sh | sudo bash
```

[install.sh](deploy/install.sh) will:

1. Detect OS / arch (linux/amd64 or linux/arm64)
2. Download `airgate-core-{os}-{arch}` from the latest GitHub Release (the frontend SPA and translation files are `//go:embed`-ed into the binary — single file, ready to run)
3. Install to `/opt/airgate-core/airgate-core` with sha256 verification
4. Create system user `airgate` and directories `/etc/airgate-core` / `/var/lib/airgate-core`
5. Install systemd unit `airgate-core.service`

The script does **not** start the service nor write `config.yaml` — that's deliberate so you can review first:

```bash
sudo systemctl start airgate-core
sudo systemctl enable airgate-core

# Then visit http://<your-host>:9517 — the wizard will ask for:
#   - PostgreSQL connection (your existing instance)
#   - Redis connection (your existing instance)
#   - Admin account
# Final config gets written to /etc/airgate-core/config.yaml
```

After the admin UI is up, go to **Plugin Management → Marketplace** to install gateway-openai / gateway-claude / gateway-kiro / airgate-playground / airgate-studio / payment-epay / airgate-health on demand (`/var/lib/airgate-core/plugins` is the persistent location).

**Upgrade / uninstall**:

```bash
# Upgrade to latest (config and data preserved)
curl -sSL https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/install.sh | sudo bash -s -- upgrade

# Pin a specific version
curl -sSL https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/install.sh | sudo bash -s -- -v v0.1.0

# Uninstall (keeps /etc/airgate-core and /var/lib/airgate-core by default)
curl -sSL https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/install.sh | sudo bash -s -- uninstall -y
```

**Common commands**:

```bash
sudo systemctl status airgate-core    # status
sudo journalctl -u airgate-core -f    # logs
sudo systemctl restart airgate-core   # restart
```

### Method 1B: Docker Compose (bundles PostgreSQL + Redis)

```bash
mkdir airgate && cd airgate
curl -sSL https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/docker-deploy.sh | bash

# Review the generated files, then start
docker compose up -d
docker compose logs -f core
```

[docker-deploy.sh](deploy/docker-deploy.sh) only prepares files — it does NOT run `up -d` for you, so you can audit first:

1. Verify `docker` / `docker compose` are installed
2. Create `data/{postgres,redis,plugins,uploads}` under the current directory
3. Download `docker-compose.yml`
4. Generate `DB_PASSWORD` / `REDIS_PASSWORD` / `JWT_SECRET` via `openssl rand` and write `.env` (mode 600)

After you `up -d`, visit `http://<your-host>:9517`. The install wizard will **automatically skip the DB / Redis steps** (env vars are already set) and only ask you to create the admin account.

All persistent data lives under `./data/`, so backup is just `tar czf backup.tgz data .env`.

**Upgrade / uninstall**:

```bash
# Upgrade to latest (run in the directory that holds docker-compose.yml)
# Default AIRGATE_IMAGE_TAG=latest, so just pull the new image; if you pinned a version, edit AIRGATE_IMAGE_TAG in .env first
docker compose pull
docker compose up -d
docker compose logs -f core

# Upgrade to a specific version
sed -i 's/^AIRGATE_IMAGE_TAG=.*/AIRGATE_IMAGE_TAG=v0.1.0/' .env
docker compose pull && docker compose up -d

# Uninstall (keep data): stop and remove containers; ./data and .env are untouched
docker compose down

# Full uninstall (also wipes data — back up first!)
docker compose down
rm -rf data .env docker-compose.yml
```

> Recommended: snapshot before upgrading with `tar czf backup-$(date +%F).tgz data .env`. Core runs database migrations automatically on startup, no manual step required.

**Key environment variables** (full list in [.env.example](deploy/.env.example)):

| Variable | Description | Required |
|---|---|---|
| `DB_PASSWORD` | Postgres password — do not change after first boot | ✅ |
| `REDIS_PASSWORD` | Redis auth password, recommended `openssl rand -hex 24`; not persisted, can be rotated by restart | ✅ |
| `JWT_SECRET` | JWT signing key, recommended `openssl rand -hex 32` | ✅ |
| `BIND_HOST` | Bind address; set `127.0.0.1` when behind a reverse proxy | ❌ |
| `PORT` | External port, default 9517 | ❌ |
| `TZ` | Timezone, default `Asia/Shanghai` | ❌ |
| `AIRGATE_IMAGE_TAG` | Image tag, default `latest`, can pin to `v0.x.y` | ❌ |
| `API_KEY_SECRET` | User API Key encryption key, hex-encoded ≥64 chars | ❌ |

### Reverse Proxy: Caddy + Automatic HTTPS (Optional)

If you want to expose core via `https://your-domain` instead of plain `http://host:9517`, the simplest option is [Caddy](https://caddyserver.com/) — it ships with automatic Let's Encrypt issuance and renewal, and the config is only a dozen lines. The example below targets Ubuntu / Debian and works for both Method 1A and 1B.

**Prerequisites**

1. The domain's A record points to this machine's public IP;
2. Firewall / security group allows **80** and **443** (HTTP-01 challenge + HTTPS);
3. Port 9517 may stay open or be restricted to localhost — Caddy will be the public entrypoint on 443.

**Install Caddy**

```bash
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
  | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
  | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update && sudo apt install -y caddy
```

For other systems see the [official install docs](https://caddyserver.com/docs/install). After installation Caddy runs as a systemd service and reads `/etc/caddy/Caddyfile`.

**Configure `/etc/caddy/Caddyfile`**

Replace the file contents with the following, then change the domain and email:

```caddyfile
airgate.example.com {
    encode zstd gzip

    reverse_proxy 127.0.0.1:9517 {
        # Disable response buffering so SSE / streaming responses arrive in real time
        flush_interval -1

        header_up Host                {host}
        header_up X-Real-IP           {remote_host}
        header_up X-Forwarded-For     {remote_host}
        header_up X-Forwarded-Proto   {scheme}

        # LLM requests can be slow — relax the timeouts
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

If core runs in docker compose and Caddy runs on the host, leave `127.0.0.1:9517` as is — compose already publishes 9517 on the host.

**Apply / verify**

```bash
sudo caddy fmt --overwrite /etc/caddy/Caddyfile   # format (optional)
sudo systemctl reload caddy                       # hot reload, no downtime
sudo journalctl -u caddy -f                       # watch certificate issuance
```

On the first reload Caddy contacts Let's Encrypt automatically; within a few seconds to a minute the log shows `certificate obtained successfully`, and `https://airgate.example.com` becomes reachable. Renewal is fully automatic.

**Common gotchas**

- **Don't drop `flush_interval -1`** — without it Caddy buffers the response and SSE / streaming endpoints turn into "all-at-once" replies.
- **Bump the timeouts** — large-model inference can take minutes; Caddy's default reverse-proxy timeouts are too short.
- **Port 80 must be open** — Let's Encrypt uses HTTP-01 to validate; if 80 is blocked, no certificate. While debugging, add a `{ acme_ca https://acme-staging-v02.api.letsencrypt.org/directory }` global block at the top of the file to switch to staging and avoid the production rate limits.
- **To close direct access on 9517** — change `core.ports` in [deploy/docker-compose.yml](deploy/docker-compose.yml) to `127.0.0.1:9517:9517` so only Caddy can reach it from outside; for the bare-metal install, set the listen address to `127.0.0.1` in `config.yaml`.

### Method 2: Run from Source (Development)

For development or contributions. Pick one of the two paths:

**A. Fully containerized (recommended, zero host dependencies)**

The host only needs Docker. Clone [`airgate-sdk`](https://github.com/DouDOU-start/airgate-sdk) and [`airgate-core`](https://github.com/DouDOU-start/airgate-core) into a shared parent directory:

```bash
mkdir airgate && cd airgate
git clone https://github.com/DouDOU-start/airgate-sdk.git
git clone https://github.com/DouDOU-start/airgate-core.git

cd airgate-core
docker compose -f deploy/docker-compose.dev.yml up
```

[deploy/docker-compose.dev.yml](deploy/docker-compose.dev.yml) brings up postgres + redis, builds the sdk / core frontends, and runs core via `go run ./cmd/server` — all inside containers. Visit `http://localhost:9517` once it is up.

**B. Run on the host directly**

Requires Go 1.25+, Node 22+, local Postgres + Redis, and the sibling [`airgate-sdk`](https://github.com/DouDOU-start/airgate-sdk) repo:

```bash
git clone https://github.com/DouDOU-start/airgate-sdk.git
git clone https://github.com/DouDOU-start/airgate-core.git
cd airgate-core

make install   # Install backend & frontend dependencies
make dev       # Start dev servers
```

See `make help` for more commands.

> ⚠️ **Do NOT use the dev compose for production.** It runs `go run`, bind-mounts host source, and hardcodes weak passwords (`airgate` / `airgate-dev`). It is for local development only. For production use Method 1A or 1B.

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
├── deploy/                       # Deployment
│   ├── install.sh                # Bare-metal installer (systemd; curl | sudo bash)
│   ├── docker-deploy.sh          # Docker compose helper (curl | bash)
│   ├── airgate-core.service      # systemd unit
│   ├── docker-compose.yml        # Production compose (pulls ghcr.io image)
│   ├── docker-compose.dev.yml    # Development compose (source mount)
│   ├── Dockerfile                # Multi-stage build
│   ├── config.docker.yaml        # Image-baked default config
│   └── .env.example              # Environment template (for docker deploy)
├── .github/workflows/
│   ├── ci.yml                    # PR checks
│   └── release.yml               # Tag-triggered: multi-arch image + native binaries
└── Makefile
```

## 🔧 Operations

- **Health check**: `GET /healthz` public endpoint, ready for docker / k8s
- **Self-contained binary**: Frontend SPA and translation files are `//go:embed`-ed into the binary. Bare-metal installs are a single file with no extra static asset directories to manage.
- **Persistence**:
  - **Bare-metal (1A)**: `/var/lib/airgate-core/{plugins,uploads}` + `/etc/airgate-core/config.yaml`. PostgreSQL / Redis are managed by you.
  - **Docker (1B)**: All data lives in `./data/{postgres,redis,plugins,uploads}` (bind mounts). Backup is `tar czf backup.tgz data .env`.
- **Upgrade**:
  - Bare-metal: `curl -sSL .../install.sh | sudo bash -s -- upgrade`
  - Docker: edit `AIRGATE_IMAGE_TAG` in `.env` → `docker compose pull && docker compose up -d`
- **DB migrations**: Ent schema changes regenerate code via `make ent`; core auto-migrates on startup
- **Plugin upgrade**: Marketplace → click refresh → uninstall old version → reinstall

> **Migrating existing Docker named-volume deployments**: Older compose files used named volumes `postgres_data` / `redis_data` / `airgate_plugins` / `airgate_uploads`. The new compose uses `./data/*` bind mounts. To migrate:
> ```bash
> docker compose down
> mkdir -p data/postgres data/redis data/plugins data/uploads
> docker run --rm -v <project>_postgres_data:/from -v $(pwd)/data/postgres:/to alpine cp -a /from/. /to/
> docker run --rm -v <project>_redis_data:/from    -v $(pwd)/data/redis:/to    alpine cp -a /from/. /to/
> docker run --rm -v <project>_airgate_plugins:/from -v $(pwd)/data/plugins:/to alpine cp -a /from/. /to/
> docker run --rm -v <project>_airgate_uploads:/from -v $(pwd)/data/uploads:/to alpine cp -a /from/. /to/
> curl -O https://raw.githubusercontent.com/DouDOU-start/airgate-core/master/deploy/docker-compose.yml
> docker compose up -d
> # After verifying everything works, drop the old named volumes
> docker volume rm <project>_postgres_data <project>_redis_data <project>_airgate_plugins <project>_airgate_uploads
> ```
> `<project>` is the docker compose project prefix (defaults to the current directory name); `docker volume ls` shows the actual names.

## 🤝 Contributing / Feedback

- Bugs / Features: [Issues](https://github.com/DouDOU-start/airgate-core/issues)
- Plugin development docs: [airgate-sdk](https://github.com/DouDOU-start/airgate-sdk)
- Reference plugin implementation: [airgate-openai](https://github.com/DouDOU-start/airgate-openai)

## 📜 License

MIT
