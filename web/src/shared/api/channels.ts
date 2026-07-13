import { get, post, put, del } from './client';
import type {
  ChannelResp, ChannelKeyResp, CreateChannelReq, UpdateChannelReq, ChannelKeyReq,
  TestChannelReq, TestChannelResp, FetchChannelModelsResp, FetchChannelModelsPreviewReq,
  RefreshChannelBalanceResp, BulkUpdateChannelsReq, BulkUpdateChannelsResp,
  ChannelListQuery, ChannelKeyListQuery, PagedData,
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
};
