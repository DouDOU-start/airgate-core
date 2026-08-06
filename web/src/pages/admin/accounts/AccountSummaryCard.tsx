import { useTranslation } from 'react-i18next';
import { PlatformIcon, platformDisplayLabel } from '../../../shared/components/PlatformIcon';
import type { AccountResp } from '../../../shared/types';
import { formatSubscriptionExpiry } from './AccountIdentityCell';
import { formatPlanTypeLabel, planTypeBadgeClass } from './AccountUsageCell';
import { AccountStateMessage } from './AccountStateMessage';

function accountTypeLabel(type?: string) {
  const key = (type || '').toLowerCase();
  if (key === 'oauth') return 'OAuth';
  if (key === 'api_key' || key === 'apikey') return 'API Key';
  return type || '—';
}

/** 测试与统计弹窗共用的账号身份摘要。 */
export function AccountSummaryCard({
  account,
  context,
}: {
  account: AccountResp;
  context?: string;
}) {
  const { t } = useTranslation();
  const planType = account.plan_type || account.usage?.plan_type;
  const planLabel = formatPlanTypeLabel(planType);
  const expiry = formatSubscriptionExpiry(account.subscription_active_until);
  const stateKey = `accounts.state_${account.state || ''}`;
  const translatedState = t(stateKey);
  const stateLabel = translatedState === stateKey ? account.state : translatedState;
  const secondaryEmail = account.email && account.email !== account.name ? account.email : '';

  return (
    <div className="ag-account-summary">
      <PlatformIcon
        platform={account.platform}
        size={24}
        className="ag-account-summary__icon"
      />
      <div className="ag-account-summary__identity">
        <div className="ag-account-summary__title-row">
          <span className="ag-account-summary__name" title={account.name}>
            {account.name || '—'}
          </span>
          {planLabel ? (
            <span className={planTypeBadgeClass(planType)} title={planLabel}>
              <span className="ag-account-plan-badge__dot" aria-hidden="true" />
              {planLabel}
            </span>
          ) : null}
          {expiry ? (
            <span
              className={
                expiry.expired
                  ? 'text-[10px] font-medium tabular-nums text-danger'
                  : 'text-[10px] tabular-nums text-text-tertiary'
              }
              title={expiry.absolute}
            >
              {expiry.label}
            </span>
          ) : null}
        </div>
        <div className="ag-account-summary__meta">
          {context ? (
            <>
              <span className="ag-account-summary__context">{context}</span>
              <span className="ag-account-summary__separator" aria-hidden="true" />
            </>
          ) : null}
          <span>{platformDisplayLabel(account.platform)}</span>
          <span className="ag-account-summary__separator" aria-hidden="true" />
          <span>{accountTypeLabel(account.type)}</span>
          {secondaryEmail ? (
            <>
              <span className="ag-account-summary__separator" aria-hidden="true" />
              <span className="ag-account-summary__email" title={secondaryEmail}>
                {secondaryEmail}
              </span>
            </>
          ) : null}
        </div>
      </div>
      <span
        className="ag-account-summary__state"
        data-has-error={Boolean(account.error_msg?.trim()) || undefined}
        data-state={account.state}
      >
        <span className="ag-account-summary__state-dot" aria-hidden="true" />
        {stateLabel}
      </span>
      <AccountStateMessage
        className="ag-account-summary__message"
        message={account.error_msg}
      />
    </div>
  );
}
