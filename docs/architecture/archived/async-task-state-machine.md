> **已归档（2026-06-12）** · 本文为任务子系统的早期完整设计稿，原存于 `airgate-sdk/docs/`。
> 任务设计的现行规范见 [`../task-state-machine.md`](../task-state-machine.md)，实现现状见 [`../current/core-runtime.md`](../current/core-runtime.md)。本文仅供查阅设计背景，**勿作开发依据**（其中 TaskRuntime/TaskDefinition、tasks.cancel 等均未实现）。

# 异步任务状态机设计

本文定义 AirGate 在 Core、SDK、插件之间处理长耗时生成任务的统一设计。目标是让对外业务 API 继续保持原有协议形态，只在响应中追加 AirGate `task_id` 作为追踪字段；Core 内部再把同步上游、异步上游和流式上游统一转换成任务状态机，插件只负责任务类型的业务编解码。

## 背景

当前 `airgate-openai` 的生图任务已经暴露出几个结构性问题：

- 状态查询路径写死在图片语义里，例如 `/v1/images/tasks`、`/v1/images/tasks/list`。
- 插件里的任务创建、执行、查询、列表、响应格式都绑定 `image_generation`。
- 有些上游同步返回图片，有些上游先返回自己的 `task_id` 再轮询，有些上游通过 SSE / WebSocket 流式返回，当前代码需要在具体图片逻辑里混合处理这些差异。
- 后续视频生成、音乐生成、语音合成、文件处理等都可能需要“先返回任务 ID，后查状态”的交互，如果继续按资源类型复制一套任务代码，会很快变成多套状态机。
- 任务能力不是 OpenAI 独有，其他平台插件也可能需要同样的异步执行模型。

因此需要先抽象“任务生命周期”和“上游执行策略”，再把图片任务迁进去。

## 设计目标

1. Core 对插件和协议适配层提供统一任务能力；对业务客户端保持原 API 标准，不要求客户端知道上游是同步、异步还是流式。
2. Core 内部维护唯一的任务状态机，负责创建、排队、执行、重试、取消、查询、列表和权限校验。
3. SDK 提供稳定任务契约，让插件声明任务类型、输入输出 schema、执行能力和查询展示语义。
4. 插件只实现任务业务逻辑：如何识别请求、如何构造任务 input、如何调用上游、如何标准化 output。
5. 图片、视频、音乐、语音、文件处理等长耗时任务都能复用同一套 Core 机制。
6. 对外兼容响应中追加的 `task_id` 必须是 AirGate 自己的任务 ID；上游自己的任务 ID 只能作为内部执行细节存储。
7. 支持同步等待模式和后台任务模式，并允许 Core 在等待超时后自动转为后台任务。
8. 保持现有 OpenAI 兼容 API 可迁移，不要求一次性废弃 `/v1/images/generations` 等现有入口。

## 非目标

- 不在 SDK 中编码 OpenAI、Anthropic 或任意平台的具体参数。
- 不让 Core 理解图片、视频、音乐的业务字段含义。
- 不把某个平台的定价、模型规则、轮询接口写进 Core。
- 不要求所有任务都必须异步执行。短任务仍然可以同步返回。
- 不要求所有插件都立即实现新任务协议。应支持分阶段迁移。
- 不把通用 `/v1/tasks` 作为业务客户端的默认新标准。原来是 OpenAI Images、某个平台视频 API 或现有 AirGate 图片查询 API，对外就继续按原协议返回。

## 核心概念

### AirGate Task

AirGate Task 是 Core 管理的统一任务实体。它是 Core、SDK、插件、管理后台之间的内部稳定对象，不是所有业务客户端都必须直接消费的外部响应格式。

任务对象建议如下：

```json
{
  "id": "ag_task_123",
  "plugin_id": "gateway-openai",
  "type": "image.generate",
  "status": "processing",
  "progress": 35,
  "stage": "polling_upstream",
  "attributes": {
    "platform": "openai",
    "model": "gpt-image-2",
    "size": "1024x1024"
  },
  "input": {
    "model": "gpt-image-2",
    "prompt": "一只柴犬坐在樱花树下",
    "size": "1024x1024"
  },
  "output": null,
  "error": null,
  "created_at": "2026-05-12T10:00:00Z",
  "updated_at": "2026-05-12T10:00:10Z",
  "started_at": "2026-05-12T10:00:01Z",
  "completed_at": null
}
```

`id` 是 AirGate 任务 ID，不等于上游任务 ID。协议适配层对外返回时通常命名为 `task_id`，并追加到原协议响应中。

Core 固定字段必须保持最小化，只包含任务生命周期、归属和安全边界。模型、平台、分辨率、时长、质量、音色等都不应成为 Core 任务表的硬编码列；它们属于插件声明的 `input`、`attributes`、`execution` 或最终 `Usage`。Core 可以保存、返回、按声明做弱索引，但不理解这些字段的业务含义。

模型很重要，但它是“使用记录维度”，不是“任务生命周期字段”。任务和使用记录统一只记录一个主模型字段：`model`。这个字段始终表示实际执行和计费的模型。模型映射、降级和工具链细节对用户透明，不进入通用任务或使用记录字段。

建议模型按以下位置记录：

| 位置 | 用途 | 示例 |
| --- | --- | --- |
| `input.model` | 任务请求里的模型，用于任务复现 | `gpt-image-2` |
| `attributes.model` | 未完成任务列表里的展示/粗筛选 | `gpt-image-2` |
| `execution.*` | 插件内部工具链细节，不作为用户侧模型维度 | `tool_model=gpt-5.4` |
| `usage.Model` / `usage.Attributes.model` | 完成后的审计、计费、统计事实 | `gpt-5.4` |

`Usage` 是最终事实来源。任务完成前可以用 `attributes` 暂存展示值；任务完成后应以关联的使用记录为准。

