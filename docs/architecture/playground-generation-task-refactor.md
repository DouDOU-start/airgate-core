# Playground 生成任务改造规划

> **本文为改造规划(尚未完成)。** Playground 现状见 [`current/plugins.md`](current/plugins.md)。

## 状态

草案。

本文用于指导 `airgate-playground`、`airgate-core`、`airgate-openai` 三个仓库中“对话、生图、未来视频/音乐生成任务”的改造。

目标不是给当前图片功能打补丁，而是把生成类长耗时能力统一到 Core 任务状态机里，让 Playground 对前端无差别支持同步上游、异步上游、可查询上游任务、不可查询上游任务。普通对话不进入任务状态机。

## 背景

当前 Playground 的对话和生图调用链混在一起，同一类“生成图片”动作至少存在三条路径：

1. 对话页选择 image model 后，前端仍调用 `/chat/completions`，再依赖 `airgate-openai` 的 chat compat 把请求转成 Images API。
2. Canvas / Studio 部分能力调用 `/image-tasks`，由 Playground 后端创建 Core Task。
3. 图片编辑和旧 Image Studio 仍直接调用 `/images/edits` 或 `/chat/completions`，绕过统一任务状态。

这导致几个结构性问题：

- 生图能力分散在前端、Playground 后端、Core host forward、OpenAI 插件里。
- chat compat 让“对话接口”承担了“生成图片接口”的职责，未来视频、音乐会继续复制这种混乱。
- 同一个 UI 动作可能因为入口不同而使用不同 group、不同账号、不同价格。
- 有些路径没有 AirGate task id，任务无法恢复、无法统一查询、无法统一展示。
- Playground 已经改用 Core Task，但旧的 `playground_image_tasks` 表和本地 worker 仍然残留。
- `image_size`、视频时长、音乐质量等类型参数不能进入 Core 固定字段，否则后续每加一种媒体类型都要改 Core 表结构。

## 核心决策

### 1. 本次改造不考虑向后兼容

本次改造按破坏性重构处理，目标是让代码边界干净，不保留旧路径的兼容分支。

明确不做：

- 不保留 `/image-tasks` 到 `/generation-tasks` 的长期别名。
- 不保留 Playground 本地 `playground_image_tasks` 表、CRUD、worker 和 Core Task 并存。
- 不保留 chat compat 生图作为降级路径。
- 不为旧前端调用入口写 adapter。
- 不在新数据结构里保留只服务历史实现的字段。
- 不为了旧任务历史查询保留业务代码。

如果确实需要迁移历史数据，应写一次性迁移脚本；迁移脚本执行后也不进入长期运行代码。

### 2. 删除 Playground 对 chat compat 的依赖

Playground 内部不再通过 `/chat/completions` 触发 image-only model。

选择图片、视频、音乐等生成模型时，前端统一创建生成任务；普通对话模型才走 `/chat/completions`。

`airgate-openai` 的 chat compat 可以删除。目标行为：

- `/v1/chat/completions` 收到 image-only model 时返回清晰错误。
- 文生图、图生图、局部重绘走 `/v1/images/generations` 或 `/v1/images/edits`。
- Playground 不使用 chat compat 作为任何内部主链路。

如果外部兼容客户端确实依赖“用 chat/completions 调图片模型”，需要单独决定是否保留兼容层；本次 Playground 改造不为这个兼容层留冗余代码。

### 3. 对话不进入任务状态机

普通对话、翻译、Claude 兼容转换这类请求继续走现有转发链路。

任务状态机只处理长耗时、可后台化、可恢复的生成类动作，例如：

- 图片生成
- 图片编辑
- 视频生成
- 音乐生成
- 后续语音、文件处理等类似任务

不要为了统一而把普通 chat completion 包成 task。

### 4. Core 固定字段保持通用

Core Task 只理解生命周期和安全边界：

- `plugin_id`
- `task_type`
- `user_id`
- `status`
- `stage`
- `progress`
- `input`
- `output`
- `attributes`
- `execution`
- `usage_id`
- 错误字段
- 时间字段

Core 不增加 `image_size`、`video_duration`、`music_quality`、`request_model`、`response_model` 这类字段。

生成类型、模型、扩展参数都放在 JSON 里：

- 用户请求原样放 `input`。
- 少量列表展示字段放 `attributes`。
- 上游任务 ID、轮询游标、上游状态放 `execution`。
- 实际用量和实际模型以 Usage 为准。

