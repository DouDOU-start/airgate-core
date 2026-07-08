import { get, post, put, del } from './client';
import type {
  ModelPriceResp, CreateModelPriceReq, UpdateModelPriceReq,
  ImportModelPricesReq, ImportModelPricesResp,
  PageReq, PagedData,
} from '../types';

export const modelPricesApi = {
  list: (params?: PageReq) =>
    get<PagedData<ModelPriceResp>>('/api/v1/admin/model-prices', params),
  create: (data: CreateModelPriceReq) => post<ModelPriceResp>('/api/v1/admin/model-prices', data),
  update: (id: number, data: UpdateModelPriceReq) => put<ModelPriceResp>(`/api/v1/admin/model-prices/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/model-prices/${id}`),
  import: (data: ImportModelPricesReq) => post<ImportModelPricesResp>('/api/v1/admin/model-prices/import', data),
};
