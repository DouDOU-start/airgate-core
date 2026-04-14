import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { Badge } from '../components/Badge';
import { HoverCard } from '../components/HoverCard';
import type { Column } from '../components/Table';
import type { UsageLogResp, CustomerUsageLogResp } from '../types';

/**
 * 列定义统一使用一个宽松的行类型：管理端拿到的是 UsageLogResp，
 * 而 end customer（API Key 登录）拿到的是 CustomerUsageLogResp（无 input_cost / actual_cost 等字段）。
 * customerScope 列不会读取那些缺失字段。
 */
type UsageRow = UsageLogResp | CustomerUsageLogResp;

/** 大数字友好显示：33518599 -> "33.52M"，1234 -> "1,234" */
export function fmtNum(n: number): string {
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(2)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 10_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toLocaleString();
}

/** 格式化费用 */
export function fmtCost(n: number): string {
  if (n >= 1000) return `$${(n / 1000).toFixed(2)}K`;
  return `$${n.toFixed(2)}`;
}

/** Reseller / admin 视角的成本列：包含完整的成本拆分与倍率信息 */
function buildResellerCostColumn(t: TFunction): Column<UsageRow> {
  return {
    key: 'cost',
    title: t('usage.cost'),
    width: '140px',
    render: (raw) => {
      const row = raw as UsageLogResp;
      return (
        <HoverCard
          content={
            <>
              <div className="text-xs font-semibold text-text mb-2">{t('usage.cost_detail')}</div>
              <div className="space-y-1 text-xs font-mono">
                <div className="flex justify-between gap-6">
                  <span className="text-text-tertiary">{t('usage.input_cost')}</span>
                  <span className="text-text-secondary">${row.input_cost.toFixed(6)}</span>
                </div>
                <div className="flex justify-between gap-6">
                  <span className="text-text-tertiary">{t('usage.output_cost')}</span>
                  <span className="text-text-secondary">${row.output_cost.toFixed(6)}</span>
                </div>
                {row.input_price > 0 && (
                  <div className="flex justify-between gap-6">
                    <span className="text-text-tertiary">{t('usage.input_unit_price')}</span>
                    <span className="text-text-secondary">${row.input_price.toFixed(4)} / 1M Token</span>
                  </div>
                )}
                {row.output_price > 0 && (
                  <div className="flex justify-between gap-6">
                    <span className="text-text-tertiary">{t('usage.output_unit_price')}</span>
                    <span className="text-text-secondary">${row.output_price.toFixed(4)} / 1M Token</span>
                  </div>
                )}
                {row.cached_input_cost > 0 && (
                  <div className="flex justify-between gap-6">
                    <span className="text-text-tertiary">{t('usage.cached_input_cost')}</span>
                    <span className="text-text-secondary">${row.cached_input_cost.toFixed(6)}</span>
                  </div>
                )}
                <div className="my-1 border-t border-glass-border" />
                {row.service_tier && (
                  <div className="flex justify-between gap-6">
                    <span className="text-text-tertiary">{t('usage.service_tier')}</span>
                    <span className="text-text-secondary capitalize">{row.service_tier}</span>
                  </div>
                )}
                <div className="flex justify-between gap-6">
                  <span className="text-text-tertiary">{t('usage.rate_multiplier')}</span>
                  <span className="text-text-secondary">{row.rate_multiplier.toFixed(2)}x</span>
                </div>
                {row.account_rate_multiplier > 0 && row.account_rate_multiplier !== 1 && (
                  <div className="flex justify-between gap-6">
                    <span className="text-text-tertiary">{t('usage.account_rate', '账号倍率')}</span>
                    <span className="text-text-secondary">{row.account_rate_multiplier.toFixed(2)}x</span>
                  </div>
                )}
                {row.sell_rate > 0 && (
                  <div className="flex justify-between gap-6">
                    <span className="text-text-tertiary">{t('usage.sell_rate', '销售倍率')}</span>
                    <span className="text-text-secondary">{row.sell_rate.toFixed(2)}x</span>
                  </div>
                )}
                <div className="my-1 border-t border-glass-border" />
                <div className="flex justify-between gap-6">
                  <span className="text-text-tertiary">{t('usage.original_cost')}</span>
                  <span className="text-text-secondary">${row.total_cost.toFixed(6)}</span>
                </div>
                {row.account_cost !== row.total_cost && (
                  <div className="flex justify-between gap-6">
                    <span className="text-text-tertiary">{t('usage.account_cost', '账号计费')}</span>
                    <span className="text-text-secondary">${row.account_cost.toFixed(6)}</span>
                  </div>
                )}
                <div className="flex justify-between gap-6">
                  <span className="text-text-tertiary">{t('usage.user_charged', '用户扣费')}</span>
                  <span className="text-text-secondary">${row.actual_cost.toFixed(6)}</span>
                </div>
                {row.sell_rate > 0 && row.billed_cost !== row.actual_cost && (
                  <>
                    <div className="flex justify-between gap-6">
                      <span className="text-text-tertiary">{t('usage.billed_cost', '客户账面')}</span>
                      <span className="text-text-secondary">${row.billed_cost.toFixed(6)}</span>
                    </div>
                    <div className="flex justify-between gap-6">
                      <span className="text-text-tertiary">{t('usage.profit', '利润')}</span>
                      <span style={{ color: 'var(--ag-success)' }}>
                        ${(row.billed_cost - row.actual_cost).toFixed(6)}
                      </span>
                    </div>
                  </>
                )}
              </div>
            </>
          }
        >
          <div className="font-mono text-xs text-right">
            {row.sell_rate > 0 && row.billed_cost !== row.actual_cost ? (
              <>
                <div className="text-text">${row.billed_cost.toFixed(6)}</div>
                <div className="text-text-tertiary">
                  {t('usage.cost_actual_short', '成本')} ${row.actual_cost.toFixed(6)}
                </div>
              </>
            ) : (
              <div className="text-text">${row.actual_cost.toFixed(6)}</div>
            )}
          </div>
        </HoverCard>
      );
    },
  };
}

