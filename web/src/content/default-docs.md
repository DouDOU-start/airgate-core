# 使用 AirGate

AirGate 是一个统一的 AI API 网关：把 OpenAI API Key 与 ChatGPT OAuth 等上游账号统一调度、计费、限流，并对外暴露 OpenAI 兼容协议（Chat Completions / Responses）以及 Anthropic Messages 协议翻译。

你可以把现有的 OpenAI SDK、Anthropic SDK、Codex CLI、Claude Code、openclaw 等客户端工具直接指向 AirGate，无需改代码。

> Roadmap：即将支持 Claude（Anthropic）原生上游账号接入，届时 `/v1/messages` 路由会自动优先走原生上游而非协议翻译。

## 快速开始

1. **创建 API Key**：进入 **API 密钥** 页，点击「创建」即可。复制返回的 `sk-...`；如果之后忘了，在该页面随时点「查看」也能再次取出。
2. **API 基础地址**：`https://your-airgate.example.com/v1`
3. **发请求**：把客户端的 `base_url` 指向上面的地址，`Authorization` 头设为 `Bearer sk-你的key`。

## API 概览

AirGate 对外暴露 OpenAI 兼容协议，并通过协议翻译同时兼容 Anthropic Messages，常用路由：

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions（最广泛使用的协议，绝大多数 OpenAI SDK / 第三方客户端走这条） |
| `POST` | `/v1/responses` | OpenAI Responses API（OpenAI 较新协议） |
| `POST` | `/v1/images/generations` | OpenAI Images API（文生图，支持 `gpt-image-1.5` / `gpt-image-2`） |
| `POST` | `/v1/images/edits` | OpenAI Images API（图生图，支持 `gpt-image-1.5` / `gpt-image-2`） |
| `GET`  | `/v1/images/tasks` | 查询异步生图任务状态（配合请求头 `Prefer: respond-async` 使用，详见下文「异步任务模式」） |
| `POST` | `/v1/messages` | Anthropic Messages（Claude Code 等 Anthropic 客户端走这条；当前为协议翻译，未来对接原生 Claude 上游后将自动切换） |
| `GET`  | `/v1/models` | 列出当前可用模型 |

> 不带 `/v1` 前缀的别名路由也都可用，方便有些工具习惯把 base URL 直接写到根域名。

### curl 示例

```bash
curl https://your-airgate.example.com/v1/chat/completions \
  -H "Authorization: Bearer sk-你的key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.4",
    "messages": [
      {"role": "user", "content": "你好"}
    ]
  }'
```

## 用 SDK 接入

### OpenAI Python SDK

```python
from openai import OpenAI

client = OpenAI(
    base_url="https://your-airgate.example.com/v1",
    api_key="sk-你的key",
)

resp = client.chat.completions.create(
    model="gpt-5.4",
    messages=[{"role": "user", "content": "你好"}],
)
print(resp.choices[0].message.content)
```

### OpenAI Images SDK（文生图）

```python
import base64
from openai import OpenAI

client = OpenAI(
    base_url="https://your-airgate.example.com/v1",
    api_key="sk-你的key",
)

resp = client.images.generate(
    model="gpt-image-2",            # gpt-image-1.5 | gpt-image-2
    prompt="一只可爱的柴犬坐在樱花树下，日系水彩风格",
    size="2048x2048",               # gpt-image-2 支持任意合规 WIDTHxHEIGHT，或 auto
    quality="medium",               # low | medium | high | auto
    background="opaque",            # opaque | transparent
    output_format="png",            # png | jpeg | webp
    n=1,
    extra_body={"stream": True},    # 可选：上游耗时较长时，AirGate 会在等待期间通过 SSE 发送 keepalive ping 防止客户端/网关超时；响应体仍是标准 ImagesResponse
)

img = resp.data[0]
with open("out.png", "wb") as f:
    f.write(base64.b64decode(img.b64_json))
```

### OpenAI Images SDK（图生图）