### 5. 使用记录只记录一个模型

使用记录只记录实际执行和计费的模型。

不设计 `chat_model`、`image_model`、`video_model`、`request_model`、`response_model` 这种分裂字段。

规则：

- 用户请求的模型保存在 `task.input.model`，用于复现。
- 插件或上游最终实际使用的模型写入 Usage 的 `model`。
- 翻译、工具调用、内部模型映射对用户透明，Usage 只记录实际使用模型。
- Playground 消息展示如果需要模型名，优先使用 Usage / task output 中的实际模型；没有完成前才展示请求模型。

### 6. AirGate task id 和上游 task id 分开

对前端和外部 API 返回的 `task_id` 必须是 AirGate 自己的任务 ID。

如果上游返回自己的任务 ID，必须持久化到 `task.execution`：

```json
{
  "upstream": {
    "task_id": "provider_task_123",
    "status": "queued",
    "poll_after_ms": 2000,
    "last_checked_at": "2026-05-13T10:00:00+08:00"
  }
}
```

这样 Core、插件或服务重启后仍然能恢复轮询，任务不会只存在内存里。

### 7. Playground 对前端只暴露统一生成任务

Playground 前端不再直接选择 `/chat/completions`、`/images/edits`、`/image-tasks`。

目标只保留一个生成任务客户端：

```text
POST /generation-tasks
GET  /generation-tasks/{task_id}
GET  /generation-tasks?conversation_id=...
```

旧 `/image-tasks` 不作为长期兼容入口保留。切换时直接更新前端所有调用点，避免留下两套任务 API。

## 当前代码问题定位

### Playground 前端

当前主要入口：

- `web/src/api.ts`
  - `chatCompletion`
  - `chatCompletionsStream`
  - `editImage`
  - `createImageTask`
  - `getImageTask`
  - `listImageTasks`
- `web/src/playground/PlaygroundContext.tsx`
  - 普通对话走 stream chat。
  - image model 走前端 planner + 多个 chat stream。
  - Canvas 走 image task。
  - 局部编辑直接走 images edits。
- `web/src/ImageStudioPage.tsx`
  - 无参考图走 chat compat。
  - 有参考图走 images edits。
- `web/src/playground/studio/StudioContext.tsx`
  - 大部分新 studio 流程走 image task。
- `web/src/playground/workflow/executors.ts`
  - image generate 走 task。
  - image edit 先直连 edits，失败后 fallback task。

问题：

- 生图任务散落在多个 React context 和页面里。
- 前端 planner 把生成编排放在 UI 层，后续视频、音乐会继续膨胀。
- 同步调用和任务调用混用，导致状态展示、取消、重试、余额刷新逻辑重复。

### Playground 后端

当前主要入口：

- `/chat/completions` 直接 host forward 到 `/v1/chat/completions`。
- `/images/edits` 直接 host forward 到 `/v1/images/edits`。
- `/image-tasks` 创建 Core Task。
- `ProcessCoreTask` 处理 Core 分发的 `image_generation`。
- 旧的 `playground_image_tasks` 表、本地 CRUD、本地 worker 仍存在。

问题：

- `/chat/completions` 和 `/images/edits` 都用 `GroupID: 0`，依赖 Core 自动路由。
- `/image-tasks` 会解析 conversation group，再把 `group_id` 放进 task input。
- 同一用户同一模型在不同入口可能走到不同 group。
- `executeChatImage` 仍然通过 `/v1/chat/completions` 调图片模型，依赖 openai chat compat。
- `executeInpaint` 走 `/v1/images/edits`，路径和 text2img/img2img 不统一。
- 旧本地 task 表已经不是主路径，应删除。

### Core

当前 Core Task 方向是对的：

- `Task` schema 已有 `input`、`output`、`attributes`、`execution`、`usage_id`。
- `task_service.go` 已经集中处理创建、更新、查询、状态迁移。
- `manager_tasks.go` 已经负责分发、重试、恢复卡住任务。

需要补强：

- `tasks.get` 必须校验 `plugin_id` 和 `user_id`，不能只按 task id 查。
- `tasks.list` 必须默认按调用插件隔离，避免不同插件相同 `task_type` 混在一起。
- `gateway.forward` 记录 Usage 后，应能把 `usage_id` 返回给任务处理器或直接关联到任务。
- Host forward 需要确保传给插件的 header 包含用户、分组、路径等必要上下文。

