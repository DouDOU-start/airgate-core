import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

/** 图表卡片统一空态：虚线圆圈图标 + "暂无数据"，空态时整卡替换图表内容。 */
export function ChartEmptyState({ className = '', icon }: { className?: string; icon: ReactNode }) {
  const { t } = useTranslation();

  return (
    <div className={`flex w-full flex-col items-center justify-center ${className}`}>
      <span className="flex h-11 w-11 items-center justify-center rounded-full border border-dashed border-border text-text-tertiary">
        {icon}
      </span>
      <div className="mt-3 text-sm font-medium text-text-secondary">{t('common.no_data')}</div>
    </div>
  );
}
