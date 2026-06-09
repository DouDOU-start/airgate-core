# Core 任务状态机设计

> **本文为任务子系统设计。** `tasks.create/update/get/list` 经 `Host.Invoke` 已实现(见 [`current/core-runtime.md`](current/core-runtime.md));版本化 capability、规范化操作等为目标态。现状以 `current/` 为准。

## 目标

Core 需要对外无差别支持同步和异步上游，同时不能把图片、视频、音乐等业务类型写死到平台字段里。

任务引擎只负责通用生命周期：

- 任务持久化
- 状态迁移
- 调度和重试
- 进度、错误、结果记录
- 所属用户和所属插件隔离
- 上游任务 ID 和轮询状态的持久化容器

业务类型由 `task_type` 表达，例如 `image_generation`、`video_generation`、`music_generation`。Core 不按这些字符串做业务分支。

## 非目标

- 对话、翻译、普通转发不进入任务状态机。
- Core 不理解 provider 私有协议。
- Core 不解析 provider 返回体，例如图片 `b64_json`、视频文件结构、音乐质量参数。
- Core 不为图片、视频、音乐分别新增固定字段。
- Core 不维护多个模型字段。使用记录只记录实际使用模型。

## 对外 API 原则

业务 API 保持原来的响应结构，只追加 AirGate 自己的 `task_id`。

例如外部标准本来返回图片响应，则仍返回图片响应；如果 Core 内部创建了任务，只在响应里额外带上 `task_id`。客户端需要查状态时用这个 `task_id` 查询 AirGate 任务。

`/v1/tasks` 只作为管理、调试、轮询 API，不替代原有业务 API。

## 任务固定字段

任务表只保留通用字段：

- `plugin_id`: 创建和处理任务的插件。
- `task_type`: 业务类型字符串，Core 不解释具体含义。
- `user_id`: 所属用户。
- `status`: 状态机状态。
- `stage`: 插件上报的当前阶段，展示和调试使用。
- `progress`: 0 到 100 的进度。
- `input`: 原始任务输入，保存用户请求和扩展参数。
- `output`: 最终业务输出，格式由业务 API 或插件约定。
- `attributes`: 少量展示、筛选维度，Core 不理解业务含义。
- `execution`: 执行状态容器，用于持久化上游任务 ID、轮询游标、平台状态等。
- `error_type`、`error_code`、`error_message`: 失败信息。
- `usage_id`: 完成后关联 `usage_log.id`。
- `idempotency_key`: 同一插件、用户、任务类型内的幂等键。
- 时间字段: `created_at`、`updated_at`、`started_at`、`completed_at`、`cancel_requested_at`、`expires_at`。

不设置 `summary` 字段。完成后的审计、计量、计费事实以 Usage 为准。

## 模型记录

任务本身不固定 `request_model`、`response_model`、`image_model`、`video_model` 等字段。

原则：

- 用户请求了什么，原样留在 `input`。
- 实际使用了什么模型，记录在 Usage 的 `model`。
- 翻译等内部改写模型的场景，对用户透明，只记录实际使用模型。
- 图片、视频、音乐的分辨率、时长、质量、风格等未知扩展参数放在 `input` 或 `attributes`，不进固定列。

## execution 字段

`execution` 是插件和 Core 之间共享的持久化执行上下文。Core 只负责保存和返回，不解释内部结构。

建议结构：

```json
{
  "platform": "openai",
  "account_id": 12,
  "upstream": {
    "task_id": "provider-task-123",
    "status": "queued",
    "poll_after_ms": 2000
  },
  "poll": {
    "next_at": "2026-05-13T10:00:00Z",
    "attempts": 3
  }
}
```

如果上游返回任务 ID，插件必须写入 `execution.upstream.task_id` 或等价字段，避免 Core 重启后丢失任务衔接信息。

## 状态机

当前状态：

- `pending`: 已创建，等待分发。
- `processing`: 插件处理中。
- `retrying`: 本次处理失败，等待重试。
- `completed`: 已成功完成。
- `failed`: 已失败，不再重试。
- `cancelling`: 正在取消。
- `cancelled`: 已取消。

允许迁移：

```text
pending -> processing
pending -> failed
pending -> cancelled

processing -> completed
processing -> failed
processing -> retrying
processing -> cancelling
processing -> cancelled

retrying -> pending
retrying -> failed

cancelling -> cancelled
cancelling -> failed
```

`completed`、`failed`、`cancelled` 是终态，终态任务不能再回到处理中。

## 同步和异步统一语义

Core 对业务调用方不区分上游是同步还是异步，差异由插件适配。

### 上游同步完成

流程：

1. 插件执行同步上游请求。
2. 插件创建任务或拿到已有幂等任务。
3. 插件立即更新为 `completed`。
4. 业务 API 返回原标准响应，并追加 `task_id`。

这种情况下 `task_id` 是审计和后续查询入口，不改变原同步响应语义。

### 上游异步并返回任务 ID

流程：

1. 插件调用上游创建任务。
2. 插件通过 `tasks.create` 写入 AirGate 任务。
3. 插件把上游任务 ID 写入 `execution`。
4. 业务 API 返回原标准响应，并追加 AirGate `task_id`。
5. 后台任务或轮询逻辑根据 `execution` 查询上游状态。
6. 完成后写 `output`、`usage_id`，并更新为 `completed`。

### 上游异步但不支持可查询任务 ID

插件只能选择两种方式：

- 阻塞到上游完成，再按同步完成处理。
- 插件自己持久化可恢复执行状态，再通过 `execution` 写入 Core。

Core 不猜测 provider 私有状态，也不靠内存保存不可恢复任务。

## Host.Invoke 方法

插件通过新版 SDK 的 `Host.Invoke` 调用 Core：

- `tasks.create`: 创建任务。
- `tasks.update`: 更新状态、进度、输出、错误、执行上下文。
- `tasks.get`: 查询单个任务。
- `tasks.list`: 查询任务列表。
- `assets.store`: 存储生成物资产。
- `assets.get_url`: 获取资产访问 URL。
- `gateway.forward`: 需要走 Core 路由、调度、计费时使用。

图片、视频、音乐等生成物必须由插件显式调用资产能力，不允许 Core 根据 provider 响应体做类型特判。

## 使用记录

Usage 是完成后的事实来源：

- 记录实际模型。
- 记录实际用量。
- 记录账号成本、用户成本、币种和倍率。
- 记录通用 metrics、attributes、cost_details。

任务只通过 `usage_id` 关联 Usage，不复制计费字段。

## 当前代码边界

- `internal/plugin/host_service.go`: 只保留 Host.Invoke 分发和通用 Host 能力适配。
- `internal/plugin/task_service.go`: 任务创建、更新、查询和状态迁移。
- `internal/plugin/manager_tasks.go`: pending/retrying 任务分发、重试和卡住任务恢复。
- `ent/schema/task.go`: 任务通用字段定义。

后续新增类型时，应优先扩展插件的 `input`、`output`、`attributes`、`execution` 和 Usage，不扩 Core 固定列。