### OpenAI 插件

当前 OpenAI 插件有两套图片逻辑：

- `/v1/images/*` 可以创建 Core Task。
- `/v1/chat/completions` + image-only model 会走 chat compat，直接同步生成并包装成 chat completion。

问题：

- chat compat 绕开 OpenAI 插件自己的 task 化路径。
- Playground 通过 chat compat 生成图片时没有统一 task id。
- `images_chat_compat.go` 维护了额外请求解析、响应包装、SSE 包装逻辑，和真实 Images API 重复。

目标：

- 删除 chat compat 分支和相关测试。
- `/v1/chat/completions` 不再接受 image-only model。
- Images REST 继续作为图片生成的唯一 OpenAI 兼容入口。

## 目标架构

### 前端调用链

普通对话：

```text
Playground UI
  -> POST /chat/completions
  -> Playground 后端
  -> Core gateway.forward
  -> Gateway 插件
  -> 上游
```

生成任务：

```text
Playground UI
  -> POST /generation-tasks
  -> Playground 后端
  -> Core tasks.create
  -> Core task dispatcher
  -> Playground ProcessTask
  -> Core gateway.forward
  -> Gateway 插件
  -> 上游同步/异步执行
  -> Core tasks.update
  -> Playground UI 轮询 /generation-tasks/{task_id}
```

关键点：

- 前端不再知道上游同步还是异步。
- 前端不再直接调用 `/images/edits`。
- 前端不再通过 `/chat/completions` 调图片模型。
- 生成任务创建后立即得到 AirGate `task_id`。

### Playground 后端职责

Playground 后端负责产品语义：

- 创建 conversation。
- 持久化用户消息。
- 创建生成 task。
- 在 task 完成后持久化 assistant 消息。
- 把 task output 转成前端需要的展示结构。

Playground 后端不负责：

- Core task 状态机。
- 上游账号选择。
- 计费。
- OpenAI chat compat。
- 本地独立 task 表。

### Core 职责

Core 负责平台能力：

- 任务持久化。
- 状态迁移。
- 分发和重试。
- 用户和插件隔离。
- 资产存储。
- 路由、调度、计费。
- usage 记录和 task `usage_id` 关联。

Core 不理解：

- 图片参数。
- 视频参数。
- 音乐参数。
- provider 私有任务状态。
- Playground 消息格式。

### OpenAI 插件职责

OpenAI 插件负责 OpenAI 协议和上游适配：

- `/v1/chat/completions` 只处理对话。
- `/v1/images/generations` 处理文生图。
- `/v1/images/edits` 处理图生图、局部重绘。
- OAuth 场景下 Images REST 可以内部翻译为 Responses image tool。
- 如果上游返回任务 ID，写入 task `execution`，而不是把上游 ID 暴露成主 ID。

OpenAI 插件不负责：

- Playground 产品任务接口。
- Playground conversation/message 持久化。
- chat compat 生图。

## 生成任务数据契约

### CreateGenerationTaskRequest

Playground 新接口建议请求体：

```json
{
  "conversation_id": 123,
  "kind": "image",
  "operation": "generate",
  "platform": "openai",
  "model": "gpt-image-2",
  "prompt": "生成一张产品海报",
  "group_id": 10,
  "parameters": {
    "size": "1024x1024",
    "quality": "high",
    "background": "transparent"
  },
  "inputs": [
    {
      "type": "image",
      "role": "source",
      "url": "airgate://asset/..."
    }
  ],
  "mask": {
    "type": "image",
    "url": "airgate://asset/..."
  },
  "client_context": {
    "canvas_node_id": "node_123"
  }
}
```

字段规则：

- `kind`: 生成媒体类型，例如 `image`、`video`、`music`。
- `operation`: 动作，例如 `generate`、`edit`、`inpaint`、`extend`。
- `model`: 用户请求的模型，原样记录到 task input。
- `parameters`: 类型扩展参数，不进 Core 固定字段。
- `inputs`: 引用图、参考音频、首帧、尾帧等输入资产。
- `mask`: 局部编辑掩膜。
- `client_context`: 前端恢复 UI 所需的少量上下文，不参与执行。

### Core Task input

Playground 创建 Core Task 时写入：

