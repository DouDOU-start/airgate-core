const BASE = '/api/v1/ext-user/airgate-playground';
const CORE_BASE = '/api/v1';

function authHeaders(): Record<string, string> {
  const headers: Record<string, string> = {};
  const token = localStorage.getItem('token');
  if (token) headers['Authorization'] = `Bearer ${token}`;
  return headers;
}

async function request<T>(method: string, path: string, body?: unknown, base = BASE): Promise<T> {
  const headers: Record<string, string> = { ...authHeaders() };
  if (body !== undefined) headers['Content-Type'] = 'application/json';

  const resp = await fetch(base + path, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  });

  if (!resp.ok) {
    const text = await resp.text();
    let msg = `HTTP ${resp.status}`;
    try {
      const j = JSON.parse(text);
      msg = j.error || j.message || msg;
    } catch { /* ignore */ }
    if (resp.status === 401) {
      localStorage.removeItem('token');
      window.location.href = '/login';
    }
    throw new Error(msg);
  }

  const text = await resp.text();
  return text ? JSON.parse(text) as T : null as unknown as T;
}

async function coreRequest<T>(method: string, path: string, body?: unknown): Promise<T> {
  const resp = await request<{ code: number; data: T; message: string }>(method, path, body, CORE_BASE);
  if (resp.code !== 0) {
    throw new Error(resp.message || 'request failed');
  }
  return resp.data;
}

// ── Types ──

export type ReasoningEffort = 'minimal' | 'low' | 'medium' | 'high' | 'xhigh';

export interface Conversation {
  id: number;
  user_id: number;
  title: string;
  group_id: number;
  platform: string;
  model: string;
  created_at: string;
  updated_at: string;
}

export interface Message {
  id: number;
  conversation_id: number;
  role: string;
  content: string;
  reasoning?: string;
  reasoning_effort?: ReasoningEffort;
  platform: string;
  model: string;
  group_id: number;
  input_tokens: number;
  output_tokens: number;
  cost: number;
  created_at: string;
}

export interface ModelInfo {
  id: string;
  name: string;
  platform?: string;
  input_price: number;
  output_price: number;
  context_window: number;
  max_output_tokens: number;
  image_only?: boolean;
  capabilities?: string[];
}

interface ProviderModelInfo {
  id?: string;
  ID?: string;
  name?: string;
  Name?: string;
  input_price?: number;
  InputPrice?: number;
  output_price?: number;
  OutputPrice?: number;
  context_window?: number;
  ContextWindow?: number;
  max_output_tokens?: number;
  MaxOutputTokens?: number;
  image_only?: boolean;
  ImageOnly?: boolean;
  capabilities?: string[];
  Capabilities?: string[];
}

interface ProviderModelListResponse {
  data?: ProviderModelInfo[];
}

export interface PlatformInfo {
  name: string;
  display_name: string;
}

interface ProviderPlatformInfo {
  name?: string;
  Name?: string;
  display_name?: string;
  DisplayName?: string;
}

export interface UserInfo {
  id: number;
  username: string;
  email: string;
  role: string;
  balance: number;
  status: string;
  api_key_id?: number;
  api_key_name?: string;
  api_key_platform?: string;
}

export interface PersistedMessageRequest {
  conversation_id: number;
  role: string;
  content: string;
  reasoning?: string;
  reasoning_effort?: ReasoningEffort;
  platform?: string;
  model?: string;
  group_id?: number;
  input_tokens?: number;
  output_tokens?: number;
  cost?: number;
}

export interface APIKeyItem {
  id: number;
  name: string;
  key?: string;
  key_prefix: string;
  group_id: number | null;
  status: string;
}

export interface GroupItem {
  id: number;
  name: string;
  platform: string;
}

export interface PagedResponse<T> {
  list: T[];
  total: number;
  page: number;
  page_size: number;
}

export type ChatMessageContent = string | Array<
  | { type: 'text'; text: string }
  | { type: 'image_url'; image_url: { url: string } }
>;

export interface ChatCompletionResponse {
  id?: string;
  model?: string;
  choices?: Array<{
    message?: {
      content?: string;
    };
  }>;
  usage?: {
    prompt_tokens?: number;
    completion_tokens?: number;
    input_tokens?: number;
    output_tokens?: number;
    cost?: number;
  };
}