示例：

- 用户请求 `gpt-image-2`，上游也按 `gpt-image-2` 执行：`model=gpt-image-2`。
- 视频或音乐任务如存在模型字段，同样只记录实际执行和计费模型。

生图的分辨率、视频的时长和帧率、音乐的时长和质量、语音的音色和格式也不能进入 Core 固定字段。它们都属于任务类型自己的 input schema。Core 只保存和透传这些结构化 JSON，不理解字段语义。

通用字段与类型参数的边界：

| 层级 | 字段示例 | Core 是否理解 | 用途 |
| --- | --- | --- | --- |
| 固定生命周期字段 | `id`、`plugin_id`、`type`、`status`、`progress`、`user_id` | 是 | 权限、状态机、查询、列表 |
| 类型化 input | `size`、`duration_seconds`、`quality`、`aspect_ratio`、`voice` | 否 | 插件校验、上游请求构造、任务复现 |
| 展示/弱索引字段 | `attributes.model`、`attributes.duration_seconds`、`attributes.resolution` | 只理解字符串键值 | 任务未完成时的列表页、管理后台、粗筛选 |
| 执行细节 | `execution` | 否 | 插件内部轮询、上游 task id、阶段信息 |

例如图片任务 input：

```json
{
  "prompt": "一只柴犬坐在樱花树下",
  "size": "1024x1024",
  "quality": "high",
  "background": "transparent",
  "output_format": "png"
}
```

视频任务 input：

```json
{
  "prompt": "城市夜景航拍",
  "duration_seconds": 8,
  "aspect_ratio": "16:9",
  "resolution": "1080p",
  "fps": 24
}
```

音乐任务 input：

```json
{
  "prompt": "轻快的电子流行音乐",
  "duration_seconds": 30,
  "quality": "high",
  "format": "mp3",
  "loopable": false
}
```

Core 不为这些字段建固定列。需要列表展示时，插件可以在创建任务时写入少量字符串化的 `attributes`：

```json
{
  "attributes": {
    "duration_seconds": "8",
    "resolution": "1080p",
    "aspect_ratio": "16:9"
  }
}
```

`attributes` 只放少量字符串化、可展示、可粗筛选的维度；完整参数仍以 `input` 为准。

### Upstream Task

Upstream Task 是某些上游平台返回的任务对象，例如：

```json
{
  "task_id": "img_abc",
  "status_url": "https://provider.example/tasks/img_abc"
}
```

它只属于插件执行细节，必须持久化到任务的 `execution` 字段，不作为客户端主 ID。

持久化 `execution` 的原因：

- Core worker 或插件进程重启后，需要继续按上游任务 ID 轮询。
- 等待模式超时转后台后，需要恢复上游任务状态。
- 上游短暂失败后重试时，需要判断是继续轮询已有上游任务，还是重新创建。
- 取消任务时，如果上游支持取消，需要拿上游任务 ID 调用取消接口。

客户端查询时也不应直接使用上游任务 ID。查询入口由协议适配层决定：

```text
GET /v1/images/tasks/ag_task_123
```

或在未来的 Core 管理 API 中查询：

```text
GET /v1/tasks/ag_task_123
```

但无论入口路径如何，最终查询的都是 AirGate Task，而不是直接查询上游：

```text
GET /provider/tasks/img_abc
```

### Task Type

任务类型使用领域动作命名，而不是平台命名：

```text
image.generate
image.edit
video.generate
music.generate
audio.speech
file.process
```

平台差异由 `plugin_id`、插件 schema、`input` 和 `attributes` 表达，不应把平台或模型塞进类型名里。

不推荐：

```text
openai_image_generation
gpt_image_task
```

推荐：

```text
image.generate
```

### Execution Mode

Execution Mode 表示上游实际如何产出结果。

```go
type TaskExecutionMode string

const (
	TaskExecutionSyncResult   TaskExecutionMode = "sync_result"
	TaskExecutionUpstreamTask TaskExecutionMode = "upstream_task"
	TaskExecutionStreamResult TaskExecutionMode = "stream_result"
)
```

含义：

| 模式 | 上游行为 | 插件行为 |
| --- | --- | --- |
| `sync_result` | 单次 HTTP 调用直接返回最终结果 | 解析响应并完成 AirGate Task |
| `upstream_task` | 上游返回自己的 `task_id` | 保存上游任务信息，轮询直到完成 |
| `stream_result` | 上游通过 SSE / WebSocket 持续返回 | 消费流并聚合最终结果 |

Execution Mode 是插件内部执行策略，不直接决定客户端使用同步还是异步接口。

### Response Mode

Response Mode 表示协议适配层希望 Core 如何处理本次请求的等待策略。它可以来自网关配置、插件默认值、Header 或显式 AirGate 扩展参数，但不要求所有业务客户端都感知这个概念。

```go
type TaskResponseMode string

const (
	TaskResponseModeWait       TaskResponseMode = "wait"
	TaskResponseModeBackground TaskResponseMode = "background"
	TaskResponseModeAuto       TaskResponseMode = "auto"
)
```

| 模式 | 协议适配层语义 |
| --- | --- |
| `wait` | Core 尽量等待任务完成，适配层返回原协议最终结果并追加 `task_id` |
| `background` | Core 创建任务后立即返回，适配层返回该协议自己的异步响应并带上 `task_id` |
| `auto` | Core 等待一小段时间；超时未完成则由适配层返回该协议自己的异步响应并带上 `task_id` |

Response Mode 与 Execution Mode 独立。

例如：

