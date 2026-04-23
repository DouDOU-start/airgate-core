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
  platform: string;
  model: string;
  group_id: number;
  input_tokens: number;
  output_tokens: number;
  cost: number;
  created_at: string;
}

export interface Platform {
  Name: string;
  DisplayName: string;
}

export interface ModelInfo {
  id: string;
  name: string;
  input_price: number;
  output_price: number;
  context_window: number;
  max_output_tokens: number;
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

export interface ChatCompletionCallbacks {
  onData: (text: string) => void;
  onDone: (usage: { input_tokens: number; output_tokens: number; model: string; cost: number }) => void;
  onError: (err: string) => void;
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

  listPlatforms: () => request<Platform[]>('GET', '/platforms'),

  listModels: (platform: string) => request<ModelInfo[]>('GET', `/models?platform=${encodeURIComponent(platform)}`),

  getUserInfo: () => coreRequest<UserInfo>('GET', '/users/me'),

  listAPIKeys: () => coreRequest<PagedResponse<APIKeyItem>>('GET', '/api-keys?page=1&page_size=100'),

  revealAPIKey: (id: number) => coreRequest<APIKeyItem>('GET', `/api-keys/${id}/reveal`),

  listGroups: () => coreRequest<PagedResponse<GroupItem>>('GET', '/groups?page=1&page_size=100'),
};

export async function chatCompletionsStream(
  apiKey: string,
  body: {
    model: string;
    messages: Array<{ role: string; content: string }>;
    stream: true;
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

  const resp = await fetch('/v1/chat/completions', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Accept': 'text/event-stream',
      'Authorization': `Bearer ${apiKey}`,
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
          callbacks.onDone(usage);
          return;
        }
        try {
          const parsed = JSON.parse(payload);
          if (parsed.error) {
            callbacks.onError(parsed.error.message || parsed.error);
            return;
          }
          const delta = parsed.choices?.[0]?.delta?.content;
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
    callbacks.onDone(usage);
  } catch (err) {
    if (signal?.aborted) return;
    callbacks.onError(err instanceof Error ? err.message : 'stream failed');
  }
}