export interface ChatCompletionCallbacks {
  onData: (text: string) => void;
  onReasoning: (text: string) => void;
  onDone: (usage: { input_tokens: number; output_tokens: number; model: string; cost: number }) => void | Promise<void>;
  onError: (err: string) => void;
}

export interface ImageEditResponse {
  created?: number;
  model?: string;
  data?: Array<{
    url?: string;
    b64_json?: string;
    revised_prompt?: string;
  }>;
  usage?: {
    prompt_tokens?: number;
    completion_tokens?: number;
    input_tokens?: number;
    output_tokens?: number;
    cost?: number;
  };
}

// ── API ──

export const api = {
  listConversations: () => request<Conversation[]>('GET', '/conversations'),

  createConversation: (data: { title?: string; group_id?: number; platform?: string; model?: string }) =>
    request<Conversation>('POST', '/conversations', data),

  getConversation: (id: number) => request<Conversation>('GET', `/conversations/${id}`),

  updateConversation: (id: number, data: { title?: string; group_id?: number; platform?: string; model?: string }) =>
    request<{ status: string }>('PUT', `/conversations/${id}`, data),

  deleteConversation: (id: number) =>
    request<{ status: string }>('DELETE', `/conversations/${id}`),

  listMessages: (convId: number) => request<Message[]>('GET', `/messages/${convId}`),

  persistMessage: (data: PersistedMessageRequest) => request<Message>('POST', '/messages', data),

  updateMessage: (id: number, data: { content: string; input_tokens?: number; output_tokens?: number; cost?: number }) =>
    request<Message>('PUT', `/messages/${id}`, data),

  listPlatforms: async () => {
    const payload = await request<ProviderPlatformInfo[]>('GET', '/platforms');
    return payload
      .map(item => {
        const name = item.name || item.Name || '';
        const displayName = item.display_name || item.DisplayName || name;
        return { name, display_name: displayName };
      })
      .filter(item => item.name);
  },

  listModels: async (platform: string) => {
    const payload = await request<ProviderModelListResponse | ProviderModelInfo[]>('GET', `/models?platform=${encodeURIComponent(platform)}`);
    const data = Array.isArray(payload) ? payload : payload.data || [];
    return data.map(item => {
      const id = item.id || item.ID || '';
      return {
        id,
        name: item.name || item.Name || id,
        platform,
        input_price: item.input_price ?? item.InputPrice ?? 0,
        output_price: item.output_price ?? item.OutputPrice ?? 0,
        context_window: item.context_window ?? item.ContextWindow ?? 0,
        max_output_tokens: item.max_output_tokens ?? item.MaxOutputTokens ?? 0,
        image_only: Boolean(item.image_only ?? item.ImageOnly),
        capabilities: item.capabilities || item.Capabilities || [],
      };
    }).filter(item => item.id);
  },

  getUserInfo: () => coreRequest<UserInfo>('GET', '/users/me'),
};

export async function chatCompletion(
  platform: string,
  body: {
    model: string;
    messages: Array<{ role: string; content: ChatMessageContent }>;
    stream?: false;
    response_format?: { type: 'json_object' };
    // size 透传给 OpenAI 网关的 chat→images 兼容路径
    // (airgate buildChatCompatImagePayload 直接 gjson 读这个字段)。
    // OpenAI 原生 Chat Completions 不识别此字段；非 image 模型上游会忽略。
    size?: string;
  },
  signal?: AbortSignal,
): Promise<ChatCompletionResponse> {
  const resp = await fetch(`${BASE}/chat/completions`, {
    method: 'POST',
    headers: {
      ...authHeaders(),
      'Content-Type': 'application/json',
      'Accept': 'application/json',
      'X-Airgate-Platform': platform,
    },
    body: JSON.stringify({ ...body, stream: false }),
    signal,
  });

  const text = await resp.text();
  let parsed: unknown = null;
  try {
    parsed = text ? JSON.parse(text) : null;
  } catch { /* ignore */ }

  if (!resp.ok) {
    if (resp.status === 401) {
      localStorage.removeItem('token');
      window.location.href = '/login';
    }
    const errorPayload = parsed as { error?: { message?: string } | string; message?: string } | null;
    const message = typeof errorPayload?.error === 'string'
      ? errorPayload.error
      : errorPayload?.error?.message || errorPayload?.message || `HTTP ${resp.status}`;
    throw new Error(message);
  }

  return (parsed || {}) as ChatCompletionResponse;
}

