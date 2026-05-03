import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useToast } from '../ui';

/**
 * useClipboard - 统一的剪贴板复制 hook
 *
 * 返回 copy 函数，自动处理错误和用户反馈。
 * - 成功时显示 toast 提示
 * - 失败时回退到 execCommand('copy') 并显示错误提示
 */
export function useClipboard() {
  const { toast } = useToast();
  const { t } = useTranslation();

  const copy = useCallback(
    async (text: string, successMsg?: string) => {
      // 尝试 1: Clipboard API（HTTPS 下可用）
      if (navigator.clipboard?.writeText) {
        try {
          await navigator.clipboard.writeText(text);
          toast('success', successMsg ?? t('common.copied'));
          return;
        } catch { /* 继续回退 */ }
      }

      // 尝试 2: execCommand（兼容 HTTP 和旧浏览器）
      try {
        const textarea = document.createElement('textarea');
        textarea.value = text;
        textarea.setAttribute('readonly', '');
        textarea.style.position = 'fixed';
        textarea.style.left = '-9999px';
        document.body.appendChild(textarea);
        textarea.focus();
        textarea.select();
        const ok = document.execCommand('copy');
        document.body.removeChild(textarea);
        if (ok) {
          toast('success', successMsg ?? t('common.copied'));
          return;
        }
      } catch { /* 继续回退 */ }

      toast('error', t('common.copy_failed', '复制失败'));
    },
    [toast, t],
  );

  return copy;
}
