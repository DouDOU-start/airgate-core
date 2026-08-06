import { AlertTriangle } from 'lucide-react';
import { cx } from '../../../shared/utils/cx';

/** 展示账号当前状态的运行时原因，例如 OAuth 授权失效或上游限流。 */
export function AccountStateMessage({
  message,
  className,
}: {
  message?: string | null;
  className?: string;
}) {
  const normalized = message?.trim();
  if (!normalized) return null;

  return (
    <div
      aria-label={normalized}
      className={cx('ag-account-state-message', className)}
      role="status"
      title={normalized}
    >
      <AlertTriangle aria-hidden="true" className="ag-account-state-message__icon" />
      <span>{normalized}</span>
    </div>
  );
}
