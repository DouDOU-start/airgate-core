export interface KeyForm {
  name: string;
  group_id: string;
  quota_usd: string;
  /** 销售倍率（reseller markup）。空字符串或 "0" 表示按平台原价计费 */
  sell_rate: string;
  /** 最高计费倍率。空字符串或 "0" 表示不限制；实际扣费倍率超过时请求被拒 */
  max_rate: string;
  /** API Key 级并发上限。空字符串或 "0" 表示不限制 */
  max_concurrency: string;
  expires_at: string;
}

export const emptyForm: KeyForm = {
  name: '',
  group_id: '',
  quota_usd: '',
  sell_rate: '',
  max_rate: '',
  max_concurrency: '',
  expires_at: '',
};
