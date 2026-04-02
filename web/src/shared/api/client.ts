import type { ApiResponse } from '../types';
import i18n from '../../i18n';

const BASE_URL = import.meta.env.VITE_API_BASE_URL || '';

// Token 管理
let accessToken: string | null = localStorage.getItem('token');

export function setToken(token: string | null) {
  accessToken = token;
  if (token) {
    localStorage.setItem('token', token);
  } else {
    localStorage.removeItem('token');
  }
}

export function getToken(): string | null {
  return accessToken;
}

// 查询参数类型
type QueryParams = Record<string, any>;

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
async function doFetch(url: string, init: RequestInit): Promise<Response> {
  try {
    return await fetch(url, init);
  } catch {
    throw new ApiError(-1, i18n.t('common.network_error'), 0);
  }
}

// 统一请求方法
async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  params?: QueryParams,
): Promise<T> {
  const url = new URL(`${BASE_URL}${path}`, window.location.origin);

  if (params) {
    Object.entries(params).forEach(([key, value]) => {
      if (value !== undefined && value !== null && value !== '') {
        url.searchParams.set(key, String(value));
      }
    });
  }

  const res = await doFetch(url.toString(), {
    method,
    headers: buildHeaders(true),
    body: body ? JSON.stringify(body) : undefined,
  });

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
export function get<T>(path: string, params?: QueryParams): Promise<T> {
  return request<T>('GET', path, undefined, params);
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
