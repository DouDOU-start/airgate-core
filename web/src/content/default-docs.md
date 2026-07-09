# 使用 AirGate

AirGate 是一个统一的 AI API 网关：把多家上游渠道统一调度、计费、限流，对外提供 OpenAI / Anthropic / Gemini **三种原生协议直连**——网关不做任何协议翻译，请求与响应原样透传，各家官方 SDK 的全部字段（工具调用、多模态、缓存等）都完整可用。

你可以把现有的 OpenAI SDK、Anthropic SDK、Google GenAI SDK、Codex CLI、Claude Code 等客户端工具直接指向 AirGate，无需改代码。

## 快速开始

1. **创建 API Key**：进入 **API 密钥** 页，点击「创建」即可。复制返回的 `sk-...`；如果之后忘了，在该页面随时点「查看」也能再次取出。
2. **API 基础地址**：`https://your-airgate.example.com`（OpenAI 客户端加 `/v1` 后缀）
3. **发请求**：把客户端的 `base_url` 指向上面的地址，密钥填 AirGate 的 `sk-` key（`Authorization: Bearer`、`x-api-key`、`x-goog-api-key` 三种头都支持）。

## API 概览

三种协议各走各的原生端点，客户端按自己的协议选端点即可：

| 方法 | 路径 | 协议 / 用途 |
| --- | --- | --- |
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions（最广泛使用的协议，绝大多数 OpenAI SDK / 第三方客户端走这条） |
| `POST` | `/v1/responses` | OpenAI Responses API（OpenAI 较新协议） |
| `POST` | `/v1/images/generations` | OpenAI 生图（gpt-image / DALL·E 系，JSON，详见下方「生图」） |
| `POST` | `/v1/images/edits` | OpenAI 图像编辑（multipart/form-data 原样透传） |
| `POST` | `/v1/messages` | Anthropic Messages（Claude Code 等 Anthropic 客户端走这条，原生直连） |
| `POST` | `/v1/messages/count_tokens` | Anthropic token 计数（免费端点，不计费） |
| `POST` | `/v1beta/models/{model}:generateContent` | Gemini 非流式（Google GenAI SDK / Gemini 客户端走这条，原生直连） |
| `POST` | `/v1beta/models/{model}:streamGenerateContent?alt=sse` | Gemini 流式（SSE） |
| `POST` | `/v1beta/models/{model}:predict` | Gemini Imagen 生图（按次计费，详见下方「生图」） |
| `POST` | `/v1beta/models/{model}:countTokens` | Gemini token 计数（免费端点，不计费） |
| `GET`  | `/v1/models` | 列出当前可用模型（全协议全量列出，OpenAI 格式） |
| `GET`  | `/v1beta/models` | 列出当前可用模型（Gemini 原生格式） |

> 端点只路由到**同协议**的上游渠道：某个模型能在哪个端点用，取决于管理员为它配置的渠道协议类型（OpenAI 兼容 / Anthropic / Gemini）。同一个模型名若配了多协议渠道，各端点各走各的，互不串台。

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

### Google GenAI Python SDK

```python
from google import genai

client = genai.Client(
    api_key="sk-你的key",
    http_options={"base_url": "https://your-airgate.example.com"},
)

resp = client.models.generate_content(
    model="gemini-2.5-flash",
    contents="你好",
)
print(resp.text)
```

## 常见问题

### Q: 调用接口提示 401 / 余额不足？

确认 Key 没有粘贴多余的空格、未过期、未停用，且账户余额足以覆盖调用成本。可在 **使用记录** 页查看明细。

### Q: 想用 Codex CLI / Claude Code / Cline 等工具？

它们通常允许自定义 `base_url` 和 `api_key`。把 base URL 指向 `https://<airgate>` 或 `https://<airgate>/v1`，密钥填 AirGate 的 API Key 即可。

### Q: 如何切换模型？

直接在请求体的 `model` 字段里写 AirGate 当前支持的模型 ID。可调用 `GET /v1/models` 拿到完整清单。

## 应用接入（OAuth 单点登录）

独立部署的应用（如对话、创作中心）可注册为 AirGate 的 OAuth 客户端，让用户用 AirGate 账号单点登录，并自动领取该用户的 API Key 调用 `/v1` 网关（消费直接计入用户余额）。

管理员在 **应用接入** 页注册应用（配置回调地址白名单、是否第一方免确认、导航入口），获得 `client_id` / `client_secret`（secret 仅创建时展示一次）。

接入流程（授权码 + PKCE，PKCE 强制 S256）：

```
1. 浏览器跳转   GET  {AirGate}/oauth/authorize?client_id&redirect_uri&state
                     &code_challenge=<base64url(sha256(verifier))>&code_challenge_method=S256
   → 授权通过后 302 回 {redirect_uri}?code=...&state=...

2. 应用后端     POST {AirGate}/oauth/token        (application/x-www-form-urlencoded)
                     grant_type=authorization_code&code&redirect_uri
                     &client_id&client_secret&code_verifier
   → {"access_token","token_type":"Bearer","expires_in":7200}

3. 应用后端     GET  {AirGate}/oauth/userinfo      (Authorization: Bearer <access_token>)
   → {"sub":"<用户ID>","name","email"}

4. 应用后端     POST {AirGate}/oauth/provision-key (Authorization: Bearer <access_token>)
   → {"api_key":"sk-...","key_hint","created"}
```

说明：

- `provision-key` 幂等：同一用户同一应用只有一把 key，重复调用返回同一把的完整明文；用户在密钥管理里删除后会自动重建，禁用则返回 403。
- 领到的 `sk-` key 与用户手动创建的 key 完全等价：调 `/v1/chat/completions` 等接口、按价目表计费扣用户余额、可在使用记录中按 key 查看消耗。
- key 只应保存在应用后端，切勿下发到浏览器。
- 令牌有效期 2 小时，过期后重走一次静默授权即可（已登录用户无感知）。

## 生图

与文本一样零翻译透传，按模型所在渠道的协议选端点：

- **OpenAI 协议渠道**：`POST /v1/images/generations`（JSON）与 `POST /v1/images/edits`（multipart/form-data，原样透传上游），适用 gpt-image / DALL·E 系模型。当前生图端点不支持 `stream: true`（会返回 400），后续版本放开。
- **Gemini 原生生图**：Gemini 系生图模型（如 gemini-2.5-flash-image）走 `generateContent` / `streamGenerateContent` 端点，与文本调用完全一致。
- **Imagen**：Imagen 系模型走 `POST /v1beta/models/{model}:predict`。

计费口径：模型价目表配置了按次价（`per_request_price` > 0）时，按 **按次单价 × 产出张数** 计费（张数以响应中实际返回的图片数为准，最少按 1 次计）；未配按次价时按上游返回的 token 用量计费（gpt-image 系上游会返回 token usage）。
