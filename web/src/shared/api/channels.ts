import { get, post, put, del } from './client';
import type {
  ChannelResp, CreateChannelReq, UpdateChannelReq,
  TestChannelReq, TestChannelResp, FetchChannelModelsResp,
  BulkUpdateChannelsReq, BulkUpdateChannelsResp,
  ChannelListQuery, PagedData,
} from '../types';

export const channelsApi = {
  list: (params?: ChannelListQuery) =>
    get<PagedData<ChannelResp>>('/api/v1/admin/channels', params),
  create: (data: CreateChannelReq) => post<ChannelResp>('/api/v1/admin/channels', data),
  update: (id: number, data: UpdateChannelReq) => put<ChannelResp>(`/api/v1/admin/channels/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/channels/${id}`),
  test: (id: number, data?: TestChannelReq) => post<TestChannelResp>(`/api/v1/admin/channels/${id}/test`, data ?? {}),
  fetchModels: (id: number) => post<FetchChannelModelsResp>(`/api/v1/admin/channels/${id}/fetch-models`),
  bulkUpdate: (data: BulkUpdateChannelsReq) => post<BulkUpdateChannelsResp>('/api/v1/admin/channels/bulk-update', data),
};
