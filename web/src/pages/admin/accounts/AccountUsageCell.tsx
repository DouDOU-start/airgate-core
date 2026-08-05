import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Spinner } from '@heroui/react';
import { RefreshCw, RotateCcw } from 'lucide-react';
import type { AccountUsage, AccountUsageWindow } from '../../../shared/types';

function clampPct(p: number) {
  return Math.min(100, Math.max(0, Number(p) || 0));
}

function usageTone(p: number) {
  const n = clampPct(p);
  if (n >= 90) return 'critical';
  if (n >= 75) return 'warning';
  return 'healthy';
}

function formatReset(resetsAt?: string | null, now = Date.now()) {
  if (!resetsAt) return '';
  const t = Date.parse(resetsAt);
  if (!Number.isFinite(t)) return '';
  const seconds = Math.max(0, Math.floor((t - now) / 1000));
  if (seconds <= 0) return '0m';
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return h > 0 ? `${d}d${h}h` : `${d}d`;
  if (h > 0) return m > 0 ? `${h}h${m}m` : `${h}h`;
  return `${Math.max(m, 1)}m`;
}

function isSparkWindow(w: AccountUsageWindow) {
  const fam = `${w.limit_id || ''} ${w.limit_name || ''}`.toLowerCase();
  return fam.includes('spark') || fam.includes('bengalfox');
}

function shortWindowLabel(w: AccountUsageWindow) {
  const slot =
    w.key === '5h' ? '5h'
      : (w.key === 'weekly' || w.key === '7d') ? '7d'
        : w.key === 'daily' || w.key === '24h' ? '1d'
          : w.key === 'monthly' ? '30d'
            : w.key === 'on_demand' ? '按量'
              : (w.label || w.key || '…').slice(0, 6);

  if (w.limit_id || w.limit_name) {
    if (isSparkWindow(w)) return `Sp·${slot}`;
    if (w.limit_id === 'xai-on-demand') return '按量';
    if (w.limit_id === 'xai-product') return (w.label || w.limit_name || '产品').slice(0, 6);
    // 其它附加 limit：取展示名缩写 + 档位
    const fam = (w.limit_name || w.limit_id || '').replace(/^gpt-?/i, '').slice(0, 5);
    return fam ? `${fam}·${slot}` : slot;
  }
  return slot;
}

function windowSortKey(w: AccountUsageWindow) {
  // 主窗口在前，Spark 次之，其它附加 limit 最后；同组内 5h 先于 7d
  const group = !w.limit_id ? 0 : isSparkWindow(w) ? 1 : 2;
  const slot =
    w.key === '5h' ? 0
      : (w.key === 'weekly' || w.key === '7d') ? 1
        : w.key === 'daily' || w.key === '24h' ? 2
          : w.key === 'monthly' ? 3
            : w.key === 'on_demand' ? 4
              : 5;
  return group * 10 + slot;
}

export function formatPlanTypeLabel(plan?: string): string {
  if (!plan) return '';
  const key = plan.trim();
  if (!key) return '';
  const lower = key.toLowerCase().replace(/[\s_-]+/g, '');
  const map: Record<string, string> = {
    free: 'Free',
    plus: 'Plus',
    pro: 'Pro',
    prolite: 'Pro Lite',
    team: 'Team',
    business: 'Business',
    enterprise: 'Enterprise',
    max: 'Max',
    go: 'Go',
    super: 'SuperGrok',
    supergrok: 'SuperGrok',
    superheavy: 'SuperGrok Heavy',
    supergrokheavy: 'SuperGrok Heavy',
    premium: 'Premium',
  };
  return map[lower] || key.charAt(0).toUpperCase() + key.slice(1);
}