- `wait` + 上游 `sync_result`：通常直接返回原协议最终结果，并追加 `task_id`。
- `wait` + 上游 `upstream_task`：Core 可以轮询一段时间，完成则返回原协议最终结果；超时则由适配层返回原协议的异步响应。
- `background` + 上游 `sync_result`：Core 仍然立即创建后台任务，适配层返回异步响应，后台 worker 同步调上游并完成任务。
- `auto` + 上游 `stream_result`：Core 聚合流；超过等待窗口就转后台，适配层返回异步响应。

## 状态机

Core 维护统一状态机：

```text
pending
  -> processing
      -> completed
      -> failed
      -> cancelling -> cancelled
      -> retrying -> pending
```

建议状态定义：

| 状态 | 含义 | 可被查询 |
| --- | --- | --- |
| `pending` | 已创建，等待调度 | 是 |
| `processing` | 插件正在执行 | 是 |
| `retrying` | 本次执行失败，等待下一次重试 | 是 |
| `completed` | 最终结果已写入 output | 是 |
| `failed` | 最终失败，已无重试 | 是 |
| `cancelling` | 正在请求取消 | 是 |
| `cancelled` | 已取消 | 是 |

状态迁移规则：

```text
pending -> processing
processing -> completed
processing -> failed
processing -> retrying
retrying -> pending
processing -> cancelling
cancelling -> cancelled
cancelling -> failed
```

禁止迁移：

```text
completed -> processing
failed -> processing
cancelled -> processing
```

终态：

```text
completed
failed
cancelled
```

Core 应校验状态迁移，插件不能随意把任意状态写入任务。

## 对外 API 兼容原则

业务客户端看到的 API 应继续保持原平台或当前项目已经定义的标准。Core 内部可以统一成 AirGate Task，但协议适配层对外返回时必须投影回原协议响应，只额外追加 `task_id` 作为 AirGate 追踪 ID。

核心原则：

1. 创建入口不改。原来调用 `POST /v1/images/generations`，仍然调用这个入口；未来视频、音乐也按各自平台或插件已经定义的入口暴露。
2. 请求 body 不强制新增通用任务字段。模型、分辨率、时长、质量等仍按原协议传递。
3. 响应 schema 不替换成通用 Task schema。完成、处理中、失败、查询、列表都由对应协议适配层决定响应结构。
4. 响应中允许追加 `task_id`。这个字段始终是 AirGate Task ID，不是上游任务 ID。
5. 上游同步、上游异步、上游流式只影响 Core 内部执行模式，不影响外部 API 标准。
6. 通用 `/v1/tasks` 可以作为 Core 管理 API、调试 API 或后台管理 API，但不作为业务客户端迁移目标。

### 创建入口映射

现有 OpenAI 风格入口继续保留：

```text
POST /v1/images/generations
POST /v1/images/edits
```

协议适配层在内部映射到任务类型：

```text
/v1/images/generations -> type=image.generate
/v1/images/edits       -> type=image.edit
```

流程：

```text
客户端调用原入口
协议适配层解析原协议请求
协议适配层构造 Task input
Core 创建 AirGate Task
Core 按状态机调度执行
插件调用上游并写入 output / error / usage
协议适配层把 Task 结果投影回原协议响应
响应中追加 task_id
```

### 完成响应

如果 OpenAI Images 兼容接口在等待窗口内完成，响应仍然是 Images API schema，只追加 `task_id`：

```json
{
  "created": 1713833628,
  "data": [
    {
      "b64_json": "..."
    }
  ],
  "usage": {},
  "task_id": "ag_task_123"
}
```

严格兼容模式下，如果某些官方 SDK 对未知字段非常敏感，可以通过网关配置决定是否追加 `task_id`；但 AirGate 自己的默认响应建议追加，便于记录、排查和后续查询。

### 未完成响应

如果调用选择后台执行，或 `wait` / `auto` 等待超时，响应也不应强制改成通用 Task schema，而应使用当前入口已有的异步响应形态。

例如当前项目若已有图片任务响应：

```json
{
  "task_id": "ag_task_123",
  "status": "processing"
}
```

就继续保持这个响应。未来视频、音乐如果某个平台标准本身有 job / task / generation 对象，也映射成对应平台对象，只把其中的 ID 换成 AirGate `task_id` 或额外追加 AirGate `task_id`。

如果某个同步协议本身没有异步响应标准，默认不应突然返回 AirGate Task 对象。只有在客户端显式选择异步模式，或该插件文档明确声明 AirGate 扩展响应时，才返回该协议适配层定义的异步响应。

### 查询和列表

查询入口也保持原有协议或当前项目已有路径。例如当前图片任务可以继续：

```text
GET /v1/images/tasks/{task_id}
GET /v1/images/tasks/list
```

这些入口内部查询 Core Task，但响应仍由图片协议适配层生成。后续视频、音乐如果需要查询，也按各自协议风格暴露，例如：

```text
GET /v1/videos/tasks/{task_id}
GET /v1/music/tasks/{task_id}
```

是否真的使用这些路径由对应插件协议决定，Core 不要求所有媒体类型共享同一条外部查询路径。

### 取消任务

取消同样由协议适配层决定是否暴露以及如何命名。Core 只提供内部取消能力：

```text
tasks.cancel
```

取消能力取决于插件声明：

- 如果上游支持取消，插件调用上游取消接口。
- 如果上游不支持取消，Core 标记 `cancelling` 后尽力中止本地轮询或流消费。
- 如果任务已完成，取消返回当前终态，不应破坏结果。

### Core 管理 API

通用任务 API 可以存在，但它的定位是内部管理、后台页面、调试、测试或运维，不是业务客户端的标准协议：

```text
GET /v1/tasks/{task_id}
GET /v1/tasks?type=image.generate&status=completed&limit=20&offset=0
POST /v1/tasks/{task_id}/cancel
```

这类 API 可以返回 AirGate Task schema，因为调用方明确知道自己在访问 AirGate 管理能力。普通业务 API 不应直接返回这个 schema。