export async function editImage(platform: string, form: FormData, signal?: AbortSignal): Promise<ImageEditResponse> {
  const resp = await fetch(`${BASE}/images/edits`, {
    method: 'POST',
    headers: {
      ...authHeaders(),
      'Accept': 'application/json',
      'X-Airgate-Platform': platform,
    },
    body: form,
    signal,
  });

  const text = await resp.text();
  let parsed: unknown = null;
  try {
    parsed = text ? JSON.parse(text) : null;
  } catch { /* ignore */ }

  if (!resp.ok) {
    if (resp.status === 401) {
      localStorage.removeItem('token');
      window.location.href = '/login';
    }
    const errorPayload = parsed as { error?: { message?: string } | string; message?: string } | null;
    const message = typeof errorPayload?.error === 'string'
      ? errorPayload.error
      : errorPayload?.error?.message || errorPayload?.message || `HTTP ${resp.status}`;
    throw new Error(message);
  }

  return (parsed || {}) as ImageEditResponse;
}

export async function chatCompletionsStream(
  platform: string,
  body: {
    model: string;
    messages: Array<{ role: string; content: ChatMessageContent }>;
    stream: true;
    reasoning_effort?: ReasoningEffort;
    size?: string;
    n?: number;
    stream_options?: { include_usage?: boolean };
  },
  callbacks: ChatCompletionCallbacks,
  signal?: AbortSignal,
): Promise<void> {
  const requestBody = {
    ...body,
    stream_options: {
      include_usage: true,
      ...body.stream_options,
    },
  };

  const resp = await fetch(`${BASE}/chat/completions`, {
    method: 'POST',
    headers: {
      ...authHeaders(),
      'Content-Type': 'application/json',
      'Accept': 'text/event-stream',
      'X-Airgate-Platform': platform,
    },
    body: JSON.stringify(requestBody),
    signal,
  });

  if (!resp.ok || !resp.body) {
    const text = await resp.text();
    let msg = `HTTP ${resp.status}`;
    try {
      const parsed = JSON.parse(text);
      msg = parsed.error?.message || parsed.error || parsed.message || msg;
    } catch { /* ignore */ }
    callbacks.onError(msg);
    return;
  }

  const reader = resp.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let usage = { input_tokens: 0, output_tokens: 0, model: body.model, cost: 0 };

  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });

      const parts = buffer.split('\n');
      buffer = parts.pop() || '';

      for (const line of parts) {
        const trimmed = line.trim();
        if (!trimmed.startsWith('data: ')) continue;
        const payload = trimmed.slice(6);
        if (payload === '[DONE]') {
          await callbacks.onDone(usage);
          return;
        }
        try {
          const parsed = JSON.parse(payload);
          if (parsed.error) {
            callbacks.onError(parsed.error.message || parsed.error);
            return;
          }
          const choiceDelta = parsed.choices?.[0]?.delta;
          const reasoningDelta = choiceDelta?.reasoning_content;
          if (reasoningDelta) callbacks.onReasoning(reasoningDelta);
          const delta = choiceDelta?.content;
          if (delta) callbacks.onData(delta);
          if (parsed.usage) {
            usage = {
              input_tokens: parsed.usage.prompt_tokens || parsed.usage.input_tokens || 0,
              output_tokens: parsed.usage.completion_tokens || parsed.usage.output_tokens || 0,
              model: parsed.model || usage.model,
              cost: parsed.usage.cost || 0,
            };
          }
        } catch {
          // non-JSON SSE line, skip
        }
      }
    }
    await callbacks.onDone(usage);
  } catch (err) {
    if (signal?.aborted) return;
    callbacks.onError(err instanceof Error ? err.message : 'stream failed');
  }
}
