import { memo, startTransition, useCallback, useEffect, useRef, useState, useSyncExternalStore, type CSSProperties, type ReactElement, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Chip, Dropdown } from '@heroui/react';
import { BarChart3, Eraser, MoreHorizontal, Pencil, RefreshCw, Trash2, Zap } from 'lucide-react';
import { NativeSwitch } from '../../../shared/components/NativeSwitch';
import type { AccountResp } from '../../../shared/types';

export interface AccountTableColumn {
  key: string;
  title: ReactNode;
  width?: string;
  mobileWidth?: string;
  maxWidth?: string;
  align?: 'left' | 'center' | 'right';
  render: (row: AccountResp) => ReactNode;
}

export const UNGROUPED_GROUP_FILTER = '__ungrouped__';
type SelectionListener = () => void;
export type AccountTypeFilterOption = {
  id: string;
  label: string;
  planLabel?: string;
  platformLabel?: string;
};
export type AccountUsageTodayStats = { requests: number; tokens: number; account_cost: number; user_cost: number };
export type AccountUsageCredits = { balance: number; unlimited: boolean };
export type AccountUsageWindow = {
  key?: string;
  label: string;
  display_label?: string;
  slot?: string;
  group?: string;
  sort_order?: number;
  used_percent: number;
  reset_at?: string;
  reset_after_seconds?: number;
  reset_seconds?: number;
};
export type AccountUsageInfo = {
  windows?: AccountUsageWindow[];
  credits?: AccountUsageCredits | null;
  today_stats?: AccountUsageTodayStats | null;
  updated_at?: string;
};
export type AccountUsageData = { accounts?: Record<string, AccountUsageInfo> };
export type CachedUsageWindow = {
  resetAtMs: number;
  usedPercent: number;
  window: AccountUsageWindow;
};
export type AccountUsageWindowCache = Map<string, CachedUsageWindow>;

export function renderAccountTypeFilterOption(option: AccountTypeFilterOption, showOAuthLabel = true): ReactNode {
  if (!option.planLabel) return option.label;
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      {option.platformLabel ? <span className="truncate">{option.platformLabel}</span> : null}
      {showOAuthLabel ? <span className="truncate">OAuth</span> : null}
      <Chip color="accent" size="sm" variant="soft">
        {option.planLabel}
      </Chip>
    </span>
  );
}

function getUsageWindowIdentity(window: AccountUsageWindow) {
  const key = window.key?.trim();
  if (key) return key;
  const group = window.group?.trim();
  const slot = window.slot?.trim();
  if (group || slot) return `${group || 'base'}:${slot || window.display_label?.trim() || window.label.trim()}`;
  return window.label.trim();
}

function getUsageWindowCacheKey(accountId: string, window: AccountUsageWindow) {
  return `${accountId}:${getUsageWindowIdentity(window)}`;
}

function getUsageWindowResetAtMs(window: AccountUsageWindow, now: number) {
  if (window.reset_at) {
    const parsed = Date.parse(window.reset_at);
    if (Number.isFinite(parsed) && parsed > now) return parsed;
  }
  const resetSeconds = Number(window.reset_seconds ?? 0);
  if (resetSeconds > 0) return now + resetSeconds * 1000;
  const resetAfterSeconds = Number(window.reset_after_seconds ?? 0);
  if (resetAfterSeconds > 0) return now + resetAfterSeconds * 1000;
  return 0;
}

function getUsageWindowUsedPercent(value: unknown) {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value === 'string' && value.trim()) {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return undefined;
}

function windowWithCachedReset(window: AccountUsageWindow, resetAtMs: number, now: number): AccountUsageWindow {
  if (resetAtMs <= now) {
    return {
      ...window,
      reset_seconds: 0,
    };
  }
  return {
    ...window,
    reset_at: new Date(resetAtMs).toISOString(),
    reset_seconds: Math.max(0, Math.ceil((resetAtMs - now) / 1000)),
  };
}

