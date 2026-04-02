import i18n from '../../i18n';

/** 格式化过期时间，未设置则显示"永不过期" */
export function formatExpiry(date?: string, neverLabel?: string): string {
  if (!date) return neverLabel ?? i18n.t('common.never_expire');
  return new Date(date).toLocaleDateString('zh-CN');
}

/** 格式化日期时间 (yyyy/M/d HH:mm) */
export function formatDateTime(date: string): string {
  return new Date(date).toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
}

/** 格式化日期 (yyyy/M/d) */
export function formatDate(date: string): string {
  return new Date(date).toLocaleDateString('zh-CN');
}
