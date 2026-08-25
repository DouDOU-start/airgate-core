import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@heroui/react';
import { Eraser, Pencil, Power, PowerOff, RefreshCw, Trash2 } from 'lucide-react';

/**
 * 批量操作工具栏：仅在 selectedCount > 0 时渲染。
 *
 * - inline:   内联在工具栏行内（挤占空间）
 * - overlay:  绝对定位覆盖在父容器上方，不推动表格（推荐）
 */
export function BulkActionsBar({
  inline = false,
  overlay = false,
  selectedCount,
  onClear,
  onEdit,
  onEnable,
  onDisable,
  onRefreshQuota,
  onClearRateLimitMarkers,
  onDelete,
}: {
  inline?: boolean;
  overlay?: boolean;
  selectedCount: number;
  onClear: () => void;
  onEdit: () => void;
  onEnable: () => void;
  onDisable: () => void;
  onRefreshQuota: () => void;
  onClearRateLimitMarkers: () => void;
  onDelete: () => void;
}) {
  const { t } = useTranslation();
  const countPulseTimerRef = useRef<number | null>(null);
  const [countPulseToken, setCountPulseToken] = useState(0);
  const [isCountPulsing, setIsCountPulsing] = useState(false);

  useEffect(() => {
    if (countPulseTimerRef.current != null) {
      window.clearTimeout(countPulseTimerRef.current);
    }
    setIsCountPulsing(true);
    setCountPulseToken((token) => token + 1);
    countPulseTimerRef.current = window.setTimeout(() => {
      setIsCountPulsing(false);
      countPulseTimerRef.current = null;
    }, 420);
  }, [selectedCount]);

  useEffect(() => () => {
    if (countPulseTimerRef.current != null) {
      window.clearTimeout(countPulseTimerRef.current);
    }
  }, []);

  const selectedText = t('accounts.bulk_selected', { count: selectedCount });
  const selectedCountText = String(selectedCount);
  const countIndex = selectedText.indexOf(selectedCountText);
  const selectedTextBefore = countIndex >= 0 ? selectedText.slice(0, countIndex) : selectedText;
  const selectedTextAfter = countIndex >= 0 ? selectedText.slice(countIndex + selectedCountText.length) : '';

  if (selectedCount === 0) return null;

  const selectedCountLabel = (
    <span className="flex h-8 shrink-0 items-center gap-0.5 whitespace-nowrap text-[15px] font-semibold leading-none text-primary">
      <span>{selectedTextBefore}</span>
      {countIndex >= 0 ? (
        <span
          key={countPulseToken}
          className="ag-bulk-selected-count"
          data-pulse={isCountPulsing || undefined}
        >
          {selectedCountText}
        </span>
      ) : null}
      <span>{selectedTextAfter}</span>
    </span>
  );

  const actionButtons = (
    <>
      <ActionButton icon={<Pencil className="w-3.5 h-3.5" />} label={t('accounts.bulk_edit')} onClick={onEdit} />
      <ActionButton icon={<Power className="w-3.5 h-3.5" />} label={t('accounts.bulk_enable')} onClick={onEnable} />
      <ActionButton icon={<PowerOff className="w-3.5 h-3.5" />} label={t('accounts.bulk_disable')} onClick={onDisable} />
      <ActionButton
        icon={<RefreshCw className="w-3.5 h-3.5 text-success" />}
        label={t('accounts.bulk_refresh_quota')}
        onClick={onRefreshQuota}
      />
      <ActionButton
        icon={<Eraser className="w-3.5 h-3.5 text-warning" />}
        label={t('accounts.bulk_clear_family_cooldowns')}
        onClick={onClearRateLimitMarkers}
      />
      <ActionButton
        icon={<Trash2 className="w-3.5 h-3.5" />}
        label={t('accounts.bulk_delete')}
        onClick={onDelete}
        danger
      />
    </>
  );

  if (overlay) {
    return (
      <div
        className="ag-accounts-bulk-header-bar absolute left-0 top-0 z-20 flex w-full items-center gap-2 overflow-hidden border border-border bg-background px-2 animate-in fade-in duration-150"
        style={{
          background: 'linear-gradient(90deg, var(--surface-tertiary) 0%, var(--surface-secondary) 42%, var(--background) 100%)',
          backgroundColor: 'var(--background)',
        }}
      >
        {selectedCountLabel}

        <div className="h-4 w-px shrink-0 bg-border" />

        <div className="flex min-w-0 items-center gap-2">
          {actionButtons}
        </div>
      </div>
    );
  }

  if (inline) {
    return (
      <div
        className="inline-flex min-h-12 max-w-full flex-wrap items-center gap-2 rounded-[var(--radius)] border border-border bg-background px-4 py-2 animate-in fade-in slide-in-from-top-1 duration-200"
        style={{
          background: 'linear-gradient(90deg, var(--surface-tertiary) 0%, var(--surface-secondary) 42%, var(--background) 100%)',
          backgroundColor: 'var(--background)',
        }}
      >
        {selectedCountLabel}
        <div className="hidden h-5 w-px bg-border sm:block" />
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          {actionButtons}
        </div>
      </div>
    );
  }

  return (
    <div
      className="mb-3 flex flex-wrap items-center gap-2 rounded-[var(--radius)] px-4 py-2.5"
      style={{
        background: 'var(--ag-primary-subtle)',
        border: '1px solid color-mix(in oklab, var(--ag-primary) 52%, transparent)',
      }}
    >
      <span className="text-sm font-medium" style={{ color: 'var(--ag-primary)' }}>
        {t('accounts.bulk_selected', { count: selectedCount })}
      </span>
      <Button
        size="sm"
        variant="ghost"
        onPress={onClear}
        aria-label={t('accounts.bulk_clear')}
      >
        {t('accounts.bulk_clear')}
      </Button>

      <div className="hidden h-5 w-px bg-border sm:block" />

      {actionButtons}
    </div>
  );
}

function ActionButton({
  icon,
  label,
  onClick,
  danger = false,
}: {
  icon: React.ReactNode;
  label: string;
  onClick: () => void;
  danger?: boolean;
}) {
  return (
    <Button
      size="sm"
      variant="outline"
      className={`ag-bulk-action-button min-w-0 max-w-full shrink-0 items-center gap-1.5 leading-none ${danger ? 'text-danger' : ''}`}
      onPress={onClick}
    >
      {icon}
      <span className="min-w-0 truncate leading-none">{label}</span>
    </Button>
  );
}