## Core 职责

Core 负责以下能力：

1. 任务表结构。
2. 状态机校验。
3. 用户和分组权限校验。
4. 账号调度和 failover。
5. 后台 worker 分发任务给插件。
6. 根据任务类型找到插件和 handler。
7. 记录任务 attempts、max_attempts、priority。
8. 写入 output、error、progress。
9. 查询和列表接口。
10. 取消任务接口。
11. 任务结果与使用记录关联。
12. 任务过期清理策略。
13. 对同步等待模式实现 wait / timeout / fallback to task。

Core 不应理解：

- 图片 prompt 字段怎么解析。
- 视频时长如何映射到上游参数。
- 音乐质量、音色、格式、循环等字段是否合法。
- 某个模型支持哪些扩展参数。
- 上游任务状态字段叫什么。
- 某个平台的失败码是否可重试。
- 某个平台的具体计费公式。

这些都属于插件。

## SDK 职责

SDK 需要表达三类契约。

### 任务 schema 声明

任务 schema 在新协议中的最小形态：

```go
type TaskSchema struct {
	Type     string
	Input    PayloadSchema
	Output   PayloadSchema
	Metadata map[string]string
}
```

任务类型展示名称和说明不是状态机必要字段，可以由管理后台根据 `type`、`Metadata` 或插件前端 schema 生成，不进入 Core 任务协议。

建议扩展或通过 `Metadata` 先承载执行策略：

```go
type TaskSchema struct {
	Type           string
	Input          PayloadSchema
	Output         PayloadSchema
	DefaultMode    string
	MaxAttempts    int
	Cancellable    bool
	ProgressMode   string
	ResultProtocol string
	Metadata       map[string]string
}
```

字段含义：

| 字段 | 含义 |
| --- | --- |
| `DefaultMode` | 默认响应模式，通常是 `auto` 或 `wait` |
| `MaxAttempts` | 默认最大重试次数 |
| `Cancellable` | 是否支持取消 |
| `ProgressMode` | `none`、`percent`、`stage` |
| `ResultProtocol` | `task`、`openai.images`、`openai.responses` 等展示/兼容提示 |

不同任务类型和模型的扩展参数通过 `Input` 的 JSON Schema 表达。Core 不为这些参数新增 Go 字段或数据库列。

图片任务 schema 示例：

```json
{
  "type": "object",
  "required": ["prompt"],
  "properties": {
    "prompt": { "type": "string" },
    "size": { "type": "string", "examples": ["1024x1024", "1536x1024"] },
    "quality": { "type": "string", "enum": ["low", "medium", "high", "auto"] },
    "background": { "type": "string", "enum": ["opaque", "transparent"] },
    "output_format": { "type": "string", "enum": ["png", "jpeg", "webp"] }
  },
  "additionalProperties": true
}
```

视频任务 schema 示例：

```json
{
  "type": "object",
  "required": ["prompt"],
  "properties": {
    "prompt": { "type": "string" },
    "duration_seconds": { "type": "integer", "minimum": 1, "maximum": 60 },
    "resolution": { "type": "string" },
    "aspect_ratio": { "type": "string" },
    "fps": { "type": "integer" }
  },
  "additionalProperties": true
}
```

音乐任务 schema 示例：

```json
{
  "type": "object",
  "required": ["prompt"],
  "properties": {
    "prompt": { "type": "string" },
    "duration_seconds": { "type": "integer", "minimum": 1 },
    "quality": { "type": "string" },
    "format": { "type": "string" },
    "loopable": { "type": "boolean" }
  },
  "additionalProperties": true
}
```

这里建议 `additionalProperties: true`，原因是上游能力变化很快，插件需要能先透传新参数；插件可以按模型能力做更严格的运行时校验。管理后台和开发工具使用 schema 渲染表单和提示，Core 后端只保存 JSON。

如果短期不想改 SDK 强类型，可以先放入 `Metadata`：

```go
Metadata: map[string]string{
  "default_mode": "auto",
  "max_attempts": "3",
  "cancellable": "false",
  "progress_mode": "percent",
  "result_protocol": "openai.images",
}
```

### 任务执行接口

当前 SDK 只有：

```go
type TaskProcessor interface {
	ProcessTask(ctx context.Context, task HostTask) error
	TaskTypes() []string
}
```

中期建议演进为：

```go
type TaskDefinitionProvider interface {
	TaskDefinitions() []TaskDefinition
}

type TaskDefinition struct {
	Schema  TaskSchema
	Handler TaskHandler
}

type TaskHandler interface {
	Execute(ctx context.Context, task HostTask, runtime TaskRuntime) (*TaskResult, error)
}
```

其中 `TaskRuntime` 由 SDK 或插件本地封装，负责安全地更新状态：

```go
type TaskRuntime interface {
	SetProcessing(ctx context.Context, progress int, stage string) error
	SetProgress(ctx context.Context, progress int, stage string) error
	Complete(ctx context.Context, output map[string]any, usage *Usage) error
	Fail(ctx context.Context, err TaskError) error
	IsCancellationRequested(ctx context.Context) bool
}
```

短期为了减少 SDK 破坏，可以先在插件内部实现这个 runtime，Core 仍然通过 `tasks.update` 接收状态更新。

### Host method

现有 Host method：

```text
tasks.create
tasks.update
tasks.get
tasks.list
gateway.forward
```

建议补齐或标准化：

```text
tasks.cancel
tasks.append_event
tasks.get_cancellation
```

`tasks.append_event` 用于记录阶段事件，例如：

```json
{
  "task_id": "ag_task_123",
  "event": "upstream_task_created",
  "payload": {
    "mode": "upstream_task"
  }
}
```

如果暂时不做 event 表，也可以把 execution 信息存进任务 output 的内部字段，但不建议长期这么做。

