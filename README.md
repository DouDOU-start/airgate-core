<div align="center">
  <img src="web/src/assets/logo.svg" alt="AirGate" width="120" />

  <h1>AirGate Core</h1>

  <p><strong>轻量单体 AI 网关：多渠道调度 · 原生协议透传 · 价目表计费</strong></p>

  <p>
    <a href="https://github.com/DouDOU-start/airgate-core/releases"><img src="https://img.shields.io/github/v/release/DouDOU-start/airgate-core?style=flat-square" alt="release" /></a>
    <a href="https://github.com/DouDOU-start/airgate-core/pkgs/container/airgate-core"><img src="https://img.shields.io/badge/ghcr.io-airgate--core-blue?style=flat-square&logo=docker" alt="ghcr.io" /></a>
    <a href="https://github.com/DouDOU-start/airgate-core/blob/master/LICENSE"><img src="https://img.shields.io/github/license/DouDOU-start/airgate-core?style=flat-square" alt="license" /></a>
    <img src="https://img.shields.io/badge/Go-1.25-00ADD8?style=flat-square&logo=go" alt="go" />
    <img src="https://img.shields.io/badge/React-19-61DAFB?style=flat-square&logo=react" alt="react" />
  </p>
</div>

---

AirGate Core 是一个**自包含的单体 AI 网关**：管理员配置**渠道**（协议类型 + base_url + 上游 api_keys + 模型列表），用户拿 `sk-` 密钥按原生协议调用对应端点，网关**零翻译纯透传**直发上游，并按**模型价目表**精确计费。

- **单二进制部署**：前端 SPA、翻译文件全部 `go:embed` 内嵌，外部依赖只有 PostgreSQL 17 与 Redis 8
- **零翻译**：请求/响应体原样透传，不做任何跨协议转换——上游行为即最终行为
- **多渠道调度**：同协议渠道按优先级分档 + 权重随机 + 多 key 轮询，失败自动 failover（≤3 次）
- **自动养护**：429/5xx 自动换渠道、401/403 自动禁用，全程留痕可排障

## 支持的入站协议

入站端点按协议分树，只路由到同协议渠道：

| 协议 | 端点 | 匹配渠道类型 |
|---|---|---|
| OpenAI | `POST /v1/chat/completions` · `/v1/responses` · `/v1/images/generations` · `/v1/images/edits` · `GET /v1/models` | `openai_compatible` / `custom` |
| Anthropic | `POST /v1/messages` · `/v1/messages/count_tokens` | `anthropic` |
| Gemini | `POST /v1beta/models/{model}:generateContent` 等 · `GET /v1beta/models` | `gemini` |
| OpenAI 视频（异步任务） | `POST /v1/videos` · `GET /v1/videos/{id}` · `GET /v1/videos/{id}/content` | `openai_video` |
| Suno 音乐（异步任务） | `POST /suno/submit/{music\|lyrics}` · `POST /suno/fetch` · `GET /suno/fetch/{id}` | `suno` |

SSE 流式全协议支持；`count_tokens` 两端点零计费。

异步任务（视频/音乐）为「提交-轮询」模型：提交时按估价**预扣**余额（按次价或
`pricing_extra.video.per_second` 按秒价 × 时长），后台每 10s 轮询上游刷新状态，
成功按实际用量**差额多退少补**并落用量日志，失败/超时（默认 30 分钟，
`task_timeout_minutes` 可调）**全额退款**。任务查询读本地快照，不穿透上游；
Suno 的计费模型名由动作合成（`suno_music` / `suno_lyrics`），渠道模型列表与价目表需配置这两个名字。

## 功能一览

- **渠道管理**：多渠道多 key、优先级/权重、渠道测试、余额刷新、上游模型拉取、批量操作、失败统计
- **计费**：模型价目表（token 单价 / 按次计价 / 视频按秒计价）+ 三管道计费（实扣 / 账面 / 渠道成本），倍率按「用户专属 > 用户等级 > 分组档位」优先级解析（等级 = 按等级×分组批量分层定价）；密钥可设最高计费倍率，实际倍率超限时预检拒绝请求（防调价后超预期消费）
- **异步任务**：视频（Sora 形态 `/v1/videos`）与 Suno 音乐的提交-轮询转发，预扣-结算-退款三段式计费
- **用户体系**：注册（邮箱验证码）/ 登录 / API Key 自助管理 / 余额预警 / 用量明细与趋势
- **分组**：渠道按分组隔离，用户按分组授权，倍率可按组覆盖
- **充值**：易支付等支付渠道、兑换码
- **OAuth 提供方**：标准 PKCE 授权码流程，供外部应用接入（`/oauth/token`、`/oauth/userinfo`、`/oauth/provision-key`）；支持 scope 约束的共享钱包查询、幂等扣款和关联退款（`/oauth/wallet/*`）
- **运维**：管理仪表盘、上游请求留痕、公告系统、后台一键自更新（systemd/Docker 感知）
- **限流**：用户/密钥/渠道三级并发闸门 + RPM 限速（Redis 原语）
- **风控中心**：转发前内容审核（关键词 Aho-Corasick 拦截 + 外部审核 API 多 key 轮询熔断 + 命中哈希缓存），observe/pre_block 双模式，采样率/分组/模型过滤，滑窗违规计数自动封禁（管理员豁免）+ 邮件通知，审核日志双保留期 TTL 清理
- **可插拔插件运行时**：基于 `hashicorp/go-plugin + gRPC` 的多实例独立进程；插件以类型和能力声明接入点，管理后台支持上传或 URL 安装、YAML 配置、独立启停、重载与卸载

## 快速开始

### Docker Compose（推荐）

