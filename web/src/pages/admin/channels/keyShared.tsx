import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Chip, Spinner, Tooltip } from '@heroui/react';
import { RefreshCw } from 'lucide-react';
import type { ChannelKeyResp, ChannelStatus, ChannelType, HealthStatus } from '../../../shared/types';
import { CHANNEL_TYPE_OPTIONS } from './ChannelFormModal';

// 渠道视图（按渠道分组展开）与密钥视图（跨渠道平铺）共用的 key 级展示逻辑，
// 避免同一套「类型徽章/状态徽章/指标行」在两个视图里各写一份。

// 渠道类型 → 徽章配色
export const TYPE_CHIP_COLORS: Record<ChannelType, 'accent' | 'warning' | 'success' | 'default'> = {
  openai_compatible: 'accent',
  anthropic: 'warning',
  gemini: 'success',
  custom: 'default',
  openai_video: 'accent',
  suno: 'warning',
};

export function typeLabel(type: string): string {
  return CHANNEL_TYPE_OPTIONS.find((item) => item.id === type)?.label ?? type;
}

const HEALTH_CHIP_COLORS: Record<HealthStatus, 'success' | 'warning' | 'danger' | 'accent'> = {
  healthy: 'success',
  degraded: 'warning',
  suspended: 'danger',
  recovering: 'accent',
};

export function HealthStatusChip({ status }: { status: HealthStatus }) {
  const { t } = useTranslation();
  if (status === 'healthy') return null;
  return (
    <Chip color={HEALTH_CHIP_COLORS[status]} size="sm" variant="soft">
      {t(`channels.health_${status}`)}
    </Chip>
  );
}

// key 状态徽章：enabled 绿 / disabled_manual 灰 / disabled_auto 红 + error_msg tooltip
export function KeyStatusChip({ status, errorMsg }: { status: ChannelStatus; errorMsg: string }) {
  const { t } = useTranslation();

  if (status === 'disabled_auto') {
    const chip = (
      <Chip color="danger" size="sm" variant="soft">
        {t('channels.status_disabled_auto')}
      </Chip>
    );
    if (!errorMsg) return chip;
    return (
      <Tooltip>
        <Tooltip.Trigger className="inline-flex">{chip}</Tooltip.Trigger>
        <Tooltip.Content className="max-w-xs break-all">{errorMsg}</Tooltip.Content>
      </Tooltip>
    );
  }

  if (status === 'disabled_manual') {
    return (
      <Chip color="default" size="sm" variant="soft">
        {t('channels.status_disabled_manual')}
      </Chip>
    );
  }

  return (
    <Chip color="success" size="sm" variant="soft">
      {t('channels.status_enabled')}
    </Chip>
  );
}

// 一段带标签的行内指标：小标题在上、值在下，右对齐数值成列。
export function Metric({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1 leading-none">
      <span className="whitespace-nowrap text-[10px] uppercase tracking-wide text-text-tertiary">{label}</span>
      <span className="whitespace-nowrap font-mono text-xs text-text-secondary">{children}</span>
    </div>
  );
}

// 指标分组之间的竖向分隔线。
export function MetricDivider() {
  return <span className="hidden h-7 w-px self-center bg-border sm:block" />;
}