```json
{
  "conversation_id": 123,
  "kind": "image",
  "operation": "generate",
  "platform": "openai",
  "model": "gpt-image-2",
  "prompt": "生成一张产品海报",
  "group_id": 10,
  "parameters": {
    "size": "1024x1024",
    "quality": "high"
  },
  "inputs": [],
  "mask": null,
  "client_context": {
    "canvas_node_id": "node_123"
  }
}
```

建议 `task_type` 使用一个通用值：

```text
generation
```

原因：

- Playground 插件是产品编排层，可以在 `input.kind` 和 `input.operation` 内部分发。
- Core 不需要知道图片、视频、音乐。
- 后续新增媒体类型不需要新增 Core 字段，也不需要复制一套 Playground task API。

如果某个 gateway/provider 插件需要单独声明更细的任务类型，可以使用自己的 `task_type`；但 Playground 这一层统一使用 `generation`。

### Core Task attributes

`attributes` 只放少量列表展示和粗筛选字段：

```json
{
  "kind": "image",
  "operation": "generate",
  "platform": "openai",
  "model": "gpt-image-2",
  "size": "1024x1024"
}
```

注意：

- `attributes.model` 是未完成任务展示值，不是最终使用记录事实。
- 完整参数仍以 `input.parameters` 为准。
- 不把大文本 prompt、大图 data URL 放进 attributes。

### Core Task execution

执行中由插件更新：

```json
{
  "strategy": "sync_blocking",
  "request": {
    "method": "POST",
    "path": "/v1/images/generations"
  },
  "upstream": {
    "task_id": "",
    "status": "",
    "poll_after_ms": 0
  }
}
```

异步上游示例：

```json
{
  "strategy": "async_polling",
  "request": {
    "method": "POST",
    "path": "/v1/videos/generations"
  },
  "upstream": {
    "task_id": "provider_video_123",
    "status": "processing",
    "poll_after_ms": 3000,
    "last_checked_at": "2026-05-13T10:20:00+08:00"
  }
}
```

### Core Task output

完成后写业务结果，不复制计费字段：

```json
{
  "content": "![generated image](https://...)",
  "assets": [
    {
      "type": "image",
      "url": "https://...",
      "mime_type": "image/png",
      "width": 1024,
      "height": 1024
    }
  ],
  "model": "gpt-image-2"
}
```

视频示例：

```json
{
  "content": "[generated video](https://...)",
  "assets": [
    {
      "type": "video",
      "url": "https://...",
      "mime_type": "video/mp4",
      "duration_seconds": 8
    }
  ],
  "model": "video-model-actual"
}
```

`input_tokens`、`output_tokens`、`cost` 不应继续作为 task output 的主数据来源。完成后应通过 `usage_id` 关联 Usage；前端如需展示费用摘要，由后端按 `usage_id` 查询并组装。

## 同步和异步上游处理

### 上游同步完成

流程：

1. Playground 创建 AirGate Task。
2. Core 分发给 Playground `ProcessTask`。
3. Playground 调用 `gateway.forward`。
4. 上游同步返回结果。
5. Core 记录 Usage。
6. Playground 保存生成物资产和 assistant message。
7. Playground 更新 task 为 `completed`，写入 `output` 和 `usage_id`。

前端行为：

- 创建任务后立即拿到 `task_id`。
- 轮询时很快看到 `completed`。
- 不需要知道上游是同步完成。

### 上游异步并支持查询

流程：

1. Playground 创建 AirGate Task。
2. 插件调用上游创建任务。
3. 上游返回 task id。
4. 插件把上游 task id 写入 `execution.upstream.task_id`。
5. task 保持 `processing`。
6. 后续分发或轮询继续根据 `execution` 查询上游状态。
7. 上游完成后写入 output、usage、assistant message。

要求：

- 上游 task id 必须持久化。
- 重启后不能依赖内存 map 恢复任务。
- 如果上游支持取消，取消时使用 `execution.upstream.task_id`。

### 上游异步但不支持查询

这种上游不能真正后台化。

可选策略：

- 在 task worker 中阻塞等待完成，再标记 `completed`。
- 如果等待时间不可控，则该 provider 应标记为“不支持后台任务”，前端创建任务时返回错误或降级为同步等待。

不允许：

- 只把不可恢复的内存状态当任务状态。
- 对外返回 task id 后，重启就丢任务。

## 消息持久化规则

创建生成任务时：

- 如果有 `conversation_id`，先保存用户消息。
- 用户消息只保存一次。
- task input 记录 `conversation_id` 和用户请求。