/** 订阅档位徽章：统一使用低饱和语义色，避免在表格中抢夺视觉焦点。 */
export function planTypeBadgeClass(plan?: string): string {
  const key = (plan || '').toLowerCase().replace(/[\s_-]+/g, '');
  const base = 'ag-account-plan-badge';
  if (key.includes('prolite')) {
    return `${base} ag-account-plan-badge--pro-lite`;
  }
  if (key.includes('super') || key.includes('heavy') || key.includes('premium') || key.includes('max') || key === 'pro' || (key.includes('pro') && !key.includes('plus'))) {
    return `${base} ag-account-plan-badge--pro`;
  }
  if (key.includes('plus') || key.includes('go')) {
    return `${base} ag-account-plan-badge--plus`;
  }
  if (key.includes('team') || key.includes('business')) {
    return `${base} ag-account-plan-badge--team`;
  }
  if (key.includes('enterprise')) {
    return `${base} ag-account-plan-badge--enterprise`;
  }
  if (key.includes('free')) {
    return `${base} ag-account-plan-badge--free`;
  }
  return `${base} ag-account-plan-badge--default`;
}

/** 是否支持主动刷新用量。
 *  Codex/Claude：官方 usage 窗口 API。
 *  xAI/Grok：cli-chat-proxy /v1/billing（对齐 CPA Manager 用量条）。
 */
export function accountSupportsUsageRefresh(platform: string, type: string): boolean {
  const p = (platform || '').toLowerCase();
  const t = (type || '').toLowerCase();
  if (p === 'codex' && (t === 'oauth' || t === '')) return true;
  if (p === 'claude' && (t === 'oauth' || t === '')) return true;
  if ((p === 'xai' || p === 'grok') && (t === 'oauth' || t === '')) return true;
  return false;
}

/** 是否支持消费「限额重置积分」（仅 Codex OAuth，对齐 airgate-openai / CPA）。 */
export function accountSupportsUsageReset(platform: string, type: string): boolean {
  const p = (platform || '').toLowerCase();
  const t = (type || '').toLowerCase();
  return p === 'codex' && (t === 'oauth' || t === '');
}

/**
 * 用量窗口：主限额 5h/7d + Spark 等附加 limit 一并展示（最多 6 条）。
 * Codex 有重置积分时展示次数 + 重置按钮。
 */