// 一把 key 的指标行：配置组（模型/优先级权重/成本倍率）| 运行时组（并发/RPM）| 金额组 | 标签，
// 渠道视图（KeyRow）与密钥视图（ChannelKeysTable）共用。
export function KeyMetricsRow({
  channelKey,
  onRefreshBalance,
  refreshingBalance,
  onRefreshUpstreamRate,
  refreshingUpstreamRate,
  showPriorityWeight = true,
}: {
  channelKey: ChannelKeyResp;
  onRefreshBalance: () => void;
  refreshingBalance: boolean;
  onRefreshUpstreamRate?: () => void;
  refreshingUpstreamRate?: boolean;
  /** 密钥视图已有独立的优先级/权重列，指标行内无需重复展示；渠道视图无独立列，保持展示。 */
  showPriorityWeight?: boolean;
}) {
  const { t } = useTranslation();
  const balanceUpdated = channelKey.balance_updated_at ? new Date(channelKey.balance_updated_at) : null;
  const fmt = (n: number) => `$${n.toFixed(2)}`;

  return (
    <div className="flex flex-wrap items-start gap-x-4 gap-y-3">
      {/* 配置组：模型 · 优先级权重 · 成本倍率 */}
      <div className="flex items-start gap-x-4">
        <Metric label={t('channels.models')}>
          {channelKey.models.length > 0 ? (
            <Tooltip>
              <Tooltip.Trigger className="inline-flex cursor-help">
                <span className="text-text">{channelKey.models.length}</span>
              </Tooltip.Trigger>
              <Tooltip.Content className="max-w-sm">
                <div className="max-h-56 overflow-y-auto font-mono text-xs leading-5">
                  {channelKey.models.map((model) => (
                    <div key={model}>{model}</div>
                  ))}
                </div>
              </Tooltip.Content>
            </Tooltip>
          ) : (
            <span className="text-text-tertiary">-</span>
          )}
        </Metric>
        {showPriorityWeight ? (
          <Metric label={`${t('channels.priority')}·${t('channels.weight')}`}>
            P{channelKey.priority} · W{channelKey.weight}
          </Metric>
        ) : null}
        <Metric label={t('channels.cost_ratio')}>
          <span className="inline-flex items-center gap-1">
            ×{channelKey.cost_ratio}
            {channelKey.upstream_rate_enabled ? (
              <>
                <span className="text-text-tertiary">·</span>
                <Tooltip>
                  <Tooltip.Trigger className="inline-flex cursor-help">
                    <span className="text-accent">
                      {channelKey.upstream_rate_at
                        ? `↑×${channelKey.upstream_rate}`
                        : `↑${t('channels.upstream_rate_never')}`}
                    </span>
                  </Tooltip.Trigger>
                  <Tooltip.Content className="max-w-xs">{t('channels.upstream_rate')}</Tooltip.Content>
                </Tooltip>
                {onRefreshUpstreamRate ? (
                  <Button
                    isIconOnly
                    aria-label={t('channels.upstream_rate')}
                    className="h-5 min-h-0 w-5"
                    isDisabled={refreshingUpstreamRate}
                    size="sm"
                    variant="ghost"
                    onPress={onRefreshUpstreamRate}
                  >
                    {refreshingUpstreamRate ? <Spinner size="sm" /> : <RefreshCw className="h-3 w-3" />}
                  </Button>
                ) : null}
              </>
            ) : null}
          </span>
        </Metric>
      </div>

      <MetricDivider />

      {/* 运行时组：并发 · RPM · 平均首字延迟（最近 5 分钟） */}
      <div className="flex items-start gap-x-4">
        <Metric label={t('channels.concurrency_label')}>
          {channelKey.current_concurrency}/{channelKey.max_concurrency > 0 ? channelKey.max_concurrency : '∞'}
        </Metric>
        <Metric label="RPM">{channelKey.current_rpm}</Metric>
        <Tooltip>
          <Tooltip.Trigger className="inline-flex cursor-help">
            <Metric label={t('channels.avg_first_token_ms')}>
              {channelKey.avg_first_token_ms > 0 ? `${Math.round(channelKey.avg_first_token_ms)}ms` : '-'}
            </Metric>
          </Tooltip.Trigger>
          <Tooltip.Content className="max-w-xs">{t('channels.avg_first_token_ms_hint')}</Tooltip.Content>
        </Tooltip>
      </div>

      <MetricDivider />

      {/* 金额组：今日（成本/收益）· 累计（成本/收益）· 余额 */}
      <div className="flex items-start gap-x-4">
        <Metric label={t('channels.stats_today')}>
          <span className="text-warning">{fmt(channelKey.today_cost)}</span>
          <span className="text-text-tertiary">/</span>
          <span className="text-success">{fmt(channelKey.today_revenue)}</span>
        </Metric>
        <Metric label={t('channels.stats_total')}>
          <span className="text-warning">{fmt(channelKey.total_cost)}</span>
          <span className="text-text-tertiary">/</span>
          <span className="text-success">{fmt(channelKey.total_revenue)}</span>
        </Metric>
        <Metric label={t('channels.balance')}>
          <span className="inline-flex items-center gap-1">
            {balanceUpdated ? fmt(channelKey.balance) : <span className="text-text-tertiary">{t('channels.balance_never')}</span>}
            <Button
              isIconOnly
              aria-label={t('channels.refresh_balance')}
              className="h-5 min-h-0 w-5"
              isDisabled={refreshingBalance}
              size="sm"
              variant="ghost"
              onPress={onRefreshBalance}
            >
              {refreshingBalance ? <Spinner size="sm" /> : <RefreshCw className="h-3 w-3" />}
            </Button>
          </span>
        </Metric>
      </div>

      {channelKey.tags.length > 0 ? (
        <>
          <MetricDivider />
          <div className="flex flex-wrap gap-1 self-center">
            {channelKey.tags.map((tag) => (
              <Chip color="default" key={tag} size="sm" variant="soft">
                {tag}
              </Chip>
            ))}
          </div>
        </>
      ) : null}
    </div>
  );
}
