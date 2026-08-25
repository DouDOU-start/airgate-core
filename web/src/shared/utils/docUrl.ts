/**
 * effectiveDocUrl 解析"文档"按钮应该跳转到哪里。
 *
 * 优先级：
 *   1. 管理员在 系统设置 → 站点品牌 中显式填写了 doc_url，则跳到该外部链接（target=_blank）
 *   2. 否则回退到内置的 /docs 页面（同源 SPA 路由，AppShell 外，无需登录）
 *
 * 同时返回 isExternal 让调用方决定是否加 target="_blank"。
 */
export function effectiveDocUrl(docUrl: string | undefined | null): {
  href: string;
  isExternal: boolean;
} {
  const trimmed = (docUrl ?? '').trim();
  if (trimmed) {
    return { href: trimmed, isExternal: true };
  }
  return { href: '/docs', isExternal: false };
}
