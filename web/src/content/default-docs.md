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
AirGate 已经兼容 openclaw 所需的全部协议，只需运行一行命令即可完成接入：

```bash
curl -fsSL https://your-airgate.example.com/openclaw/install.sh -o openclaw-install.sh && bash openclaw-install.sh
```

脚本会：

1. 提示你粘贴一把 AirGate 的 API Key
2. 拉取管理员预设的可选模型列表让你勾选
3. 自动生成 `~/.openclaw/openclaw.json`（旧配置会被备份）

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
