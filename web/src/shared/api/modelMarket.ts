import { get } from './client';
import type { ModelMarketResp, PageReq } from '../types';

// 模型广场：未登录可见的模型价格 + 倍率区间公开接口。
export const modelMarketApi = {
  list: (params?: PageReq & { tag_id?: number }) =>
    get<ModelMarketResp>('/api/v1/model-market', params),
};
