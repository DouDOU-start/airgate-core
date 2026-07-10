import type { ApiResponse, SessionRole } from '../types';
import i18n from '../../i18n';

const BASE_URL = import.meta.env.VITE_API_BASE_URL || '';

// 旧版本曾把 API Key 登录的明文 Key 写入 sessionStorage（key: apikey_session_secret），
// 现已改为仅存内存变量；模块加载时清理历史残留，避免明文密钥继续留在浏览器存储中。
try {
  if (typeof window !== 'undefined') {
    window.sessionStorage.removeItem('apikey_session_secret');
  }
} catch {
  // 隐私模式或受限浏览器下 Storage 可能不可用，静默忽略。
}

// Token 管理：特化为 localStorage 专用函数（不接受 storage 种类参数），
// 防止后人顺手把敏感值写回 sessionStorage。
function readLocalStorage(key: string): string | null {
  if (typeof window === 'undefined') return null;
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

function writeLocalStorage(key: string, value: string | null) {
  if (typeof window === 'undefined') return;
  try {
    if (value == null) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, value);
  } catch {
    // 隐私模式或受限浏览器下 Storage 可能不可用，静默忽略。
  }
}

let accessToken: string | null = readLocalStorage('token');

interface TokenClaims {
  role?: string;
  api_key_id?: number;
  exp?: number;
}

export function setToken(token: string | null) {
  accessToken = token;
  writeLocalStorage('token', token);
}

export function getToken(): string | null {
  return accessToken;
}

function getTokenClaims(token = accessToken): TokenClaims | null {
  if (!token) return null;

  const payload = token.split('.')[1];
  if (!payload) return null;

  try {
    const base64 = payload.replace(/-/g, '+').replace(/_/g, '/');
    const padded = base64.padEnd(Math.ceil(base64.length / 4) * 4, '=');
    const json = new TextDecoder().decode(
      Uint8Array.from(atob(padded), (char) => char.charCodeAt(0)),
    );
    return JSON.parse(json) as TokenClaims;
  } catch {
    return null;
  }
}

export function getTokenRole(token = accessToken): SessionRole | null {
  const role = getTokenClaims(token)?.role;
  return role === 'admin' || role === 'user' || role === 'api_key' ? role : null;
}

export function getTokenAPIKeyID(token = accessToken): number | null {
  const id = getTokenClaims(token)?.api_key_id;
  return typeof id === 'number' && id > 0 ? id : null;
}

// API Key 登录场景下用户输入的原文 Key，仅保留在模块级内存变量中（不落任何
// Web Storage，避免明文密钥被持久化）。页面刷新后丢失，属可接受降级；
// 供 CCS 导入等需要原文 Key 的客户端功能使用。
let sessionAPIKeySecret: string | null = null;

export function setSessionAPIKey(key: string | null) {
  sessionAPIKeySecret = key;
}

export function getSessionAPIKey(): string | null {
  return sessionAPIKeySecret;
}

// 查询参数类型
type QueryParams = Record<string, any>;
type RequestOptions = {
  signal?: AbortSignal;
};

// 当前浏览器时区（IANA 名，例如 "Asia/Shanghai"、"America/New_York"）。
// 自动附加到 GET 请求，保证后端按用户本地时区计算"今天 / 7 天"等边界。
function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
  } catch {
    return '';
  }
}

// 构建请求头
function buildHeaders(includeContentType: boolean): Record<string, string> {
  const headers: Record<string, string> = {};
  if (includeContentType) {
    headers['Content-Type'] = 'application/json';
  }
  if (accessToken) {
    headers['Authorization'] = `Bearer ${accessToken}`;
  }
  return headers;
}

// Token 自动刷新：在过期前 30 分钟内首次请求时静默续期。
let refreshPromise: Promise<boolean> | null = null;

function tokenExpiresWithin(seconds: number): boolean {
  const claims = getTokenClaims();
  if (!claims?.exp) return false;
  return claims.exp - Date.now() / 1000 < seconds;
}