export function mergeCachedUsageWindows(data: AccountUsageData | undefined, cache: AccountUsageWindowCache): AccountUsageData | undefined {
  if (!data?.accounts) return data;

  const now = Date.now();
  const accounts: Record<string, AccountUsageInfo> = {};
  const liveCacheKeys = new Set<string>();

  for (const [accountId, usage] of Object.entries(data.accounts)) {
    const rawWindows = Array.isArray(usage?.windows) ? usage.windows : [];
    const mergedWindows: AccountUsageWindow[] = [];

    for (const window of rawWindows) {
      const cacheKey = getUsageWindowCacheKey(accountId, window);
      const resetAtMs = getUsageWindowResetAtMs(window, now);
      const cached = cache.get(cacheKey);
      const usedPercent = getUsageWindowUsedPercent(window.used_percent)
        ?? cached?.usedPercent
        ?? 0;
      const effectiveResetAtMs = resetAtMs > now
        ? resetAtMs
        : cached && cached.resetAtMs > now
          ? cached.resetAtMs
          : 0;
      const windowWithCachedUsage = {
        ...window,
        used_percent: usedPercent,
      };
      const nextWindow = effectiveResetAtMs > now
        ? windowWithCachedReset(windowWithCachedUsage, effectiveResetAtMs, now)
        : {
            ...windowWithCachedUsage,
            reset_seconds: Number(window.reset_seconds ?? 0),
          };

      cache.set(cacheKey, {
        resetAtMs: effectiveResetAtMs > now ? effectiveResetAtMs : 0,
        usedPercent,
        window: nextWindow,
      });
      liveCacheKeys.add(cacheKey);
      mergedWindows.push(nextWindow);
    }

    accounts[accountId] = {
      ...usage,
      windows: mergedWindows,
    };
  }

  for (const cacheKey of cache.keys()) {
    if (!liveCacheKeys.has(cacheKey)) {
      cache.delete(cacheKey);
    }
  }

  return {
    ...data,
    accounts,
  };
}

export function runAfterInputFrame(work: () => void) {
  if (typeof window === 'undefined') {
    startTransition(work);
    return;
  }

  window.requestAnimationFrame(() => {
    window.setTimeout(() => startTransition(work), 0);
  });
}

export function useLatestRef<T>(value: T) {
  const ref = useRef(value);
  ref.current = value;
  return ref;
}

export class AccountSelectionStore {
  private selectedIds = new Set<number>();
  private version = 0;
  private listeners = new Set<SelectionListener>();
  private rowListeners = new Map<number, Set<SelectionListener>>();

  subscribe = (listener: SelectionListener) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  subscribeRow = (id: number, listener: SelectionListener) => {
    let listeners = this.rowListeners.get(id);
    if (!listeners) {
      listeners = new Set();
      this.rowListeners.set(id, listeners);
    }
    listeners.add(listener);
    return () => {
      listeners?.delete(listener);
      if (listeners?.size === 0) {
        this.rowListeners.delete(id);
      }
    };
  };

  getSnapshot = () => this.version;

  has(id: number) {
    return this.selectedIds.has(id);
  }

  getSelectedIds() {
    return Array.from(this.selectedIds);
  }

  countVisible(ids: number[]) {
    let count = 0;
    for (const id of ids) {
      if (this.selectedIds.has(id)) count += 1;
    }
    return count;
  }

  setRow(id: number, isSelected: boolean) {
    const alreadySelected = this.selectedIds.has(id);
    if (alreadySelected === isSelected) return;

    if (isSelected) {
      this.selectedIds.add(id);
    } else {
      this.selectedIds.delete(id);
    }
    this.notify([id]);
  }

  setRows(ids: number[], isSelected: boolean) {
    const changedIds: number[] = [];
    for (const id of ids) {
      const alreadySelected = this.selectedIds.has(id);
      if (alreadySelected === isSelected) continue;
      if (isSelected) {
        this.selectedIds.add(id);
      } else {
        this.selectedIds.delete(id);
      }
      changedIds.push(id);
    }
    if (changedIds.length > 0) {
      this.notify(changedIds);
    }
  }

  clear() {
    if (this.selectedIds.size === 0) return;
    const changedIds = Array.from(this.selectedIds);
    this.selectedIds.clear();
    this.notify(changedIds);
  }

