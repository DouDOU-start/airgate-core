import type { ApiResponse, SessionRole } from '../types';
import i18n from '../../i18n';

const BASE_URL = import.meta.env.VITE_API_BASE_URL || '';

// Token 管理
function readBrowserStorage(kind: 'localStorage' | 'sessionStorage', key: string): string | null {
  if (typeof window === 'undefined') return null;
  try {
    return window[kind].getItem(key);
  } catch {
    return null;
  }
}

function writeBrowserStorage(kind: 'localStorage' | 'sessionStorage', key: string, value: string | null) {
  if (typeof window === 'undefined') return;
  try {
    if (value == null) window[kind].removeItem(key);
    else window[kind].setItem(key, value);
  } catch {
    // Storage can be unavailable in private mode or locked-down browsers.
  }
}

let accessToken: string | null = readBrowserStorage('localStorage', 'token');

interface TokenClaims {
  role?: string;
  api_key_id?: number;
  exp?: number;
}

export function setToken(token: string | null) {
  accessToken = token;
  writeBrowserStorage('localStorage', 'token', token);
}

export function getToken(): string | null {
  return accessToken;
}

export function getTokenClaims(token = accessToken): TokenClaims | null {
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

// API Key 登录场景下用户输入的原文 Key，仅保留在 sessionStorage 内，
// 退出登录或关闭浏览器即清除。供 CCS 导入等需要原文 Key 的客户端功能使用。
const API_KEY_SECRET_STORAGE = 'apikey_session_secret';

export function setSessionAPIKey(key: string | null) {
  writeBrowserStorage('sessionStorage', API_KEY_SECRET_STORAGE, key);
}

export function getSessionAPIKey(): string | null {
  return readBrowserStorage('sessionStorage', API_KEY_SECRET_STORAGE);
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

// 执行 fetch 请求
function isAbortError(err: unknown): boolean {
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

export function post<T>(path: string, body?: unknown): Promise<T> {
  return request<T>('POST', path, body);
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

// 文件上传（multipart/form-data）
export async function upload<T>(path: string, formData: FormData): Promise<T> {
  const url = new URL(`${BASE_URL}${path}`, window.location.origin);

  const res = await doFetch(url.toString(), {
    method: 'POST',
    headers: buildHeaders(false),
    body: formData,
  });

  return handleResponse<T>(res);
}
