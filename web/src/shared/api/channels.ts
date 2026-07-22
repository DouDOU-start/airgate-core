import { get, post, put, del, getToken } from './client';
import type {
  ChannelResp, ChannelKeyResp, CreateChannelReq, UpdateChannelReq, ChannelKeyReq,
  TestChannelReq, TestChannelResp, FetchChannelModelsResp, FetchChannelModelsPreviewReq,
  RefreshChannelBalanceResp, BulkUpdateChannelsReq, BulkUpdateChannelsResp,
  ChannelListQuery, ChannelKeyListQuery, PagedData, ImportChannelsResp,
} from '../types';

export const channelsApi = {
  list: (params?: ChannelListQuery) =>
    get<PagedData<ChannelResp>>('/api/v1/admin/channels', params),
  // 密钥视图：跨渠道平铺分页（priority/weight 等排序）
  listKeys: (params?: ChannelKeyListQuery) =>
    get<PagedData<ChannelKeyResp>>('/api/v1/admin/channels/keys', params),
  // 渠道只管 name / base_url
  create: (data: CreateChannelReq) => post<ChannelResp>('/api/v1/admin/channels', data),
  update: (id: number, data: UpdateChannelReq) => put<ChannelResp>(`/api/v1/admin/channels/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/channels/${id}`),
  // 在渠道下新增一把 key
  addKey: (channelId: number, data: ChannelKeyReq) =>
    post<ChannelKeyResp>(`/api/v1/admin/channels/${channelId}/keys`, data),
  // 更新一把 key（api_key 留空 = 保持原密钥）
  updateKey: (keyId: number, data: ChannelKeyReq) =>
    put<ChannelKeyResp>(`/api/v1/admin/channels/keys/${keyId}`, data),
  // 删除一把 key
  deleteKey: (keyId: number) => del<void>(`/api/v1/admin/channels/keys/${keyId}`),
  // 按 key 测试指定模型
  test: (keyId: number, data?: TestChannelReq, options?: { signal?: AbortSignal }) =>
    post<TestChannelResp>(`/api/v1/admin/channels/keys/${keyId}/test`, data ?? {}, options),
  // 余额按 key 刷新
  refreshBalance: (keyId: number, options?: { signal?: AbortSignal }) =>
    post<RefreshChannelBalanceResp>(`/api/v1/admin/channels/keys/${keyId}/balance`, {}, options),
  // 按 key 拉取上游模型列表
  fetchModels: (keyId: number) => post<FetchChannelModelsResp>(`/api/v1/admin/channels/keys/${keyId}/fetch-models`),
  // 未保存前的模型预览（临时凭据）
  fetchModelsPreview: (data: FetchChannelModelsPreviewReq) =>
    post<FetchChannelModelsResp>('/api/v1/admin/channels/fetch-models', data),
  bulkUpdate: (data: BulkUpdateChannelsReq) => post<BulkUpdateChannelsResp>('/api/v1/admin/channels/bulk-update', data),
  export: async (params?: Pick<ChannelListQuery, 'keyword' | 'type' | 'status'>) => {
    const BASE_URL = import.meta.env.VITE_API_BASE_URL || '';
    const url = new URL(`${BASE_URL}/api/v1/admin/channels/export`, window.location.origin);
    if (params) {
      Object.entries(params).forEach(([k, v]) => {
        if (v) url.searchParams.set(k, String(v));
      });
    }
    const headers: Record<string, string> = {};
    const token = getToken();
    if (token) headers['Authorization'] = `Bearer ${token}`;
    const res = await fetch(url.toString(), { headers });
    if (!res.ok) throw new Error(`Export failed: ${res.status}`);
    const blob = await res.blob();
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    const disposition = res.headers.get('Content-Disposition');
    a.download = disposition?.match(/filename="(.+)"/)?.[1] ?? 'channels.json';
    a.click();
    URL.revokeObjectURL(a.href);
  },
  import: (data: unknown[]) => post<ImportChannelsResp>('/api/v1/admin/channels/import', data),
};
