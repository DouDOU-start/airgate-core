# AirGate 现状架构(as-is)

> **现状文档** · 描述 AirGate **当前代码的实际实现**,为日常开发的权威依据。已知架构差距与改进方向见 [`tech-debt.md`](tech-debt.md)。
> **维护规则**:改动涉及本目录所述架构(分层/契约/转发/计费/调度)时,须同步更新对应文档——这是防漂移的核心机制。

## 这套文档是什么

`current/` 描述系统**实际是什么样**(对得上代码),`tech-debt.md` 登记已知差距与改进方向。开发新功能、评审改动,以本目录为标准。

| 文档 | 内容 |
|---|---|
| [`core-runtime.md`](core-runtime.md) | Core 后端真实实现:分层、转发管线、HostService、判决/计费、调度、路由 |
| [`plugin-contract.md`](plugin-contract.md) | core ↔ 插件真实契约:proto、Go 接口、Host.Invoke、ForwardOutcome、capability、manifest |
| [`plugins.md`](plugins.md) | 各插件现状:7 个插件的职责(含混合现状)、前端机制 |
| [`tech-debt.md`](tech-debt.md) | 技术债登记:协议/产品硬编码热点(位置/现状/目标方向) |

## 生态构成(真实)

可插拔 AI 网关运行时。monorepo,各子目录独立 Git 仓,经 `go.work` 协作。

| 组件 | 仓 | 类型 | 职责 |
|---|---|---|---|
| **Core** | `airgate-core` | 运行时 | 鉴权、账号调度、转发管线、计费、任务/资产、插件生命周期、后台 UI |
| **网关插件** | `airgate-openai` / `airgate-claude` / `airgate-kiro` | `gateway` | 转发请求至上游 AI 平台(当前还承担 provider 认证与账号 UI,见 plugins.md) |
| **扩展插件** | `airgate-playground` / `airgate-studio` / `airgate-epay` / `airgate-health` | `extension` | 聊天 UI / 内容创作 / 支付 / 健康监控 |
| **SDK** | `airgate-sdk` | 契约层 | gRPC 协议(ABI)、Go 插件接口、运行时桥、devkit、前端主题 |

Core 经 hashicorp/go-plugin 将插件作为独立 gRPC 子进程加载。

## 真实请求流

以网关转发为例(`internal/plugin/forwarder.go` 的 `Forward`):

```
客户端(API Key) → Core dynamic_router
  → checkBalance(余额预检)
  → [只读元信息快车道:/v1/models 等 → 插件本地合成,跳过账号/计费链路]
  → acquireClientQuota(客户端 RPM/并发)
  → failover 循环(最多 3 次,forwarder.go:59):
        pickAccount(scheduler 选号)
        → acquireAccountSlot(账号并发槽,排队最多 60s,forwarder.go:63)
        → Plugin.Forward()  ──gRPC──▶ 网关插件 → 上游 AI API
        ← ForwardOutcome(判决 Kind + Usage + 上游响应)
        → scheduler.Apply(按 Kind 更新账号状态/冷却)
        → 失败且可 failover 则换账号重试
  → billing(三管道计费) + recorder(写 usage_log)
  → 响应回客户端
```

插件反向调用 Core 能力经 `Host.Invoke`(单一 `CoreInvokeService`,见 plugin-contract.md),如 `tasks.create`、`assets.store`。

## 演进方式

已知的架构差距与改进方向登记于 [`tech-debt.md`](tech-debt.md);分阶段治理路线见 [`../boundary-refactor-plan.md`](../boundary-refactor-plan.md)(原目标愿景文档 `ecosystem-v2.md` 已归档至 `docs/architecture/archived/`)。

流程:先以本目录准确记录现状 → 决定标准如何改 → 改文档 → 改代码向新标准看齐。