export function AccountUsageCell({
  usage,
  refreshing,
  resetting,
  onRefresh,
  onReset,
  canRefresh,
  canReset,
}: {
  usage?: AccountUsage | null;
  refreshing?: boolean;
  resetting?: boolean;
  onRefresh?: () => void;
  /** 消费一枚限额重置积分 */
  onReset?: () => void;
  canRefresh?: boolean;
  /** 是否允许展示重置（Codex OAuth） */
  canReset?: boolean;
}) {
  const { t } = useTranslation();
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (!usage?.windows?.length) return undefined;
    const timer = window.setInterval(() => setNow(Date.now()), 30_000);
    return () => window.clearInterval(timer);
  }, [usage?.windows?.length]);

  const windows = useMemo(() => {
    const all = [...(usage?.windows ?? [])];
    all.sort((a, b) => windowSortKey(a) - windowSortKey(b));
    return all.slice(0, 6);
  }, [usage?.windows]);

  const hiddenCount = Math.max(0, (usage?.windows?.length ?? 0) - windows.length);
  const resetAvailable = usage?.reset_credits_available ?? 0;
  // 有过用量快照才展示重置次数（避免未刷新时误显示 0）
  const showResetUI = Boolean(canReset && usage);
  const showCredits =
    usage?.credits
    && (usage.credits.unlimited
      || usage.credits.has_credits
      || (usage.credits.balance && usage.credits.balance !== '0' && usage.credits.balance !== '0.0'));
  const empty = windows.length === 0 && !showCredits && resetAvailable <= 0;

  const tipParts = (usage?.windows ?? []).map((w) => {
    const pct = clampPct(w.used_percent).toFixed(1);
    const reset = formatReset(w.resets_at, now);
    const name = w.limit_name || w.limit_id
      ? `${w.limit_name || w.limit_id} ${w.key}`
      : (w.label || w.key);
    return `${name}: ${pct}%${reset ? ` · ${t('accounts.usage_resets_in', { time: reset })}` : ''}`;
  });
  if (resetAvailable > 0) {
    tipParts.push(t('accounts.usage_reset_credits', { count: resetAvailable }));
  }

  return (
    <div className="ag-account-usage" title={tipParts.join('\n')}>
      {empty ? (
        <div className="ag-account-usage__empty">
          {canRefresh ? t('accounts.usage_empty') : '—'}
        </div>
      ) : (
        <div className="ag-account-usage__windows">
          {windows.map((w, i) => {
            const pct = clampPct(w.used_percent);
            const reset = formatReset(w.resets_at, now);
            const spark = isSparkWindow(w);
            return (
              <div
                key={`${w.limit_id || 'main'}:${w.key}:${i}`}
                className="ag-account-usage__window"
                data-tone={usageTone(pct)}
              >
                <span
                  className="ag-account-usage__label"
                  data-supplementary={spark || undefined}
                  title={w.limit_name || w.limit_id || w.label || w.key}
                >
                  {shortWindowLabel(w)}
                </span>
                <div
                  className="ag-account-usage__track"
                  role="progressbar"
                  aria-label={w.limit_name || w.limit_id || w.label || w.key}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={Math.round(pct)}
                >
                  <div
                    className="ag-account-usage__fill"
                    style={{ width: `${pct}%` }}
                  />
                </div>
                <span className="ag-account-usage__percent">
                  {pct.toFixed(0)}%
                </span>
                <span
                  className="ag-account-usage__reset-time"
                  title={reset ? t('accounts.usage_resets_in', { time: reset }) : undefined}
                >
                  {reset || '—'}
                </span>
              </div>
            );
          })}
        </div>
      )}

      {(showCredits || showResetUI || hiddenCount > 0 || usage?.stale || canRefresh) ? (
        <div className="ag-account-usage__footer">
          <div className="ag-account-usage__meta">
            {showCredits ? (
              <span className="ag-account-usage__meta-item">
                {usage!.credits!.unlimited
                  ? t('accounts.usage_unlimited')
                  : `${t('accounts.usage_credits')} ${usage!.credits!.balance || '—'}`}
              </span>
            ) : null}
            {showResetUI ? (
              <span
                className="ag-account-usage__meta-item"
                data-available={resetAvailable > 0 || undefined}
                title={t('accounts.usage_reset_hint')}
              >
                <RotateCcw className="h-2.5 w-2.5" aria-hidden="true" />
                {t('accounts.usage_reset_credits', { count: resetAvailable })}
              </span>
            ) : null}
            {hiddenCount > 0 ? (
              <span className="ag-account-usage__meta-item">+{hiddenCount}</span>
            ) : null}
            {usage?.stale ? (
              <span className="ag-account-usage__meta-item" data-stale="true">
                {t('accounts.usage_stale')}
              </span>
            ) : null}
          </div>

          <div className="ag-account-usage__actions">
            {showResetUI && resetAvailable > 0 && onReset ? (
              <span title={t('accounts.usage_reset_hint')}>
                <Button
                  size="sm"
                  variant="secondary"
                  className="ag-account-usage__reset-button"
                  isDisabled={resetting || refreshing}
                  aria-label={t('accounts.usage_reset_action')}
                  onPress={onReset}
                >
                  {resetting ? <Spinner size="sm" /> : <RotateCcw className="h-3 w-3" />}
                  {t('accounts.usage_reset_action')}
                </Button>
              </span>
            ) : null}
            {canRefresh ? (
              <Button
                isIconOnly
                size="sm"
                variant="ghost"
                className="ag-account-usage__refresh-button"
                aria-label={t('accounts.usage_refresh')}
                isDisabled={refreshing || resetting}
                onPress={onRefresh}
              >
                {refreshing ? <Spinner size="sm" /> : <RefreshCw className="h-3.5 w-3.5" />}
              </Button>
            ) : null}
          </div>
        </div>
      ) : null}
    </div>
  );
}
