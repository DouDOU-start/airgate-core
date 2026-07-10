import i18n from '../../i18n';

// 日期本地化：locale 跟随当前界面语言（zh → zh-CN，其余 → en-US），
// 避免在英文界面下仍按中文格式渲染日期。
function currentLocale(): string {
  return i18n.language?.startsWith('zh') ? 'zh-CN' : 'en-US';
}

/** 格式化日期时间 (yyyy/M/d HH:mm) */
export function formatDateTime(date: string | number | Date): string {
  return new Date(date).toLocaleString(currentLocale(), {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
}

/** 格式化日期 (yyyy/M/d) */
export function formatDate(date: string | number | Date): string {
  return new Date(date).toLocaleDateString(currentLocale());
}

/** 格式化时间 (HH:mm:ss，24 小时制) */
export function formatTime(date: string | number | Date): string {
  return new Date(date).toLocaleTimeString(currentLocale(), { hour12: false });
}

/**
 * 把 YYYY-MM-DD 转成本地时区当日 23:59:59 的 RFC3339 字符串（含时区偏移），
 * 供「过期时间」表单提交使用，避免硬编码 UTC 导致过期边界偏移一整个时区。
 */
/**
 * 把 RFC3339 / ISO 时间串按本地时区取 YYYY-MM-DD，供「过期时间」编辑回填使用。
 * 与 endOfDayLocalISO 对称：提交按本地时区编码，回填也须按本地时区解码；
 * 若直接裸切 UTC 字符串（slice/split），西向时区会回显 +1 天并在重复保存时持续漂移。
 */
export function localDateStr(iso: string): string {
  const d = new Date(iso);
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

export function endOfDayLocalISO(date: string): string {
  // 以目标日期构造本地时间，取该日期当天的时区偏移（正确处理夏令时）
  const d = new Date(`${date}T23:59:59`);
  const offsetMin = -d.getTimezoneOffset();
  const sign = offsetMin >= 0 ? '+' : '-';
  const abs = Math.abs(offsetMin);
  const hh = String(Math.floor(abs / 60)).padStart(2, '0');
  const mm = String(abs % 60).padStart(2, '0');
  return `${date}T23:59:59${sign}${hh}:${mm}`;
}

/** 大数字缩写显示：33518599 -> "33.52M"，1234 -> "1,234"；空值按 0 处理 */
export function fmtNum(n: number | null | undefined): string {
  if (n == null) return '0';
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(2)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 10_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toLocaleString();
}

/**
 * 格式化趋势图时间桶标签：小时桶（"YYYY-MM-DD HH:00"）取 HH:MM，天桶取 MM/DD。
 * 后端按调用方时区格式化桶 key，这里只做截取，不做时区换算。
 */
export function fmtTrendTime(timeStr: string): string {
  if (timeStr.includes(' ')) {
    const time = timeStr.split(' ')[1] ?? '';
    return time.slice(0, 5) || timeStr;
  }
  const parts = timeStr.split('-');
  if (parts.length === 3) return `${parts[1]}/${parts[2]}`;
  return timeStr;
}

/** 倍率显示：默认两位小数，第三位有效时保留三位（1.5 -> "1.50x"，1.125 -> "1.125x"） */
export function fmtRate(v: number): string {
  const s = v.toFixed(3);
  return `${s.endsWith('0') ? v.toFixed(2) : s}x`;
}
