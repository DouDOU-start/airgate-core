/** 将多行 IP 文本解析为数组，空内容返回 undefined */
export function parseIpList(text: string): string[] | undefined {
  const trimmed = text.trim();
  if (!trimmed) return undefined;
  return trimmed.split('\n').map((s) => s.trim()).filter(Boolean);
}

/** 将 IP 数组格式化为多行文本 */
export function formatIpList(ips?: string[]): string {
  return ips?.join('\n') ?? '';
}