任务完成时：

- 保存 assistant 消息。
- assistant 消息的 `model` 使用实际模型。
- assistant 消息关联 `group_id`。
- token 和 cost 不从前端估算，以 Usage 或 task 关联结果为准。

失败时：

- task 标记 `failed`。
- 不保存 assistant 成功消息。
- 前端展示 task 错误。
- 可选保存一条系统/错误消息需要单独产品决策，不放在底层任务逻辑里。

## group 路由规则

生成任务创建时必须解析出确定的 `group_id`：

1. 请求显式传 `group_id`。
2. 否则从 conversation 读取 `group_id`。
3. 如果仍然没有 group，返回错误，不再用 `GroupID: 0` 自动选路。

任务执行时所有 `gateway.forward` 都使用 task input 中的 `group_id`。

这样可以保证：

- 用户在 UI 里选的分组就是实际执行分组。
- 任务重试不会换到另一个分组。
- 使用记录、消息、任务展示一致。

## 分阶段实施计划

### 阶段 1：Core 任务能力补强

改造点：

- `tasks.get` 增加 `plugin_id` 和 `user_id` 校验。
- `tasks.list` 默认按调用插件隔离，并支持按 `user_id`、`task_type`、`status` 查询。
- `gateway.forward` 记录 Usage 后返回 `usage_id`。
- `tasks.update` 支持同时更新 `execution`、`attributes`、`usage_id`。
- 确认 task 状态迁移不允许终态回退。

验收：

- Playground 插件不能读到 OpenAI 插件创建的同名任务。
- 普通用户不能通过 task id 查询其他用户任务。
- 完成任务能关联 usage。

### 阶段 2：Playground 后端统一生成任务接口

改造点：

- 新增 `POST /generation-tasks`。
- 新增 `GET /generation-tasks/{id}`。
- 新增 `GET /generation-tasks`。
- `TaskTypes()` 从只返回 `image_generation` 改为返回 `generation`。
- `ProcessCoreTask` 改为通用 generation dispatcher。
- 删除 `CreateImageTask`、`GetImageTask`、`ListImageTasks` 的本地表实现。
- 删除 `playground_image_tasks` 表创建逻辑。
- 删除 `ProcessPendingImageTasks` 本地 worker。

执行器：

- `kind=image, operation=generate` -> `/v1/images/generations`
- `kind=image, operation=edit` -> `/v1/images/edits`
- `kind=image, operation=inpaint` -> `/v1/images/edits`
- 未来 `kind=video`、`kind=music` 新增 executor，不改 Core task schema。

验收：

- Playground 后端不再通过 `/v1/chat/completions` 执行图片模型。
- Playground 生成任务只持久化在 Core task 表。
- 本地 `playground_image_tasks` 不再被创建或查询。

### 阶段 3：Playground 前端统一调用

改造点：

- `api.createImageTask/getImageTask/listImageTasks` 改为 `createGenerationTask/getGenerationTask/listGenerationTasks`。
- `sendMessage` 中 image model 分支不再调用 `streamAssistantResponse`。
- 图片模型发送统一创建 generation task。
- 删除前端 image planner 的 chat 调用路径；如果仍要多 shot，作为 generation task 的 `parameters.shots` 或后端编排能力。
- `submitImageEdit` 不再直接调用 `editImage`，改为 `operation=inpaint` 或 `operation=edit` task。
- `ImageStudioPage` 不再调用 `chatCompletion` 或 `editImage`。
- `workflow/executors.ts` 的 image edit 不再直连 edits。

验收：

- 前端搜索不到生图场景调用 `chatCompletion`。
- 前端搜索不到生图场景调用 `editImage`。
- 生成任务统一有 task id、状态、错误、结果。

### 阶段 4：OpenAI 插件删除 chat compat

改造点：

- 删除 `forwardChatCompletionsAsImages` 调用分支。
- 删除 `images_chat_compat.go` 和对应测试。
- `isChatCompatImageModel` 不再参与 `/chat/completions` 路由。
- `/v1/chat/completions` 遇到 image-only model 返回错误，例如“图片模型请使用 Images API”。
- 保留 `/v1/images/generations`、`/v1/images/edits` 的真实图片路径。

验收：

- `rg forwardChatCompletionsAsImages airgate-openai` 无结果。
- `rg images_chat_compat airgate-openai` 无业务代码结果。
- Playground 所有图片任务仍可通过 Images API 完成。

