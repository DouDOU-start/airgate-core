# AirGate Core

AirGate 的核心运行时引擎：统一管理、统一装配，插件负责具体平台能力。

## 项目概览

AirGate 是一个可扩展的 AI 网关平台，由以下仓库组成：

| 仓库 | 职责 |
| --- | --- |
| **`airgate-core`** | **运行时引擎：管理后台、账号调度、计费、插件生命周期管理** |
| `airgate-sdk` | 接口契约：插件接口、共享类型、gRPC 协议定义 |
| `airgate-openai` | 参考实现：OpenAI 兼容网关插件 |

> Core 负责所有通用平台能力，插件只需实现 SDK 定义的接口即可接入。

## 项目结构

```text
airgate-core/
├── backend/              # Go 后端
│   ├── cmd/server/       # 入口
│   ├── internal/         # 业务逻辑
│   │   ├── plugin/       # 插件生命周期管理
│   │   ├── router/       # 路由注册与网关转发
│   │   └── ...
│   └── ent/              # 数据库 ORM（Ent）
├── web/                  # 管理后台前端（Vue）
├── Makefile
└── .github/workflows/    # CI
```

## 快速开始

```bash
make install          # 安装前后端依赖
make dev              # 启动开发环境（前后端）
make build            # 构建前后端
make ci               # lint + test + build
```

更多命令见 `make help`。

## Core 职责

### 账号与调度

- 用户、分组、API Key 管理
- 账号增删改查与凭证存储
- 基于优先级、状态和负载的账号调度
- 限流、并发控制、计费

### 插件生命周期

- 插件进程管理（基于 hashicorp/go-plugin，gRPC 通信）
- 动态注册插件路由到 HTTP 网关
- 托管插件前端资源
- 插件市场：安装、升级、卸载

### 请求流程

```text
用户请求 → Core 鉴权 → Core 选账号 → 插件 Forward() → 上游 AI API
                                          ↓
                                    ForwardResult
                                   ┌──────┴──────┐
                              token 用量     账号状态反馈
                              Core 计费      Core 更新账号状态
```

### 插件接入流程

```text
启动插件进程（go-plugin）
  → Info()      获取元信息（ID、类型、账号格式、前端声明）
  → Platform()  获取业务平台键
  → Models()    获取模型列表（缓存，用于计费）
  → Routes()    获取路由声明（注册到 HTTP 网关）
  → GetWebAssets()  提取前端资源（如有）
```

Core 以插件运行时返回的元信息为准，**不依赖 `plugin.yaml` 做运行时决策**。

### 前端插件集成

1. 插件通过 `WebAssetsProvider` 提供前端静态资源
2. Core 挂载到 `/plugins/{name}/assets/*`
3. 管理后台根据 `FrontendPages` 注册路由、渲染导航
4. Core 页面预留插槽，根据 `FrontendWidgets` 动态加载插件组件

## 插件市场

管理员通过管理后台即可完成插件的浏览、安装、升级和卸载。

**插件源**需提供：`registry.json`、各版本下载地址、`sha256` 校验值。

**安装包**需包含：插件二进制、`plugin.yaml`、可选的 `web/` 前端资源。

### 安装流程

1. 从插件源下载并校验 `sha256`
2. 检查 `min_core_version`、依赖和 ID 冲突
3. 启动插件进程，注册路由和前端资源

### 升级流程

1. 下载新版本 → 停旧进程 → 替换文件 → 启动新版本
2. 失败时自动回滚旧版本

### 卸载流程

1. 调用插件 `Stop()` → 停止进程 → 移除路由和资源 → 删除目录和数据库记录

## 开发工具

```bash
make lint             # golangci-lint 代码检查
make fmt              # 代码格式化
make test             # 运行测试
make ent              # 生成 Ent ORM 代码
```

## 相关文档

- 插件开发指南、接口定义、SDK 类型说明：见 [airgate-sdk](https://github.com/DouDOU-start/airgate-sdk)
- OpenAI 插件实现参考：见 [airgate-openai](https://github.com/DouDOU-start/airgate-openai)

## License

MIT
