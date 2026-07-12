/**
 * effectiveDocUrl 返回「文档」入口应跳转的外部链接。
 *
 * 仅当管理员在 系统设置 → 站点品牌 中填写了 doc_url 时才存在文档入口，
 * 返回该外部链接；未配置时返回 null，调用方据此隐藏文档入口
 * （不再提供内置文档页）。
 */
export function effectiveDocUrl(docUrl: string | undefined | null): string | null {
  const trimmed = (docUrl ?? '').trim();
  return trimmed || null;
}
