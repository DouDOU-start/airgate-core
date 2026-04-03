import { useTranslation } from 'react-i18next';
import { Inbox } from 'lucide-react';
import { type ReactNode } from 'react';

interface EmptyStateProps {
  title?: string;
  description?: string;
  icon?: ReactNode;
  action?: ReactNode;
}

export function EmptyState({
  title,
  description,
  icon,
  action,
}: EmptyStateProps) {
  const { t } = useTranslation();
  const displayTitle = title ?? t('common.no_data');
  const displayDescription = description ?? t('common.no_data_desc');

  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <div className="flex items-center justify-center w-16 h-16 rounded-full bg-bg-deep mb-4">
        {icon || <Inbox className="w-7 h-7 text-text-tertiary" />}
      </div>
      <p className="text-lg font-semibold text-text">{displayTitle}</p>
      <p className="text-sm text-text-secondary mt-1.5 max-w-[300px] leading-relaxed">{displayDescription}</p>
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}