### 阶段 5：清理旧兼容和重复代码

清理项：

- Playground `/image-tasks` 路由。
- Playground `ImageTask` 只服务旧接口的字段。
- Playground 本地 image task SQL。
- Playground 本地 stale task recovery。
- Playground 前端 `ImageTask` 类型命名。
- OpenAI chat compat 请求解析、SSE 包装、chat 响应包装。
- 任何 `X-Airgate-Task-Execution` 的滥用，只保留必要的递归守卫。

验收：

- 不保留“旧入口继续可用”的双路径代码。
- 不保留本地任务表和 Core task 并存。
- 不保留 chat compat 生图。

## 测试计划

### Core

- `tasks.create` 创建任务。
- `tasks.update` 合法状态迁移。
- 终态任务不能回退。
- `tasks.get` 用户隔离。
- `tasks.list` 插件隔离。
- `gateway.forward` 返回 usage id。

### Playground 后端

- 创建 image generate task。
- 创建 image edit task。
- 创建 image inpaint task。
- 缺少 group_id 时从 conversation 解析。
- 无法解析 group_id 时失败。
- task 完成后保存 assistant message。
- task 失败时不保存成功消息。
- 不再访问 `playground_image_tasks`。

### Playground 前端

- 普通对话仍流式输出。
- 图片模型发送后立即出现任务状态。
- 图片生成完成后消息列表刷新。
- 局部重绘走任务状态。
- Studio / Canvas / workflow 都走统一 generation task。
- 停止、重试、错误展示不依赖 chat stream。

### OpenAI 插件

- `/v1/chat/completions` + 对话模型正常。
- `/v1/chat/completions` + image-only model 返回明确错误。
- `/v1/images/generations` 正常。
- `/v1/images/edits` 正常。
- OAuth Images REST -> Responses image tool 仍正常。

## 风险和处理

### 风险：一次性删除 chat compat 影响外部客户端

处理：

- 本文只要求 Playground 不再依赖 chat compat。
- 当前改造不为外部 chat compat 保留实现。
- 删除 chat compat 属于明确的 breaking change，应在 OpenAI 插件变更说明中写清楚。
- 如未来重新需要外部兼容，应作为新需求重新设计，不在本次重构中预留冗余分支。

### 风险：旧任务历史不可查

处理：

- 不保留长期兼容代码。
- 默认不迁移旧任务历史。
- 删除旧表创建逻辑和访问逻辑，长期代码不再读写 `playground_image_tasks`。
- 如果上线前明确需要迁移历史数据，只允许写一次性迁移脚本；脚本执行后删除，不进入运行时代码。

### 风险：不可查询上游任务无法恢复

处理：

- provider capability 必须声明是否支持可恢复异步任务。
- 不支持查询的上游只能同步阻塞执行，或拒绝后台任务。
- 不允许返回 task id 后只靠内存等待。

### 风险：任务 output 和 usage 重复

处理：

- output 只放业务结果和必要展示模型。
- 计费、tokens、实际模型以 Usage 为准。
- Playground API 可以组装 usage summary，但不要把它当 Core Task 固定字段。

## 代码清洁标准

本次实施完成后，代码应满足：

- 一个业务动作只有一条主链路，不保留旧链路 fallback。
- 旧 API、旧类型、旧 SQL、旧 worker 删除，不留空壳。
- 新接口命名使用 `generation`，不再把 Playground 产品接口命名绑定到 `image`。
- 不用注释解释“旧逻辑为什么保留”；旧逻辑应直接删除。
- 不新增只为历史兼容存在的字段、mapper、转换函数。
- 测试只覆盖新链路，不为旧行为写兼容断言。

## 完成标准

改造完成后应满足：

1. 普通对话只走 chat completion。
2. 图片生成、图片编辑、未来视频和音乐都走 generation task。
3. Playground 不再通过 chat compat 生成图片。
4. OpenAI chat compat 生图代码被删除，或至少不再被 Playground 引用。
5. Core Task 不增加任何图片/视频/音乐专属固定字段。
6. task id 是 AirGate task id，上游 task id 只存在 `execution`。
7. usage 只记录一个实际模型。
8. group_id 在任务创建时确定，执行和重试保持一致。
9. Playground 不再保留本地 image task 表和 worker。
10. 前端所有生成入口状态展示一致，失败和重试逻辑一致。