  private notify(changedIds: number[]) {
    this.version += 1;
    for (const id of changedIds) {
      this.rowListeners.get(id)?.forEach((listener) => listener());
    }
    this.listeners.forEach((listener) => listener());
  }
}

function StatusPill({ status, tooltip }: { status: 'active' | 'disabled'; tooltip?: string }) {
  const { t } = useTranslation();
  const chip = (
    <Chip color={status === 'active' ? 'success' : 'default'} size="sm" variant="soft">
      {status === 'active' ? t('status.active') : t('status.disabled')}
    </Chip>
  );

  if (!tooltip) return chip;
  return <span className="inline-flex" title={tooltip}>{chip}</span>;
}

export function TableSelectionCheckbox({
  ariaLabel,
  isIndeterminate,
  isSelected,
  onChange,
}: {
  ariaLabel: string;
  isIndeterminate?: boolean;
  isSelected: boolean;
  onChange: (isSelected: boolean) => void;
}) {
  const checkboxRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (checkboxRef.current) {
      checkboxRef.current.indeterminate = !!isIndeterminate;
    }
  }, [isIndeterminate]);

  return (
    <input
      ref={checkboxRef}
      type="checkbox"
      aria-label={ariaLabel}
      checked={isSelected}
      className="ag-table-selection-checkbox"
      onChange={(event) => onChange(event.currentTarget.checked)}
    />
  );
}

export function columnAlignClass(align?: AccountTableColumn['align']) {
  if (align === 'right') return 'text-right';
  if (align === 'left') return 'text-left';
  return 'text-center';
}

function cellJustifyClass(align?: AccountTableColumn['align']) {
  if (align === 'right') return 'justify-end';
  if (align === 'left') return 'justify-start';
  return 'justify-center';
}

export const ACCOUNT_SELECTION_COLUMN_STYLE: CSSProperties = {
  minWidth: 'var(--ag-accounts-selection-column-width)',
  width: 'var(--ag-accounts-selection-column-width)',
};

export function columnWidthStyle(column: AccountTableColumn): CSSProperties | undefined {
  if (!column.width) return undefined;
  const width = column.mobileWidth
    ? `var(--ag-accounts-col-${column.key}-width, ${column.width})`
    : column.width;
  return {
    minWidth: width,
    width,
    maxWidth: column.maxWidth,
  };
}

const AccountRowSelectionCell = memo(function AccountRowSelectionCell({
  ariaLabel,
  selectionStore,
  rowId,
  onSelectedChange,
}: {
  ariaLabel: string;
  selectionStore: AccountSelectionStore;
  rowId: number;
  onSelectedChange: (id: number, isSelected: boolean) => void;
}) {
  const isSelected = useSyncExternalStore(
    useCallback((listener) => selectionStore.subscribeRow(rowId, listener), [rowId, selectionStore]),
    useCallback(() => selectionStore.has(rowId), [rowId, selectionStore]),
    () => false,
  );
  const handleChange = useCallback((nextSelected: boolean) => {
    onSelectedChange(rowId, nextSelected);
  }, [onSelectedChange, rowId]);

  return (
    <div className="inline-flex" onClick={(event) => event.stopPropagation()}>
      <TableSelectionCheckbox
        ariaLabel={ariaLabel}
        isSelected={isSelected}
        onChange={handleChange}
      />
    </div>
  );
});

const AccountTableCellContent = memo(function AccountTableCellContent({
  column,
  row,
}: {
  column: AccountTableColumn;
  row: AccountResp;
}) {
  return (
    <div className={`flex w-full min-w-0 items-center ${cellJustifyClass(column.align)}`}>
      {column.render(row)}
    </div>
  );
}, (prev, next) => prev.column === next.column && prev.row === next.row);

