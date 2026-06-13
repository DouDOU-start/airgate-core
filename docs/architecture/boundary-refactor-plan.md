# 生态边界治理路线（规划）

> **本文为治理规划（尚未实施，不伴随代码改动）。** 现状以 [`current/`](current/) 为准，差距登记见 [`current/tech-debt.md`](current/tech-debt.md)，目标边界见根 `CLAUDE.md`「生态边界」职责速查表。
> 用途：把"代码边界如何理清甚至重构"的分析与排期依据集中一处，供决策；实施某阶段后须同步更新本文与 `current/` 对应文档。

## 一、诊断摘要（2026-06）

**已治理**（tech-debt 一类热点多数已收口）：错误格式、metadata_only 路由、模型家族、图片授权、图片模型判定均已改为**插件 Metadata/能力声明 + Core 默认兜底**，Core 不再新增协议特判。

**仍开放的边界违反**，按性质分三组：

| 组 | 问题 | 位置 | 性质 |
|---|---|---|---|
| A. Core 残留特判 | ~~已修复~~ Claude→GPT 调度映射改为路由级 `scheduling_model_map` 声明，硬编码留作兜底 | `internal/plugin/scheduling_model.go` | 局部热点 |
| A | ~~已修复~~ `/status` 反代目标改为 config `plugins.status_plugin` | `internal/server/router.go` | 局部热点（设计上有意，仅耦合插件名） |
| A | ~~已修复~~ SDK 已补 `TaskStatusRetrying`/`TaskStatusCancelling`，与 Core 7 态对齐 | `sdkgo/task.go` | 契约缺口 |
| B. 通道过宽 | HostService 单一 Invoke 通道暴露 19 个 method，授权为扁平 method 级 | `internal/plugin/host_service.go` | 结构性 |
| B | Manifest 无 `requires.host`/`provides`，capability 只在 Go 代码声明 | manifest 生成链 | 结构性 |
| C. 插件职责混合 | 三个网关插件混合 gateway + provider（OAuth/session/TLS/EventStream）+ 账号 UI | openai/claude/kiro 各仓 | 架构级 |
| C | Playground 兼做协议转发/SSE 解析/任务编排 | `airgate-playground` | 架构级 |
| C | Gateway `Forward` 直接收发原始协议体，无规范化操作层 | 契约层 | 架构级（最深） |

## 二、排序原则

1. **先止血**：所有新代码按职责归位、勿加深（已由根/各仓 `CLAUDE.md` 红线约束）——零成本，持续生效。
2. **低风险先行**：A 组是局部改动，不破坏 ABI，可随平时迭代清掉。
3. **通道先于拆分**：B 组（HostService 分组 + Manifest v2）是 C 组拆分的前置——没有按能力分组的授权与声明，拆出来的 provider/UI 插件无法精确授权。
4. **破坏性最后**：C 组涉及仓库拆分与 ABI 演进，等 B 组契约稳定后做，避免拆两次。

## 三、治理阶段

### 阶段 1：热点收尾（✅ 已完成，2026-06-12）

- `scheduling_model.go`：实施时调整方案——精确 ID 的 `scheduling_model` 覆盖不了协议翻译入口的前缀匹配（claude-\* 不在 openai 插件目录中），故新增**路由级** `Metadata["scheduling_model_map"]` 约定（JSON 前缀映射表，最长前缀优先），openai 插件在 4 条 Anthropic 翻译路由声明（env 覆盖由插件读取，变量名不变）；Core 硬编码映射保留为未声明插件的兜底，勿再扩展。
- `/status` 插件名：改为 config `plugins.status_plugin`（默认 `airgate-health`），Core 不再绑定具体插件名。
- SDK 已补 `TaskStatusRetrying`/`TaskStatusCancelling` 常量（纯增量）。
- 验收结果：tech-debt 一类表全部闭环（#1-#8）；三仓 `make ci` 通过；约定表已登记 `scheduling_model_map`。

### 阶段 2：宿主通道治理（结构性，先契约后实现）

- 设计版本化 capability 分组：`host.tasks@1` / `host.assets@1` / `host.routing@1` / `host.users@1` / `host.metadata@1`，对应现 19 个 method 的归组；通道仍可复用单一 `CoreInvokeService`（分组作用于**授权与声明**，不必改 gRPC service 拆分，避免破坏 ABI）。
- Manifest v2：`manifest_version` + `requires.host`（分组声明）+ `provides`；`genmanifest` 从 `PluginInfo` 生成，保持"生成式、禁手改"。
- 兼容策略：旧扁平 `host.invoke.<method>` 与新分组并存一个版本周期，Core 注册表同时识别。
- 验收：新插件仅声明分组即可获能力；`plugin-contract.md` 更新为 v2 契约；全部官方插件迁移完成后移除扁平声明。

### 阶段 3：网关插件仓内拆层（架构级第一步，不拆仓）

- 在 openai/claude/kiro 仓内把代码按 `internal/gateway/`（对外协议）、`internal/provider/`（上游 auth/session/传输）、`web/`（账号 widget 已天然独立）分包，切断 gateway↔provider 的直接调用糖（定义仓内接口）。
- 不改插件对外形态、不拆仓——先让边界在代码结构上可见、可 lint（可加 import 检查：gateway 包禁 import provider 内部实现）。
- 验收：`plugins.md` 的"混合现状"列出的文件各归其包；新增上游能力只动 `provider/`。

### 阶段 4：Playground UI-only 化

- 依赖既有规划 [`playground-generation-task-refactor.md`](playground-generation-task-refactor.md)（生成任务统一走 Core Task）；对话流式转发改为 Core 编排 API（`gateway.forward` 已具备，需补流式编排与 SSE 由 Core 统一解析或经网关插件声明）。
- 删除 playground 后端的协议解析/任务残留表，收敛为"页面 + 调 Core API"。
- 验收：playground 后端无 SSE/协议格式代码；`plugins.md` 中其"混合现状"清空。

### 阶段 5（可选，架构级）：规范化操作层

- Gateway ↔ Provider 之间引入规范化 operation（`chat.generate` / `image.generate` …），替代原始协议体透传。收益（跨协议复用、provider 可替换）与成本（全链路改造、ABI 演进）都最大。
- 决策点：在阶段 3 完成后评估——若仓内拆层已满足维护诉求，可不做或缩小为仅图像/任务类操作规范化。

## 四、每阶段通用要求

- 实施前先改文档（本文 + `current/` 对应篇），再改代码向文档看齐（与 `current/README.md` 演进流程一致）。
- 每阶段完成后更新 `tech-debt.md` 闭环对应条目。
- 破坏性 ABI 变更（阶段 2 起可能涉及）须评估 core + 全部 7 个插件仓，遵守 `airgate-sdk/CLAUDE.md` 扩展纪律。
