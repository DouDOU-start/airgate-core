// 站点"备用接入地址"（custom_endpoints 设置项）的解析/序列化
// 存储形式：JSON 字符串数组 [{name, endpoint, description}]，与 api_base_url 同属 site 分组设置

export interface CustomEndpoint {
  name: string;
  endpoint: string;
  description: string;
}

// 不做过滤，保留原始行（含空 endpoint），供设置页编辑态使用
export function parseCustomEndpoints(raw: string | undefined): CustomEndpoint[] {
  if (!raw) return [];
  try {
    const arr = JSON.parse(raw);
    if (!Array.isArray(arr)) return [];
    return arr
      .filter((item): item is Record<string, unknown> => !!item && typeof item === 'object')
      .map((item) => ({
        name: typeof item.name === 'string' ? item.name : '',
        endpoint: typeof item.endpoint === 'string' ? item.endpoint : '',
        description: typeof item.description === 'string' ? item.description : '',
      }));
  } catch {
    return [];
  }
}

// 保存时清洗：去空白、丢弃 endpoint 为空的行
export function serializeCustomEndpoints(items: CustomEndpoint[]): string {
  const cleaned = items
    .map((item) => ({
      name: item.name.trim(),
      endpoint: item.endpoint.trim(),
      description: item.description.trim(),
    }))
    .filter((item) => item.endpoint);
  return JSON.stringify(cleaned);
}

// 第三方测速站外链，对齐 sub2api 的展示型测速方案
export function speedTestUrl(endpoint: string): string {
  return `https://www.tcptest.cn/http/${encodeURIComponent(endpoint)}`;
}