/** End customer 视角的成本列：只展示后端剥离过的 cost 字段 */
function buildCustomerCostColumn(t: TFunction): Column<UsageRow> {
  return {
    key: 'cost',
    title: t('usage.cost'),
    width: '140px',
    render: (raw) => {
      const cost = (raw as CustomerUsageLogResp).cost ?? 0;
      return (
        <div className="font-mono text-xs text-right">
          <div className="text-text">${cost.toFixed(6)}</div>
        </div>
      );
    },
  };
}

/**
 * 使用记录表格的共享列定义。
 * 管理端和用户端共用，管理端额外在前面插入 user / api_key / account 列。
 *
 * customerScope=true 时切换为 end customer 视角的成本列，避免读取后端剥离过的字段。
 */
export function useUsageColumns(opts?: { customerScope?: boolean }): Column<UsageRow>[] {
  const { t } = useTranslation();
  const customerScope = opts?.customerScope ?? false;

  const costColumn = customerScope ? buildCustomerCostColumn(t) : buildResellerCostColumn(t);

  return [
    {
      key: 'created_at',
      title: t('usage.time'),
      width: '168px',
      render: (row) => (
        <span className="text-text-secondary">
          {new Date(row.created_at).toLocaleString('zh-CN')}
        </span>
      ),
    },
    {
      key: 'model',
      title: t('usage.model'),
      width: '220px',
      render: (row) => (
        <span className="block max-w-full truncate text-text" title={row.model}>
          {row.model}
        </span>
      ),
    },
    {
      key: 'tokens',
      title: 'TOKEN',
      width: '160px',
      render: (row) => {
        // 注意：cached_input_tokens 表示 cache read；cache_creation_tokens 为 5m+1h 之和，
        // 两者与 input/output 互斥计入 total_tokens。
        // CustomerUsageLogResp 不下发 cache_creation_* 字段，这里用 ?? 0 做兼容。
        const cacheCreation = (row as UsageLogResp).cache_creation_tokens ?? 0;
        const cacheCreation5m = (row as UsageLogResp).cache_creation_5m_tokens ?? 0;
        const cacheCreation1h = (row as UsageLogResp).cache_creation_1h_tokens ?? 0;
        const total =
          row.input_tokens + row.output_tokens + row.cached_input_tokens + cacheCreation;
        return (
          <HoverCard
            content={
              <>
                <div className="text-xs font-semibold text-text mb-2">Token {t('usage.detail')}</div>
                <div className="space-y-1 text-xs font-mono">
                  <div className="flex justify-between gap-6">
                    <span className="text-text-tertiary">{t('usage.input_tokens')}</span>
                    <span className="text-text-secondary">{row.input_tokens.toLocaleString()}</span>
                  </div>
                  <div className="flex justify-between gap-6">
                    <span className="text-text-tertiary">{t('usage.output_tokens')}</span>
                    <span className="text-text-secondary">{row.output_tokens.toLocaleString()}</span>
                  </div>
                  {row.cached_input_tokens > 0 && (
                    <div className="flex justify-between gap-6">
                      <span className="text-text-tertiary">{t('usage.cache_read')}</span>
                      <span className="text-text-secondary">{row.cached_input_tokens.toLocaleString()}</span>
                    </div>
                  )}
                  {cacheCreation5m > 0 && (
                    <div className="flex justify-between gap-6">
                      <span className="text-text-tertiary">{t('usage.cache_creation_5m')}</span>
                      <span className="text-text-secondary">{cacheCreation5m.toLocaleString()}</span>
                    </div>
                  )}
                  {cacheCreation1h > 0 && (
                    <div className="flex justify-between gap-6">
                      <span className="text-text-tertiary">{t('usage.cache_creation_1h')}</span>
                      <span className="text-text-secondary">{cacheCreation1h.toLocaleString()}</span>
                    </div>
                  )}
                  <div className="flex justify-between gap-6 pt-1 border-t border-glass-border">
                    <span className="text-text-tertiary">{t('usage.total_tokens')}</span>
                    <span className="text-primary font-semibold">{total.toLocaleString()}</span>
                  </div>
                </div>
              </>
            }
          >
            <div className="flex flex-col items-center gap-0.5">
              <div className="font-mono text-xs flex items-center gap-1.5">
                <span className="text-emerald-400">↓ {row.input_tokens.toLocaleString()}</span>
                <span className="text-sky-400">↑ {row.output_tokens.toLocaleString()}</span>
              </div>
              {(row.cached_input_tokens > 0 || cacheCreation > 0) && (
                <div className="text-[11px] font-mono text-text-tertiary flex items-center gap-2">
                  {row.cached_input_tokens > 0 && (
                    <span title={t('usage.cache_read')}>⊕ {fmtNum(row.cached_input_tokens)}</span>
                  )}
                  {cacheCreation > 0 && (
                    <span className="text-amber-400" title={t('usage.cache_creation')}>
                      ✦ {fmtNum(cacheCreation)}
                    </span>
                  )}
                </div>
              )}
            </div>
          </HoverCard>
        );
      },
    },
    costColumn,
    {
      key: 'stream',
      title: t('usage.type'),
      width: '84px',
      hideOnMobile: true,
      render: (row) => (
        <Badge variant={row.stream ? 'info' : 'default'}>
          {row.stream ? t('usage.type_stream') : t('usage.type_sync')}
        </Badge>
      ),
    },
    {
      key: 'first_token_ms',
      title: t('usage.first_token'),
      width: '96px',
      hideOnMobile: true,
      render: (row) => (
        <span className="font-mono text-xs text-text-secondary">
          {row.first_token_ms > 0 ? (row.first_token_ms >= 1000 ? `${(row.first_token_ms / 1000).toFixed(2)}s` : `${row.first_token_ms}ms`) : '-'}
        </span>
      ),
    },
    {
      key: 'duration_ms',
      title: t('usage.duration'),
      width: '96px',
      hideOnMobile: true,
      render: (row) => (
        <span className="font-mono text-xs">
          {row.duration_ms >= 1000 ? `${(row.duration_ms / 1000).toFixed(2)}s` : `${row.duration_ms}ms`}
        </span>
      ),
    },
  ];
}
