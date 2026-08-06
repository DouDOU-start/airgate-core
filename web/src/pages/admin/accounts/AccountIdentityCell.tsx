import { Chip } from '@heroui/react';
import { PlatformIcon, platformDisplayLabel } from '../../../shared/components/PlatformIcon';
import { formatPlanTypeLabel, planTypeBadgeClass } from './AccountUsageCell';
import { AccountStateMessage } from './AccountStateMessage';

function typeLabel(type?: string) {
  const key = (type || '').toLowerCase();
  if (key === 'oauth') return 'OAuth';
  if (key === 'api_key' || key === 'apikey') return 'API Key';
  return type || '';
}

/** 解析订阅到期时间，返回短文案 + 是否已过期。 */
export function formatSubscriptionExpiry(raw?: string | null): {
  label: string;
  expired: boolean;
  absolute: string;
} | null {
  if (!raw || !String(raw).trim()) return null;
  const s = String(raw).trim();
  let ms = Date.parse(s);
  if (!Number.isFinite(ms)) {
    // 纯数字：unix 秒或毫秒
    const n = Number(s);
    if (Number.isFinite(n) && n > 0) {
      ms = n > 1e12 ? n : n * 1000;
    }
  }
  if (!Number.isFinite(ms)) return null;
  const d = new Date(ms);
  const absolute = d.toLocaleString(undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
  const now = Date.now();
  const expired = ms <= now;
  const days = Math.round((ms - now) / 86400000);
  let label: string;
  if (expired) {
    const ago = Math.abs(days);
    label = ago <= 0 ? '已过期' : `已过期 ${ago} 天`;
  } else if (days <= 0) {
    label = '今日到期';
  } else if (days === 1) {
    label = '明天到期';
  } else if (days < 30) {
    label = `${days} 天后到期`;
  } else {
    label = `至 ${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  }
  return { label, expired, absolute };
}

/**
 * 账号身份聚合：图标 + 名称/订阅 + 类型·邮箱
 * 平台名不再重复写（图标已表达）。
 */
export function AccountIdentityCell({
  name,
  platform,
  type,
  email,
  planType,
  subscriptionActiveUntil,
  errorMsg,
}: {
  name: string;
  platform?: string;
  type?: string;
  email?: string;
  planType?: string;
  /** 订阅有效期 */
  subscriptionActiveUntil?: string | null;
  /** 当前运行时状态原因，例如 OAuth 授权失效。 */
  errorMsg?: string | null;
}) {
  const plan = formatPlanTypeLabel(planType);
  const expiry = formatSubscriptionExpiry(subscriptionActiveUntil);
  const kind = typeLabel(type);
  const subtitle = [kind, email].filter(Boolean).join(' · ');
  const title = [
    name,
    platformDisplayLabel(platform),
    kind,
    email,
    plan ? `订阅：${plan}` : '',
    expiry ? `有效期：${expiry.absolute}` : '',
  ].filter(Boolean).join(' · ');

  return (
    <div className="flex min-w-0 max-w-[400px] items-center gap-3 py-1" title={title}>
      <PlatformIcon platform={platform} size={22} className="mt-0.5" />
      <div className="min-w-0 flex-1 space-y-0.5">
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span className="truncate text-sm font-medium leading-5 text-text">
            {name || '—'}
          </span>
          {plan ? (
            <span className={planTypeBadgeClass(planType)} title={plan}>
              <span className="ag-account-plan-badge__dot" aria-hidden="true" />
              {plan}
            </span>
          ) : null}
          {expiry ? (
            <span
              className={
                expiry.expired
                  ? 'shrink-0 text-[10px] font-medium tabular-nums text-danger'
                  : 'shrink-0 text-[10px] tabular-nums text-text-tertiary'
              }
              title={expiry.absolute}
            >
              {expiry.label}
            </span>
          ) : null}
        </div>
        {subtitle ? (
          <div className="flex min-w-0 items-center gap-1.5 text-[11px] leading-4 text-text-tertiary">
            {kind ? (
              <Chip size="sm" variant="soft" className="h-4 min-h-0 px-1.5">
                <span className="text-[10px]">{kind}</span>
              </Chip>
            ) : null}
            {email ? (
              <span className="min-w-0 truncate font-mono" title={email}>
                {email}
              </span>
            ) : null}
          </div>
        ) : (
          <div className="text-[11px] leading-4 text-text-tertiary">
            {platformDisplayLabel(platform)}
          </div>
        )}
        <AccountStateMessage message={errorMsg} />
      </div>
    </div>
  );
}