### 资产存储

Core 提供统一的资产存储能力，插件不应自行实现文件下载和持久化。

已有 Host method：

```text
assets.store        存储原始字节（插件已有数据在内存中）
assets.store_url    从外部 URL 下载并存储（Core 负责 HTTP 下载、大小限制、Content-Type 检测）
assets.get_url      获取已存储资产的可访问 URL
assets.get_bytes    获取已存储资产的原始字节
```

职责边界：

| 层级 | 职责 | 示例 |
| --- | --- | --- |
| Core 资产存储 | HTTP 下载、大小限制、Content-Type 检测、持久化（本地 / S3）、URL 签发 | `assets.store_url` 下载 50MB 以内的外部图片 |
| 插件 | 识别 output 中哪些字段含可下载媒体、调用对应 Host method、注册到自己的资产跟踪表 | 遍历 Images API 响应的 `data[]`，对 `b64_json` 调 `assets.store`，对外部 `url` 调 `assets.store_url` |
| Core 任务系统 | 持久化 output JSON、状态机、权限 | 不解析 output 中的 URL 或 base64 字段 |

设计原则：

- Core 不理解任务 output 的结构，不自动扫描或处理 output 中的媒体字段。
- 插件在执行任务时主动调用 `assets.store` 或 `assets.store_url`，把外部 URL 或 base64 转为 Core 管理的本地 URL，再写入 output。
- 任务完成后 output 中应只包含稳定的本地 URL，不应包含可能过期的外部签名 URL。
- 后续新增视频、音乐等媒体类型时，插件只需调用同一组 Host method，不需要各自实现下载逻辑。

## 插件职责

插件实现任务定义，不实现通用状态机。

建议插件内部结构：

```go
type TaskHandler interface {
	Type() string
	BuildInput(ctx context.Context, req *sdk.ForwardRequest) (map[string]any, error)
	Execute(ctx context.Context, task sdk.HostTask, runtime TaskRuntime) error
	BuildResponse(task *sdk.HostTask) map[string]any
}
```

如果同一插件有多个任务：

```go
type TaskRegistry struct {
	handlers map[string]TaskHandler
}
```

OpenAI 插件注册：

```go
registry.Register(OpenAIImageGenerateTask{})
registry.Register(OpenAIImageEditTask{})
registry.Register(OpenAIVideoGenerateTask{})
registry.Register(OpenAIMusicGenerateTask{})
```

`ProcessTask` 只做分发：

```go
func (g *OpenAIGateway) ProcessTask(ctx context.Context, task sdk.HostTask) error {
	handler := g.tasks.Get(task.TaskType)
	if handler == nil {
		return fmt.Errorf("不支持的任务类型: %s", task.TaskType)
	}
	return g.runner.Run(ctx, task, handler)
}
```

## 上游执行策略

### 同步上游

流程：

```text
Core 创建 AirGate Task
Core worker 分发给插件
插件调用上游
上游直接返回最终结果
插件标准化 output
Core 标记 completed
```

适合：

- 当前很多 Images API 直连上游。
- 小文件处理。
- 短音频生成。

### 异步上游

流程：

```text
Core 创建 AirGate Task
插件调用上游创建任务
上游返回 upstream_task_id
插件保存 execution 信息
插件轮询上游状态
上游完成
插件获取最终结果
Core 标记 completed
```

插件内部 execution 示例：

```json
{
  "mode": "upstream_task",
  "provider": "apimart",
  "upstream_task_id": "img_abc",
  "status_url": "https://provider.example/tasks/img_abc",
  "poll_interval_ms": 3000,
  "last_status": "running",
  "next_poll_at": "2026-05-12T10:00:20Z",
  "created_at": "2026-05-12T10:00:01Z",
  "updated_at": "2026-05-12T10:00:10Z"
}
```

这类字段是任务恢复所必需的持久化状态，不应直接作为公共 output 暴露给普通用户；管理后台可以按权限查看脱敏信息。

`execution` 更新策略：

- 插件拿到上游任务 ID 后应立即写入 `execution`，再进入轮询。
- 每次轮询后更新 `last_status`、`updated_at`、`next_poll_at` 等恢复所需字段。
- 如果上游返回结果 URL、文件 ID 或下载 token，优先保存可恢复的引用，不要只保存在内存里。
- 如果字段包含签名 URL、token 或账号相关敏感信息，应加密、脱敏或只保存可重新获取结果的非敏感 ID。
- Core 不解析 `execution` 业务字段，只负责持久化、权限隔离和传回同一插件继续执行。

### 流式上游

流程：

```text
Core 创建 AirGate Task
插件打开 SSE / WebSocket
插件消费事件
插件更新 progress / stage
插件聚合最终结果
Core 标记 completed
```

适合：

- ChatGPT OAuth WebSocket 图片工具。
- 后续可能的流式视频生成进度。

## 任务与使用记录

任务表不是审计和计费事实表。AirGate 里所有可计费或需要审计的调用都应该落到统一使用记录里，包括：

- 图片生成和图片编辑。
- 视频生成。
- 音乐生成。
- 语音合成。
- 未来其它工具型、媒体型或文件型任务。

普通对话模型调用、Responses / Chat Completions 调用、Anthropic Messages 协议翻译等不需要任务状态机。它们仍然通过现有同步或流式 Forward 路径直接写 Usage。任务状态机只处理长耗时、可后台化、需要查询状态的生成或处理类任务。

任务和使用记录的关系：

```text
同步普通调用
  ForwardOutcome.Usage -> Core 写使用记录

异步任务调用
  Core 创建 Task
  插件执行 Task
  插件返回 Usage
  Core 写使用记录
  Task.usage_id -> Usage.id
```

也就是说，任务只回答“这件事做到哪一步了”，使用记录回答“这次调用实际用了什么、产出了什么计量、花了多少钱”。