```python
with open("in.png", "rb") as f:
    resp = client.images.edit(
        model="gpt-image-2",        # gpt-image-1.5 | gpt-image-2
        image=f,                    # 也可传 [f1, f2] 列表传多张参考图
        prompt="把这张图变成梵高星空风格的油画",
        size="1536x1024",
        quality="medium",
        background="opaque",
        output_format="png",
        n=1,
        extra_body={"stream": True},  # 可选：等同上面，SSE keepalive ping 防超时，响应体仍是 ImagesResponse
    )

img = resp.data[0]
with open("out.png", "wb") as f:
    f.write(base64.b64decode(img.b64_json))
```

### 生图：异步任务模式（`Prefer: respond-async`）

上面的 `stream: True` 只是在同步等待时发心跳防超时，**响应仍是阻塞等到图片生成完才返回**。如果你想立即拿到一个 `task_id` 后台轮询、不占用一个长连接（适合移动端、Serverless、批量任务场景），给请求加 `Prefer: respond-async` HTTP header 即可。

服务端行为：

- 立即返回 `202 Accepted`，响应体包含 `task_id` 和 `status_url`
- 响应头 `Preference-Applied: respond-async` 表示已切到异步模式
- 响应头 `Location` 指向任务查询地址

> 注意：异步模式的响应体不是标准 `ImagesResponse`，OpenAI 官方 SDK 的类型化解析（`client.images.generate(...)`）无法直接套用。推荐用 `httpx` / `requests` 等通用 HTTP 客户端，或调用 SDK 的 raw response 接口拿原始 JSON。

Python（用 `httpx` 直发）：

```python
import time
import httpx

BASE = "https://your-airgate.example.com/v1"
AUTH = {"Authorization": "Bearer sk-你的key"}

# 1. 提交任务，立即拿到 task_id
resp = httpx.post(
    f"{BASE}/images/generations",
    headers={**AUTH, "Prefer": "respond-async"},
    json={
        "model": "gpt-image-2",
        "prompt": "一只可爱的柴犬坐在樱花树下",
        "size": "2048x2048",
    },
    timeout=30,
)
resp.raise_for_status()                          # 期望 202 Accepted
task_id = resp.json()["task_id"]                 # AirGate task id

# 2. 轮询任务状态
while True:
    status = httpx.get(
        f"{BASE}/images/tasks",
        params={"task_id": task_id},
        headers=AUTH,
        timeout=10,
    ).json()
    if status["status"] in ("completed", "failed"):
        break
    time.sleep(2)

# 3. 任务完成后：
#    - status["result_content"] 是 Markdown 形式的图片引用，URL 为相对路径
#      如 "![image](/assets-runtime/...)"，需拼上 base URL 才能直接访问
#    - status["model"] / "input_tokens" / "output_tokens" / "cost" 提供计费摘要
#    - 失败时 status["error"] 带原因
print(status)
```

curl 等价示例：

```bash
# 提交任务
curl -i https://your-airgate.example.com/v1/images/generations \
  -H "Authorization: Bearer sk-你的key" \
  -H "Content-Type: application/json" \
  -H "Prefer: respond-async" \
  -d '{
    "model": "gpt-image-2",
    "prompt": "一只可爱的柴犬坐在樱花树下",
    "size": "2048x2048"
  }'
# → HTTP/1.1 202 Accepted
#   Preference-Applied: respond-async
#   Location: /v1/images/tasks?task_id=01933e4f-89a0-7c1e-8b3f-d4a92a1f00aa
#   {
#     "object": "image.task",
#     "task_id": "01933e4f-89a0-7c1e-8b3f-d4a92a1f00aa",
#     "status": "pending",
#     "status_url": "/v1/images/tasks?task_id=01933e4f-89a0-7c1e-8b3f-d4a92a1f00aa"
#   }

# 轮询任务
curl "https://your-airgate.example.com/v1/images/tasks?task_id=01933e4f-89a0-7c1e-8b3f-d4a92a1f00aa" \
  -H "Authorization: Bearer sk-你的key"
# → {
#     "task_id": "01933e4f-89a0-7c1e-8b3f-d4a92a1f00aa",
#     "status": "completed",
#     "progress": 100,
#     "result_content": "![image](/assets-runtime/...)",
#     "model": "gpt-image-2",
#     "input_tokens": 12,
#     "output_tokens": 0,
#     "cost": 0.012
#   }
```