export const AccountSchedulingSwitch = memo(function AccountSchedulingSwitch({
  ariaLabel,
  isSelected,
  rowId,
  onToggle,
}: {
  ariaLabel: string;
  isSelected: boolean;
  rowId: number;
  onToggle: (id: number) => void;
}) {
  const handleClick = useCallback(() => {
    onToggle(rowId);
  }, [onToggle, rowId]);

  return (
    <NativeSwitch
      ariaLabel={ariaLabel}
      isSelected={isSelected}
      onChange={handleClick}
    />
  );
}, (prev, next) => (
  prev.ariaLabel === next.ariaLabel
  && prev.isSelected === next.isSelected
  && prev.rowId === next.rowId
  && prev.onToggle === next.onToggle
));

export const AccountRowActions = memo(function AccountRowActions({
  row,
  labels,
  onEdit,
  onDelete,
  onTest,
  onStats,
  onRefreshQuota,
  onClearCooldowns,
}: {
  row: AccountResp;
  labels: {
    actions: string;
    clearCooldowns: string;
    delete: string;
    edit: string;
    more: string;
    refreshQuota: string;
    stats: string;
    test: string;
  };
  onEdit: (row: AccountResp) => void;
  onDelete: (row: AccountResp) => void;
  onTest: (row: AccountResp) => void;
  onStats: (id: number) => void;
  onRefreshQuota: (id: number) => void;
  onClearCooldowns: (id: number) => void;
}) {
  return (
    <div className="ag-table-row-actions ag-account-row-actions mx-auto flex w-[92px] items-center justify-center gap-1">
      <button
        type="button"
        aria-label={labels.edit}
        className="ag-account-row-action-button"
        onClick={(event) => {
          event.stopPropagation();
          onEdit(row);
        }}
      >
        <Pencil className="w-3.5 h-3.5" />
      </button>
      <button
        type="button"
        aria-label={labels.delete}
        className="ag-account-row-action-button ag-account-row-action-button--danger"
        onClick={(event) => {
          event.stopPropagation();
          onDelete(row);
        }}
      >
        <Trash2 className="w-3.5 h-3.5" />
      </button>
      <Dropdown>
        <Dropdown.Trigger
          aria-label={labels.more}
          className="ag-account-row-more-trigger ag-account-row-action-button h-7 w-7 min-w-7"
        >
          <MoreHorizontal className="w-3.5 h-3.5" />
        </Dropdown.Trigger>
        <Dropdown.Popover placement="bottom end">
          <Dropdown.Menu
            aria-label={labels.actions}
            onAction={(key) => {
              switch (String(key)) {
                case 'test':
                  onTest(row);
                  break;
                case 'stats':
                  onStats(row.id);
                  break;
                case 'refresh_quota':
                  onRefreshQuota(row.id);
                  break;
                case 'clear_cooldowns':
                  onClearCooldowns(row.id);
                  break;
              }
            }}
          >
            <Dropdown.Item id="test" textValue={labels.test}>
              <span className="flex items-center gap-2">
                <Zap className="w-3.5 h-3.5" style={{ color: 'var(--ag-warning)' }} />
                {labels.test}
              </span>
            </Dropdown.Item>
            <Dropdown.Item id="stats" textValue={labels.stats}>
              <span className="flex items-center gap-2">
                <BarChart3 className="w-3.5 h-3.5" style={{ color: 'var(--ag-primary)' }} />
                {labels.stats}
              </span>
            </Dropdown.Item>
            {row.type === 'oauth' ? (
              <Dropdown.Item id="refresh_quota" textValue={labels.refreshQuota}>
                <span className="flex items-center gap-2">
                  <RefreshCw className="w-3.5 h-3.5" style={{ color: 'var(--ag-success)' }} />
                  {labels.refreshQuota}
                </span>
              </Dropdown.Item>
            ) : null}
            <Dropdown.Item id="clear_cooldowns" textValue={labels.clearCooldowns}>
              <span className="flex items-center gap-2">
                <Eraser className="w-3.5 h-3.5" style={{ color: 'var(--ag-warning)' }} />
                {labels.clearCooldowns}
              </span>
            </Dropdown.Item>
          </Dropdown.Menu>
        </Dropdown.Popover>
      </Dropdown>
    </div>
  );
}, (prev, next) => (
  prev.row === next.row
  && prev.labels === next.labels
  && prev.onEdit === next.onEdit
  && prev.onDelete === next.onDelete
  && prev.onTest === next.onTest
  && prev.onStats === next.onStats
  && prev.onRefreshQuota === next.onRefreshQuota
  && prev.onClearCooldowns === next.onClearCooldowns
));