### Usage 统一维度

SDK 已经提供通用用量结构：

```go
type Usage struct {
	Model       string
	Summary     string
	Attributes  []UsageAttribute
	Metrics     []UsageMetric
	CostDetails []UsageCostDetail
	Metadata    map[string]string
}
```

模型和扩展参数应进入 Usage，而不是 Core 任务固定列。对话类调用也使用同一个 Usage 结构，但不经过 Task。

图片生成示例：

```json
{
  "model": "gpt-image-2",
  "summary": "图片生成 · gpt-image-2 · 1024x1024",
  "attributes": [
    { "key": "modality", "kind": "custom", "label": "类型", "value": "image" },
    { "key": "model", "kind": "model", "label": "模型", "value": "gpt-image-2" },
    { "key": "resolution", "kind": "resolution", "label": "分辨率", "value": "1024x1024" },
    { "key": "quality", "kind": "quality", "label": "质量", "value": "high" }
  ],
  "metrics": [
    { "key": "image_count", "kind": "image", "label": "图片张数", "unit": "image", "value": 1 },
    { "key": "output_tokens", "kind": "token", "label": "图像输出 Token", "unit": "token", "value": 4160 }
  ]
}
```

视频生成示例：

```json
{
  "model": "video-model",
  "summary": "视频生成 · 8s · 1080p",
  "attributes": [
    { "key": "modality", "kind": "custom", "label": "类型", "value": "video" },
    { "key": "model", "kind": "model", "label": "模型", "value": "video-model" },
    { "key": "resolution", "kind": "resolution", "label": "分辨率", "value": "1080p" },
    { "key": "quality", "kind": "quality", "label": "质量", "value": "standard" }
  ],
  "metrics": [
    { "key": "video_seconds", "kind": "video", "label": "视频时长", "unit": "second", "value": 8 }
  ]
}
```

音乐生成示例：

```json
{
  "model": "music-model",
  "summary": "音乐生成 · 30s · high",
  "attributes": [
    { "key": "modality", "kind": "custom", "label": "类型", "value": "music" },
    { "key": "model", "kind": "model", "label": "模型", "value": "music-model" },
    { "key": "quality", "kind": "quality", "label": "质量", "value": "high" },
    { "key": "format", "kind": "custom", "label": "格式", "value": "mp3" }
  ],
  "metrics": [
    { "key": "audio_seconds", "kind": "audio", "label": "音频时长", "unit": "second", "value": 30 }
  ]
}
```

### Task 与 Usage 的字段边界

| 信息 | Task 中的位置 | Usage 中的位置 | 说明 |
| --- | --- | --- | --- |
| 生命周期状态 | `status`、`progress`、`stage` | 不存 | Task 独有 |
| 客户端请求参数 | `input` | 可按需复制到 `Attributes` | 完整参数以 Task input 为准 |
| 上游执行细节 | `execution` | 可脱敏后进入 `Metadata` | 普通用户默认不看 execution |
| 模型 | `input.model` / `attributes.model` 临时展示 | `Model` / `Attributes` | 计费与审计以 Usage 为准 |
| 分辨率、时长、质量 | `input` / `attributes` 临时展示 | `Attributes` / `Metrics` | 统计以 Usage 为准 |
| token、图片张数、视频秒数 | 不建议存 Task 固定列 | `Metrics` | 统一统计入口 |
| 成本 | 不建议存 Task 固定列 | `AccountCost` / `CostDetails` | 统一扣费入口 |
| 使用记录关联 | `usage_id` | `id` | Task 完成后关联 |

列表页如果要展示未完成任务，可以读取 Task 的 `attributes`，或按插件声明的 schema 组合展示文案。完成后的历史账单、统计图、成本明细必须读取 Usage。

## OpenAI 图片迁移映射

当前行为：

```text
/v1/images/generations
/v1/images/edits
/v1/images/tasks
/v1/images/tasks/list
```

目标映射：

| 当前入口 | 目标任务类型 |
| --- | --- |
| `/v1/images/generations` | `image.generate` |
| `/v1/images/edits` | `image.edit` |

当前上游路径：

| 上游情况 | Execution Mode |
| --- | --- |
| API Key 上游直接返回 Images JSON | `sync_result` |
| API Key 中转返回上游 `task_id` | `upstream_task` |
| OAuth Responses tool / WebSocket 聚合图片 | `stream_result` |
| Web Reverse 生成后返回图片 | `sync_result` 或 `stream_result`，取决于实现细节 |

迁移后的插件内部结构：

```text
task_registry.go
task_runner.go
task_http.go
task_image_generate.go
task_image_edit.go
```

`task_images.go` 不再同时承担所有职责。

## 数据模型建议

Core 任务表建议字段：

```text
id
plugin_id
task_type
status
progress
stage
user_id
input
output
attributes
execution
error_type
error_code
error_message
usage_id
attempts
max_attempts
priority
idempotency_key
created_at
updated_at
started_at
completed_at
cancel_requested_at
expires_at
```

字段说明：

| 字段 | 说明 |
| --- | --- |
| `input` | 插件标准化后的任务输入 |
| `output` | 插件标准化后的任务输出 |
| `attributes` | 插件提供的少量展示/筛选维度，值建议统一转字符串 |
| `execution` | 插件内部执行状态，例如 upstream task id、轮询状态；必须持久化以支持重启恢复 |
| `usage_id` | 关联使用记录；完成后的模型、计量和费用事实以 usage 为准 |
| `idempotency_key` | 防止客户端重复创建相同任务 |
| `expires_at` | 任务结果保留时间 |

`execution` 应有权限边界。普通用户查询任务时不返回或只返回脱敏摘要。

## 错误模型

任务错误建议统一为：