任务状态字段：`pending` / `processing` / `completed` / `failed`。失败时 `error` 字段带上原因。

`/v1/images/edits` 完全相同，只是请求体多 `image` / `mask` 字段。

### Anthropic Python SDK

```python
from anthropic import Anthropic

client = Anthropic(
    base_url="https://your-airgate.example.com",
    api_key="sk-你的key",
)

resp = client.messages.create(
    model="claude-sonnet-4-6",
    max_tokens=1024,
    messages=[{"role": "user", "content": "你好"}],
)
print(resp.content[0].text)
```

## 一键接入 openclaw

[openclaw](https://github.com/openclaw/openclaw) 是一款可以运行在本机的个人 AI 助理，可同时桥接 WhatsApp、Telegram、Slack、Discord 等十几种聊天平台。
AirGate 已经兼容 openclaw 所需的全部协议，只需运行一行命令即可完成接入。

**Linux / macOS**（终端）：

```bash
curl -fsSL https://your-airgate.example.com/openclaw/install.sh -o openclaw-install.sh && bash openclaw-install.sh
```

**Windows**（PowerShell 5 或更高版本）：

```powershell
iwr -useb https://your-airgate.example.com/openclaw/install.ps1 | iex
```

脚本会：

1. 提示你粘贴一把 AirGate 的 API Key
2. 拉取管理员预设的可选模型列表让你勾选
3. 自动生成 `~/.openclaw/openclaw.json`（Windows 为 `%USERPROFILE%\.openclaw\openclaw.json`，旧配置会被备份）

完成后启动 openclaw 即可：

```bash
openclaw gateway
```

## 常见问题

### Q: 调用接口提示 401 / 余额不足？

确认 Key 没有粘贴多余的空格、未过期、未停用，且账户余额足以覆盖调用成本。可在 **使用记录** 页查看明细。

### Q: 想用 Codex CLI / Claude Code / Cline 等工具？

它们通常允许自定义 `base_url` 和 `api_key`。把 base URL 指向 `https://<airgate>` 或 `https://<airgate>/v1`，密钥填 AirGate 的 API Key 即可。

### Q: 如何切换模型？

直接在请求体的 `model` 字段里写 AirGate 当前支持的模型 ID。可调用 `GET /v1/models` 拿到完整清单。

### Q: 生图接口支持哪些模型和参数？

支持 `gpt-image-1.5` / `gpt-image-2`。

| 模型 | 文生图 | 图生图 |
| --- | --- | --- |
| `gpt-image-1.5` | ✅ | ✅ |
| `gpt-image-2` | ✅ | ✅ |

参数：

- `size`：`auto` 或 `WIDTHxHEIGHT`。`gpt-image-2` 要求宽高均为 16 的倍数、单边不超过 3840、长短边比例不超过 3:1、总像素在 `655360` 到 `8294400` 之间；常用值如 `1024x1024`、`1536x1024`、`1024x1536`、`2048x2048`、`3840x2160`。
- `quality`：`low`、`medium`、`high`、`auto`
- `n`：OAuth 模式目前仅支持 `1`；API Key 直通模式按上游能力处理。
- `background`：`opaque` / `transparent`
- `output_format`：`png` / `jpeg` / `webp`
- `input_fidelity`：仅图生图可用，`gpt-image-1` / `gpt-image-1.5` 可传 `low` / `high`；`gpt-image-2` 默认高保真处理参考图，无需传。

响应使用标准 OpenAI Images API schema（`data[].b64_json` + `usage`），官方 SDK 能直接解析。