export const AccountTableRow = memo(function AccountTableRow({
  columns,
  row,
  selectRowAriaLabel,
  selectionStore,
  onSelectedChange,
}: {
  columns: AccountTableColumn[];
  row: AccountResp;
  selectRowAriaLabel: string;
  selectionStore: AccountSelectionStore;
  onSelectedChange: (id: number, isSelected: boolean) => void;
}) {
  return (
    <tr data-slot="tr" data-key={row.id}>
      <td data-slot="td" className="text-center" style={ACCOUNT_SELECTION_COLUMN_STYLE}>
        <AccountRowSelectionCell
          ariaLabel={selectRowAriaLabel}
          rowId={row.id}
          selectionStore={selectionStore}
          onSelectedChange={onSelectedChange}
        />
      </td>
      {columns.map((column) => (
        <td
          data-slot="td"
          key={column.key}
          style={columnWidthStyle(column)}
        >
          <AccountTableCellContent column={column} row={row} />
        </td>
      ))}
    </tr>
  );
}, (prev, next) => (
  prev.columns === next.columns
  && prev.row === next.row
  && prev.selectRowAriaLabel === next.selectRowAriaLabel
  && prev.selectionStore === next.selectionStore
  && prev.onSelectedChange === next.onSelectedChange
));

export function AccountsTableLoadingRow({ colSpan, minHeight = 220 }: { colSpan: number; minHeight?: number }) {
  return (
    <tr data-slot="tr" data-key="loading">
      <td data-slot="td" colSpan={colSpan}>
        <div aria-busy="true" aria-live="polite" className="w-full" style={{ minHeight }}>
          <span className="sr-only">Loading</span>
        </div>
      </td>
    </tr>
  );
}

export const AutoRefreshCountdownLabel = memo(function AutoRefreshCountdownLabel({
  autoRefresh,
  label,
  offLabel,
  onRefresh,
}: {
  autoRefresh: number;
  label: string;
  offLabel: string;
  onRefresh: () => void;
}) {
  const labelRef = useRef<HTMLSpanElement>(null);

  useEffect(() => {
    const renderCountdown = (seconds: number) => {
      if (labelRef.current) {
        labelRef.current.textContent = autoRefresh ? `${label}${seconds}s` : offLabel;
      }
    };

    if (!autoRefresh) {
      renderCountdown(0);
      return undefined;
    }

    let remaining = autoRefresh;
    renderCountdown(remaining);
    const timer = window.setInterval(() => {
      remaining -= 1;
      if (remaining <= 0) {
        onRefresh();
        remaining = autoRefresh;
      }
      renderCountdown(remaining);
    }, 1000);
    return () => window.clearInterval(timer);
  }, [autoRefresh, label, offLabel, onRefresh]);

  return (
    <span ref={labelRef}>
      {autoRefresh ? `${label}${autoRefresh}s` : offLabel}
    </span>
  );
}, (prev, next) => (
  prev.autoRefresh === next.autoRefresh
  && prev.label === next.label
  && prev.offLabel === next.offLabel
  && prev.onRefresh === next.onRefresh
));