```json
{
  "type": "upstream_error",
  "code": "rate_limited",
  "message": "请求暂时无法完成，请稍后重试",
  "retryable": true
}
```

错误类型建议：

| type | 含义 |
| --- | --- |
| `invalid_request` | 客户端参数错误 |
| `auth_error` | 上游认证失败 |
| `rate_limited` | 上游限流 |
| `quota_exceeded` | 额度不足 |
| `upstream_error` | 上游服务错误 |
| `timeout` | 执行超时 |
| `cancelled` | 用户取消 |
| `internal_error` | Core 或插件内部错误 |

插件负责把上游错误映射到标准错误。Core 负责按错误类型决定是否重试和如何展示。

## 重试策略

Core 应控制重试次数，插件提供错误是否可重试的建议。

建议：

- `invalid_request` 不重试。
- `auth_error` 不重试，并可能标记账号不可用。
- `rate_limited` 可以按 `retry_after` 重试。
- `upstream_error` 可以重试。
- `timeout` 可以重试，但要限制总耗时。
- 用户取消不重试。

任务 attempt 不应重复扣费。只有上游成功返回可计费用量时才写使用记录；失败费用是否记录由插件按平台规则决定。

## 同步等待策略

Core 对 `wait` / `auto` 模式应有统一等待窗口。

建议配置：

```text
default_wait_timeout = 120s
max_wait_timeout = 300s
background_poll_interval = 1s
```

等待模式流程：

```text
创建任务
立即调度
等待 completed / failed / cancelled
如果超时：
  协议适配层返回原协议的异步响应 + task_id
如果完成：
  协议适配层返回原协议最终响应 + task_id
```

这使同步上游和异步上游都可以对外表现为原同步接口；只有等待超时或显式后台模式时，才返回该协议自己的异步响应。

## 结果协议

任务 output 应在 Core 内部统一，但对外必须支持原协议返回。业务客户端不直接消费 `task_output`，而是消费协议适配层生成的响应。

统一任务 output：

```json
{
  "kind": "image",
  "items": [
    {
      "mime_type": "image/png",
      "b64_json": "..."
    }
  ],
  "usage": {}
}
```

OpenAI Images 兼容响应：

```json
{
  "created": 1713833628,
  "data": [
    {
      "b64_json": "..."
    }
  ],
  "usage": {}
}
```

插件应提供 output 到协议响应的转换器。Core 不理解图片字段，只调用插件声明的转换能力，或让插件在任务完成时同时写入：

```json
{
  "task_output": {},
  "protocol_outputs": {
    "openai.images": {}
  }
}
```

短期可以先让插件的旧入口查询任务后自行转换；长期再把协议转换纳入 SDK 能力。

## 权限与安全

Core 查询任务时必须校验：

- 当前用户只能查询自己的任务。
- 管理员可以查询所有任务。
- API Key 只能查询该 Key 所属用户/分组创建的任务。
- 插件只能更新自己创建的任务。
- 插件不能越权读取其他插件任务。

敏感字段：

- 上游 access token 不得进入 task input / output。
- 上游 `upstream_task_id` 默认不返回普通用户。
- 上游原始错误需要脱敏后再写入 `error.message`。
- 原始响应如包含签名 URL，需要设置过期和权限边界。

## 幂等性

创建任务应支持幂等键：

```text
Idempotency-Key: xxx
```

或：

```json
{
  "idempotency_key": "xxx"
}
```

同一用户、同一插件、同一任务类型、同一幂等键，在任务未过期前应返回同一个 AirGate Task。

这能避免客户端超时重试时重复生成图片/视频并重复扣费。

## 进度语义

进度应是 Core 通用字段，但含义由插件声明。

建议：

| progress | 通用含义 |
| --- | --- |
| 0 | 已创建 |
| 10 | 开始处理 |
| 30 | 已发送到上游 |
| 50 | 上游处理中 |
| 80 | 正在下载或整理结果 |
| 100 | 完成 |

插件可以附加 `stage`：

```json
{
  "progress": 50,
  "stage": "polling_upstream"
}
```

普通客户端可以只看百分比，高级 UI 可以展示 stage。

## 迁移计划

### 阶段一：文档与内部抽象

目标：不改外部行为，先让 `airgate-openai` 内部不再写死图片状态机。

改动：

- 新增插件内部 `TaskRegistry`。
- 新增插件内部 `TaskRunner`。
- 把当前 `image_generation` 迁为 `image.generate` / `image.edit` 的 handler。
- `ProcessTask` 改为按 `task.TaskType` 分发。
- 查询响应改为通用 `buildTaskResponse`，图片字段由 handler 注入。
- 保留 `/v1/images/tasks` 和 `/v1/images/tasks/list`，但内部调用通用查询函数。

验收：

- 现有图片任务行为不变。
- 新增任务类型不需要改 `ProcessTask` 主流程。
- 后端测试通过。

### 阶段二：SDK schema 补齐

目标：插件能声明结构化任务能力。

改动：

- 扩展 `TaskSchema` 或先用 `Metadata` 标准化字段。
- `SchemaProvider` 返回 task schema。
- devserver 展示任务 schema。
- Core 能读取插件 task schema。

验收：

- `airgate-openai` 能声明 `image.generate` / `image.edit`。
- Core 管理后台能看到插件支持的任务类型。

### 阶段三：Core 通用任务服务

目标：Core 提供内部统一任务服务，协议适配层通过它创建、查询、取消和更新任务；业务客户端仍走原 API。

改动：

- Core 新增通用任务创建、查询、列表、取消 service。
- Core 实现 wait/background/auto。
- Core 实现幂等键。
- Core worker 支持按 task type 分发给插件。
- Core 对 task output 做权限过滤。
- 可选提供受权限保护的 `/v1/tasks` 管理 API，用于后台、调试和运维。

验收：