```bash
mkdir airgate && cd airgate
curl -sSL https://raw.githubusercontent.com/DouDOU-start/airgate-core/standalone-gateway/deploy/docker-deploy.sh | bash
docker compose up -d
```

脚本会生成随机密钥写入 `.env`（权限 600），并准备好数据目录。数据库、Redis、上传文件、微信校验文件和已安装插件都会持久化到部署目录下的 `data/`；升级镜像或重建容器不会丢失。启动后访问 `http://<host>:9517` 注册账号——第一个注册的账号会自动成为系统管理员。

### 裸金属（systemd）

```bash
curl -sSL https://raw.githubusercontent.com/DouDOU-start/airgate-core/standalone-gateway/deploy/install.sh | sudo bash
sudo systemctl enable --now airgate-core
```

需要已运行的 PostgreSQL 15+ 与 Redis 7+。参考 `backend/config.yaml.example` 创建 `/etc/airgate-core/config.yaml`（填入 DB/Redis 连接与 `jwt.secret`，也可用环境变量提供），启动后访问 `http://<host>:9517` 注册的第一个账号即系统管理员。

### 源码开发

```bash
git clone -b standalone-gateway https://github.com/DouDOU-start/airgate-core.git
cd airgate-core
make install   # 安装全部依赖（Go modules + pnpm + air + 首次前端构建）
make dev       # 前后端热重载
```

## 配置

配置优先级：环境变量 > `config.yaml`（路径由 `--config` 或 `CONFIG_PATH` 指定，默认 `./config.yaml`）。

| 环境变量 | 说明 | 默认 |
|---|---|---|
| `DB_HOST` / `DB_PORT` / `DB_USER` / `DB_PASSWORD` / `DB_NAME` / `DB_SSLMODE` | PostgreSQL 连接 | — |
| `REDIS_HOST` / `REDIS_PORT` / `REDIS_PASSWORD` / `REDIS_DB` | Redis 连接 | — |
| `JWT_SECRET` | 登录 token 签名密钥 | — |
| `API_KEY_SECRET` | 渠道/用户密钥 AES-256-GCM 加密密钥，hex ≥64 字符；留空使用内置默认密钥（生产建议配置随机值） | 内置默认 |
| `PORT` / `HOST` | 监听端口 / 地址 | `9517` / `0.0.0.0` |
| `GIN_MODE` | `release` / `debug` | `debug` |
| `LOG_LEVEL` / `LOG_FORMAT` | 日志级别 / 格式（`text`/`json`） | `info` / `text` |
| `PLUGINS_ENABLED` | 没有 `runtime.yaml` 的手工安装插件的兼容默认开关 | `false` |
| `PLUGINS_DIR` / `PLUGINS_HOOK_TIMEOUT_MS` | 插件目录 / Relay Hook 执行链总超时毫秒数 | `data/plugins` / `500` |

配置文件不存在时，连接信息可完全由环境变量提供（docker compose 场景）。首次启动后注册的第一个账号会自动成为系统管理员。

## 请求链路

```
客户端（sk- 密钥） → 鉴权/余额预检 → 内容审核预检（风控中心，可选） → 用户/密钥并发闸门
  → 渠道调度（协议过滤 + 优先级分档 + 权重随机 + 多 key 轮询）
  → 透传直发上游（失败 failover ≤3：429/5xx 换渠道 / 401·403 自动禁用）
  → usage 计量归一化 → 价目表计费 → 异步落账 usage_log
```

## 项目结构

```
airgate-core/
├── backend/
│   ├── cmd/server/          # 入口
│   ├── ent/schema/          # 数据模型（唯一事实源，改后 make ent）
│   └── internal/
│       ├── relay/           # 转发子系统：registry 调度 / adaptor 透传 / pipeline 主循环 / task 异步任务 / errfmt 错误形态 / pricing 价目 / outcome 判定
│       ├── billing/         # 三管道计费与异步记账
│       ├── scheduler/       # Redis 限流原语（并发闸门 / RPM）
│       ├── app/             # 领域服务（user / channel / group / usage / payment / ...）
│       ├── server/          # HTTP 层（dto / handler / middleware / router）
│       ├── infra/store/     # 数据访问（唯一 import ent 的层）
│       ├── auth/            # API Key 鉴权与加密
│       ├── moderation/      # 风控判定核心（输入抽取 / 关键词 / 审核 API 熔断 / worker 池 / 封禁副作用）
│       ├── pluginruntime/   # 多实例插件协议、能力驱动与文件系统管理
│       └── errlog/          # 上游失败留痕
├── web/                     # React 19 + Vite + TanStack Query + Tailwind
└── deploy/                  # Dockerfile / compose / install.sh / systemd unit
```

## 从旧版本升级（重要）

本版本移除了全部启动期兼容/迁移代码，只保留最新干净 schema，**存量库升级须手动迁移**：

1. **表结构**：启动时仅做非破坏性建表补列（`Schema.Create`）。存量库既有外键的 `ON DELETE` 行为、历史列回填（`billed_cost`、`used_quota_actual`、`key_hint`、`*_snapshot`）不再自动迁移，如需保持删除用户/渠道后的历史留存语义，请手动对齐最新 `ent/schema`。
2. **支付配置**：旧插件时代的明文/无 `enc:v1` 前缀支付配置不再自动规范化，装载时会跳过并告警，需在管理后台重新保存一次。

全新部署无需关心以上内容。

## 构建与自检

```bash
make help          # 查看全部命令
make ci            # 提交前自检：lint + test + ent 漂移检查 + build
make ent           # 改 ent/schema 后重新生成
make docker-build  # 本地构建镜像
```

## 许可证

见 [LICENSE](LICENSE)。