// formatCountdown 把剩余毫秒格式化成 "Xd Yh"/"Xh Ym"/"Ym" 样式，
// 与 sub2api 的"限流中 10h 16m 自动恢复"徽标一致。
function formatCountdown(ms: number): string {
  if (ms <= 0) return '';
  const s = Math.floor(ms / 1000);
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${sec}s`;
}

function accountHasLiveCooldown(row: AccountResp, now: number): boolean {
  const stateUntil = row.state_until ? Date.parse(row.state_until) : 0;
  if (stateUntil > now) return true;
  return (row.family_cooldowns || []).some((fc) => Date.parse(fc.until) > now);
}

let cooldownClockNow = Date.now();
let cooldownClockTimer: number | null = null;
const cooldownClockListeners = new Set<() => void>();

function subscribeCooldownClock(listener: () => void) {
  cooldownClockNow = Date.now();
  cooldownClockListeners.add(listener);
  if (cooldownClockTimer == null) {
    cooldownClockTimer = window.setInterval(() => {
      cooldownClockNow = Date.now();
      cooldownClockListeners.forEach((notify) => notify());
    }, 1000);
  }

  return () => {
    cooldownClockListeners.delete(listener);
    if (cooldownClockListeners.size === 0 && cooldownClockTimer != null) {
      window.clearInterval(cooldownClockTimer);
      cooldownClockTimer = null;
    }
  };
}

function subscribeIdleClock() {
  return () => {};
}

function getCooldownClockSnapshot() {
  return cooldownClockNow;
}

function useCooldownClock(enabled: boolean): number {
  return useSyncExternalStore(
    enabled ? subscribeCooldownClock : subscribeIdleClock,
    getCooldownClockSnapshot,
    getCooldownClockSnapshot,
  );
}

let usageResetClockNow = Date.now();
let usageResetClockTimer: number | null = null;
const usageResetClockListeners = new Set<() => void>();

function subscribeUsageResetClock(listener: () => void) {
  usageResetClockNow = Date.now();
  usageResetClockListeners.add(listener);
  if (usageResetClockTimer == null) {
    usageResetClockTimer = window.setInterval(() => {
      usageResetClockNow = Date.now();
      usageResetClockListeners.forEach((notify) => notify());
    }, 30_000);
  }

  return () => {
    usageResetClockListeners.delete(listener);
    if (usageResetClockListeners.size === 0 && usageResetClockTimer != null) {
      window.clearInterval(usageResetClockTimer);
      usageResetClockTimer = null;
    }
  };
}

function getUsageResetClockSnapshot() {
  return usageResetClockNow;
}

export function useUsageResetClock(enabled: boolean): number {
  return useSyncExternalStore(
    enabled ? subscribeUsageResetClock : subscribeIdleClock,
    getUsageResetClockSnapshot,
    getUsageResetClockSnapshot,
  );
}

/**
 * AccountStatusCell 渲染账号状态徽标，按 state + state_until 动态展示：
 *   active       → 绿色 "活跃"
 *   rate_limited → 橙色 "限流中 Xh Ym"（state_until 倒计时）
 *   degraded     → 黄色 "降级 Xm"（池账号软降级，倒计时）
 *   disabled     → 红色 "已禁用"（tooltip 显示 error_msg）
 * 到期的 rate_limited / degraded 视作 active（后端 lazy 回收，前端可先显示 active）。
 *
 * 同一行还会叠加家族级冷却（family_cooldowns）：账号 state 可能仍是 active，
 * 但某个 family（如 gpt-image）在 Redis 上仍处冷却中。用一个橙色小 pill
 * 标出"限流家族数"，hover tooltip 列出每个家族剩余时间。
 */
export function AccountStatusCell({ row }: { row: AccountResp }) {
  const { t } = useTranslation();
  const hasLiveCooldown = accountHasLiveCooldown(row, Date.now());
  const tickingNow = useCooldownClock(hasLiveCooldown);
  const now = hasLiveCooldown ? tickingNow : Date.now();
  const untilMs = row.state_until ? Date.parse(row.state_until) : 0;
  const remainingMs = untilMs - now;
  const hasCountdown = untilMs > 0 && remainingMs > 0;

  // 过滤出仍生效的家族冷却（后端可能返回刚到期的）。
  const liveFamilyCooldowns = (row.family_cooldowns || []).filter(
    (fc) => Date.parse(fc.until) > now,
  );

  const pill = (label: string, bg: string, fg: string, tooltip?: string) => (
    <span
      className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-[11px] font-semibold border whitespace-nowrap"
      style={{ background: bg, color: fg, borderColor: bg }}
      title={tooltip}
    >
      <span className="w-1.5 h-1.5 rounded-full" style={{ background: fg }} />
      {label}
    </span>
  );

  // 主 state 徽标
  let mainBadge: ReactElement;
  if (row.state === 'rate_limited' && hasCountdown) {
    mainBadge = pill(
      `${t('accounts.rate_limited_label', '限流中')} ${formatCountdown(remainingMs)}`,
      'var(--ag-warning-subtle)',
      'var(--ag-warning)',
      t('accounts.rate_limited_tooltip', '上游限流，到期自动恢复，不影响调度开关'),
    );
  } else if (row.state === 'degraded' && hasCountdown) {
    mainBadge = pill(
      `${t('accounts.degraded_label', '降级')} ${formatCountdown(remainingMs)}`,
      'var(--ag-warning-subtle)',
      'var(--ag-warning)',
      t('accounts.degraded_tooltip', '上游池抖动，软降级仅做兜底，到期自动恢复'),
    );
  } else if (row.state === 'disabled') {
    const reason = row.error_msg?.trim() === '管理员手动关闭调度' ? '手动关闭' : row.error_msg?.trim();
    mainBadge = (
      <div className="inline-flex min-w-0 max-w-full flex-col items-center gap-0.5">
        <StatusPill status="disabled" tooltip={reason || undefined} />
        {reason && (
          <span className="block max-w-[5.75rem] truncate text-center text-[10px] leading-none text-[var(--ag-muted)]" title={reason}>
            {reason}
          </span>
        )}
      </div>
    );
  } else {
    // active，或 rate_limited/degraded 已到期（lazy 恢复）
    mainBadge = <StatusPill status="active" />;
  }

  if (liveFamilyCooldowns.length === 0) {
    return mainBadge;
  }

  // tooltip 多行：每个家族 + 剩余时间，rate-limit 原因截断到 80 字符避免过宽
  const familyTooltip = liveFamilyCooldowns
    .map((fc) => {
      const ms = Date.parse(fc.until) - now;
      const reason = fc.reason ? ` — ${fc.reason.slice(0, 80)}` : '';
      return `${fc.family} ${formatCountdown(ms)}${reason}`;
    })
    .join('\n');

  const familyLabel = t(
    'accounts.family_cooldown_label',
    '{{count}} 家族限流',
    { count: liveFamilyCooldowns.length },
  );

  return (
    <div className="inline-flex flex-wrap items-center gap-1">
      {mainBadge}
      {pill(
        familyLabel,
        'var(--ag-warning-subtle)',
        'var(--ag-warning)',
        familyTooltip,
      )}
    </div>
  );
}

export function AccountCapacityChip({ current, max }: { current: number; max: number }) {
  const previousCurrentRef = useRef(current);
  const pulseTimerRef = useRef<number | null>(null);
  const [isPulsing, setIsPulsing] = useState(false);
  const [pulseTone, setPulseTone] = useState<'success' | 'warning'>('success');
  const [pulseToken, setPulseToken] = useState(0);
  const state = current <= 0 ? 'idle' : current >= max ? 'full' : 'active';

  useEffect(() => {
    if (previousCurrentRef.current === current) return;
    const previousCurrent = previousCurrentRef.current;
    previousCurrentRef.current = current;

    if (pulseTimerRef.current != null) {
      window.clearTimeout(pulseTimerRef.current);
    }

    setPulseTone(current < previousCurrent ? 'warning' : 'success');
    setIsPulsing(true);
    setPulseToken((token) => token + 1);
    pulseTimerRef.current = window.setTimeout(() => {
      setIsPulsing(false);
      pulseTimerRef.current = null;
    }, 520);
  }, [current]);

  useEffect(() => () => {
    if (pulseTimerRef.current != null) {
      window.clearTimeout(pulseTimerRef.current);
    }
  }, []);

  return (
    <span
      key={pulseToken}
      className="ag-account-capacity"
      data-state={state}
      data-pulse={isPulsing || undefined}
      data-pulse-tone={pulseTone}
      title={`${current} / ${max}`}
    >
      <span className="ag-account-capacity-current">{current}</span>
      <span className="ag-account-capacity-divider">/</span>
      <span className="ag-account-capacity-max">{max}</span>
    </span>
  );
}