- 图片协议适配层可以通过 Core service 查询 `task_id` 对应的内部任务。
- 同步上游和异步上游在原图片 API 中表现一致。
- 普通业务客户端不需要迁移到 `/v1/tasks`。

### 阶段四：协议入口接入 Core 任务服务

目标：OpenAI Images 等原有入口复用 Core 任务服务，但对外响应保持原协议。

改动：

- `/v1/images/generations` 内部创建 `image.generate` 任务，完成时返回 Images schema 并追加 `task_id`。
- `/v1/images/edits` 内部创建 `image.edit` 任务，完成时返回 Images schema 并追加 `task_id`。
- `/v1/images/tasks` 和 `/v1/images/tasks/list` 继续保持当前响应结构，内部改为查询 Core Task。
- 新增视频/音乐任务时只加 handler 和 schema。

验收：

- OpenAI SDK 默认路径仍可同步拿结果。
- 返回中能看到 AirGate `task_id`。
- 显式异步模式返回当前协议定义的异步响应，而不是通用 AirGate Task schema。
- 视频/音乐不新增专属状态机。

### 阶段五：清理内部重复状态机

目标：清理插件内部按图片写死的状态机和轮询代码，保留对外兼容路由。

前提：

- Core 通用任务服务已稳定。
- 图片入口已通过协议适配层读写 Core Task。
- 视频、音乐或其他任务类型能复用同一套 runner。

改动：

- 删除插件里的图片专用状态迁移、轮询和重试分支。
- 保留 `/v1/images/tasks` 和 `/v1/images/tasks/list` 的对外兼容壳。
- 查询函数只负责协议响应投影，不再自己管理状态机。

## 当前 `airgate-openai` 应先怎么改

建议先做插件内部重构，不等待 Core 完整改造。

第一步文件结构：

```text
backend/internal/gateway/task_registry.go
backend/internal/gateway/task_runner.go
backend/internal/gateway/task_http.go
backend/internal/gateway/task_image.go
```

职责：

| 文件 | 职责 |
| --- | --- |
| `task_registry.go` | 注册和查找 task handler |
| `task_runner.go` | 通用状态迁移、调用 `forwardViaHost`、错误处理 |
| `task_http.go` | 从 HTTP 请求解析 task_id、列表参数、写当前协议的任务查询响应 |
| `task_image.go` | OpenAI 图片 input/output 编解码 |

第二步替换点：

- `TaskTypes()` 从 registry 生成。
- `ProcessTask()` 从 registry 查 handler。
- `forwardImagesViaTask()` 改成 `forwardTask()`，图片 handler 负责 `BuildInput`。
- `buildTaskRequestBody()` 移入图片 handler。
- `buildTaskResponse()` 支持 handler 自定义 output projection。

第三步保留行为：

- 仍然支持 `/v1/images/tasks`。
- 仍然返回当前图片任务响应字段。
- 仍然支持当前 API Key 同步上游和上游异步 `task_id` 轮询。

这样后续加视频时只做：

```go
registry.Register(OpenAIVideoGenerateTask{})
```

不改状态机。

## 测试计划

Core 测试：

- 创建内部任务后能返回 AirGate `task_id` 给协议适配层。
- wait 模式任务完成时返回最终结果。
- wait 超时返回待处理状态和 `task_id`，由协议适配层投影成原协议异步响应。
- background 模式立即返回 `task_id` 给协议适配层。
- 查询任务权限校验。
- 状态机非法迁移被拒绝。
- 幂等键重复请求返回同一任务。
- 取消 pending / processing / completed 任务。

SDK 测试：

- `TaskSchema` protobuf 往返。
- `TaskTypes` / `TaskDefinitions` 能被 gRPC runtime 正确暴露。
- Host method capability 校验。

插件测试：

- 图片文生图请求能构造 `image.generate` input。
- 图片编辑请求能构造 `image.edit` input。
- 同步上游响应能完成任务。
- 上游 `task_id` 响应能进入轮询并完成任务。
- 完成响应保持 OpenAI Images schema，并额外包含 AirGate `task_id`。
- 未完成响应保持当前图片任务响应结构，不返回通用 AirGate Task schema。
- 上游错误能写入标准 error。
- `ProcessTask` 遇到未知 task type 返回明确错误。
- 旧 `/v1/images/tasks` 查询仍然兼容。

## 开放问题

1. 通用 `/v1/tasks` 是否需要对外暴露为管理 API；如果暴露，权限边界和返回字段需要单独设计。
2. `task_id` 使用数字 ID 还是字符串 ID？建议对外使用字符串，例如 `ag_task_123`，内部可继续用 bigint。
3. OpenAI SDK 兼容路径默认应该是 `wait` 还是 `auto`？建议默认 `wait`，显式请求才异步。
4. task output 是否长期保存完整 base64？图片/视频结果可能很大，后续需要对象存储或结果过期策略。
5. 插件是否需要实现协议响应转换接口，还是由插件在 output 中同时写入兼容响应？
6. 取消任务是否必须进入 SDK 强类型接口，还是先通过 Host method 实现？

## 结论

异步任务不应该作为图片功能的附属能力存在。它应该是 AirGate Core 的通用执行模型：

- Core 管任务生命周期。
- SDK 管任务契约声明。
- 插件管任务业务执行。
- 协议适配层把原 API 请求转成内部 Task，再把 Task 结果投影回原 API 响应。
- 业务客户端继续看原协议响应，只额外拿到 AirGate `task_id`，不看上游同步/异步差异。

当前最稳妥的落地路径是先在 `airgate-openai` 内部抽出任务注册表和 runner，保持现有接口不变并追加 `task_id`；然后补 SDK schema；最后让 Core 接管统一任务服务。通用 `/v1/tasks` 只作为管理和调试能力考虑，不作为业务客户端的新标准。