async function tryRefreshToken(): Promise<boolean> {
  if (!accessToken) return false;
  if (refreshPromise) return refreshPromise;

  refreshPromise = (async () => {
    try {
      const res = await fetch(`${BASE_URL}/api/v1/auth/refresh`, {
        method: 'POST',
        headers: { 'Authorization': `Bearer ${accessToken}`, 'Content-Type': 'application/json' },
      });
      if (!res.ok) return false;
      const json = await res.json() as ApiResponse<{ token: string }>;
      if (json.code !== 0 || !json.data?.token) return false;
      setToken(json.data.token);
      return true;
    } catch {
      return false;
    } finally {
      refreshPromise = null;
    }
  })();

  return refreshPromise;
}

// 统一响应处理
async function handleResponse<T>(res: Response): Promise<T> {
  let json: ApiResponse<T>;
  try {
    json = await res.json();
  } catch {
    throw new ApiError(-1, i18n.t('common.server_error', { status: res.status }), res.status);
  }

  if (json.code !== 0) {
    if (res.status === 401 && accessToken) {
      setToken(null);
      window.location.href = '/login';
    }
    throw new ApiError(json.code, json.message, res.status);
  }

  return json.data;
}

// 判定请求是否被 AbortController 主动取消（原样抛出，调用方据此区分用户取消与真实失败）
export function isAbortError(err: unknown): boolean {
  return typeof err === 'object'
    && err !== null
    && 'name' in err
    && (err as { name?: unknown }).name === 'AbortError';
}

async function doFetch(url: string, init: RequestInit): Promise<Response> {
  try {
    return await fetch(url, init);
  } catch (err) {
    if (isAbortError(err)) {
      throw err;
    }
    throw new ApiError(-1, i18n.t('common.network_error'), 0);
  }
}

// 统一请求方法
async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  params?: QueryParams,
  options?: RequestOptions,
): Promise<T> {
  // 过期前 30 分钟自动刷新
  if (accessToken && tokenExpiresWithin(1800)) {
    await tryRefreshToken();
  }

  const url = new URL(`${BASE_URL}${path}`, window.location.origin);

  if (params) {
    Object.entries(params).forEach(([key, value]) => {
      if (value !== undefined && value !== null && value !== '') {
        url.searchParams.set(key, String(value));
      }
    });
  }

  // 给 GET 请求自动附加浏览器时区，后端用它计算"今天 / 7 天"等边界以及解析
  // YYYY-MM-DD 形式的 start_date / end_date。调用方显式提供的 tz 不会被覆盖。
  if (method === 'GET' && !url.searchParams.has('tz')) {
    const tz = browserTimezone();
    if (tz) {
      url.searchParams.set('tz', tz);
    }
  }

  const res = await doFetch(url.toString(), {
    method,
    headers: buildHeaders(true),
    body: body ? JSON.stringify(body) : undefined,
    signal: options?.signal,
  });

  // 401 时尝试刷新 token 并重试一次
  if (res.status === 401 && accessToken) {
    const refreshed = await tryRefreshToken();
    if (refreshed) {
      const retryRes = await doFetch(url.toString(), {
        method,
        headers: buildHeaders(true),
        body: body ? JSON.stringify(body) : undefined,
        signal: options?.signal,
      });
      return handleResponse<T>(retryRes);
    }
  }

  return handleResponse<T>(res);
}

// API 错误类
export class ApiError extends Error {
  constructor(
    public code: number,
    message: string,
    public httpStatus: number,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

// 导出快捷方法
export function get<T>(path: string, params?: QueryParams, options?: RequestOptions): Promise<T> {
  return request<T>('GET', path, undefined, params, options);
}

export function post<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T> {
  return request<T>('POST', path, body, undefined, options);
}

export function put<T>(path: string, body?: unknown): Promise<T> {
  return request<T>('PUT', path, body);
}

export function del<T>(path: string): Promise<T> {
  return request<T>('DELETE', path);
}

export function patch<T>(path: string, body?: unknown): Promise<T> {
  return request<T>('PATCH', path, body);
}

