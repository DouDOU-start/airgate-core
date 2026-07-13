import { useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from '@tanstack/react-router';
import { useQuery } from '@tanstack/react-query';
import { Button, Chip, EmptyState, Input } from '@heroui/react';
import { Inbox, Search, Sparkles } from 'lucide-react';
import { modelMarketApi } from '../shared/api/modelMarket';
import { queryKeys } from '../shared/queryKeys';
import { useSiteSettings, defaultLogoUrl } from '../app/providers/SiteSettingsProvider';
import { useDebouncedValue } from '../shared/hooks/useDebouncedValue';
import { getToken } from '../shared/api/client';
import type { ModelMarketItemResp } from '../shared/types';

type Translate = (key: string, options?: Record<string, unknown>) => string;

// 单次拉全量：模型广场是浏览/比价页，不做分页 UI；上限即后端 PageReq.page_size 允许的最大值。
const MARKET_PAGE_SIZE = 100;

function fmtPrice(value: number): string {
  if (!value) return '—';
  return `$${value}`;
}

// cacheLine 缓存单价行，逻辑与管理端模型卡片一致（公开响应不含 pricing_extra，无需处理服务档/长上下文）。
function cacheLine(row: ModelMarketItemResp, t: Translate): ReactNode {
  const parts: ReactNode[] = [];
  const push = (key: string, label: string, value: number) => {
    if (value <= 0) return;
    parts.push(
      <span className="whitespace-nowrap" key={key}>
        <span className="text-text-tertiary">{label} </span>
        <span className="font-medium">{fmtPrice(value)}</span>
      </span>,
    );
  };
  push('r', t('model_market.price_short_cached'), row.cached_input_price);
  push('w5', t('model_market.price_short_write5m'), row.cache_creation_price);
  push('w1h', t('model_market.price_short_write1h'), row.cache_creation_1h_price);
  if (parts.length === 0) return null;
  return parts.map((p, i) => (i === 0 ? p : [<span className="text-text-tertiary/60" key={`s${i}`}> · </span>, p]));
}

function MarketPriceCard({ row, t }: { row: ModelMarketItemResp; t: Translate }) {
  const cache = cacheLine(row, t);
  return (
    <div className="flex flex-col rounded-[var(--ag-radius-lg)] border border-border bg-surface p-5 transition-colors hover:border-text-tertiary/50">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0 truncate font-mono text-sm font-medium text-text" title={row.model}>
          {row.model}
        </div>
        {row.tag ? <Chip size="sm" variant="soft">{row.tag.name}</Chip> : null}
      </div>
      <div className="mt-4 grid grid-cols-2 gap-3">
        <div>
          <div className="text-[11px] text-text-tertiary">{t('model_market.price_short_input')}</div>
          <div className="mt-0.5 font-mono tabular-nums">
            {row.input_price > 0 ? (
              <>
                <span className="text-sm text-text-tertiary">$</span>
                <span className="text-xl font-semibold tracking-tight text-text">{row.input_price}</span>
                <span className="ml-1 text-[10px] text-text-tertiary">/1M</span>
              </>
            ) : <span className="text-xl text-text-tertiary">—</span>}
          </div>
        </div>
        <div>
          <div className="text-[11px] text-text-tertiary">{t('model_market.price_short_output')}</div>
          <div className="mt-0.5 font-mono tabular-nums">
            {row.output_price > 0 ? (
              <>
                <span className="text-sm text-text-tertiary">$</span>
                <span className="text-xl font-semibold tracking-tight text-text">{row.output_price}</span>
                <span className="ml-1 text-[10px] text-text-tertiary">/1M</span>
              </>
            ) : <span className="text-xl text-text-tertiary">—</span>}
          </div>
        </div>
      </div>
      {(cache || row.per_request_price > 0) ? (
        <div className="mt-3 space-y-1 font-mono text-xs tabular-nums text-text-secondary">
          {cache ? <div className="flex flex-wrap gap-x-1">{cache}</div> : null}
          {row.per_request_price > 0 ? (
            <div className="flex flex-wrap gap-x-1">
              <span className="whitespace-nowrap font-medium text-warning">
                {t('model_market.price_short_per_request')} {fmtPrice(row.per_request_price)}
              </span>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

export default function ModelMarketPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const site = useSiteSettings();
  const isLoggedIn = !!getToken();

  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword.trim(), 250);
  const [tagFilter, setTagFilter] = useState<string | null>(null);

  const listQuery = useMemo(() => ({
    page: 1,
    page_size: MARKET_PAGE_SIZE,
    keyword: debouncedKeyword || undefined,
  }), [debouncedKeyword]);

  const { data, isLoading } = useQuery({
    queryKey: queryKeys.modelMarket(listQuery),
    queryFn: () => modelMarketApi.list(listQuery),
  });

  const allRows = data?.list ?? [];
  // 标签筛选在客户端做：公开接口不额外提供标签列表端点，标签集合直接从当前结果里去重派生。
  const tags = useMemo(() => {
    const seen = new Map<string, string>();
    for (const row of allRows) {
      if (row.tag) seen.set(row.tag.name, row.tag.name);
    }
    return [...seen.keys()];
  }, [allRows]);
  const rows = useMemo(
    () => (tagFilter ? allRows.filter((row) => row.tag?.name === tagFilter) : allRows),
    [allRows, tagFilter],
  );

  return (
    <div className="relative flex min-h-screen flex-col bg-bg text-text">
      <div className="relative z-10 flex-1">
        <nav className="mx-auto flex max-w-6xl items-center justify-between px-6 py-4 md:px-12">
          <div className="flex items-center gap-2.5">
            <img src={site.site_logo || defaultLogoUrl} alt="" className="h-8 w-8 rounded-[var(--radius-md)] object-cover" />
            <span className="font-display text-base font-semibold tracking-tight">{site.site_name || 'AirGate'}</span>
          </div>
          <div className="flex items-center gap-2">
            <Button size="sm" variant="ghost" onPress={() => navigate({ to: '/home' })}>
              {t('model_market.back_home')}
            </Button>
            <Button size="sm" variant="primary" onPress={() => navigate({ to: isLoggedIn ? '/' : '/login' })}>
              {isLoggedIn ? t('home.go_dashboard') : t('home.login')}
            </Button>
          </div>
        </nav>

        <section className="mx-auto max-w-6xl px-6 pb-6 pt-10 md:px-12">
          <div className="mb-2 inline-flex items-center gap-2">
            <Sparkles className="h-3.5 w-3.5 text-text-tertiary" strokeWidth={2.25} />
            <span className="ag-kicker">{t('model_market.badge')}</span>
          </div>
          <h1 className="font-display mb-2 text-2xl font-medium tracking-tight text-text md:text-3xl">
            {t('model_market.title')}
          </h1>
          <p className="max-w-2xl text-sm text-text-tertiary">{t('model_market.subtitle')}</p>

          {data?.multiplier ? (
            <div className="mt-4 inline-flex items-center gap-2 rounded-[var(--field-radius)] border border-[var(--ag-glass-border)] bg-[var(--ag-glass)] px-4 py-2 text-sm">
              <span className="text-text-secondary">
                {t('model_market.multiplier_hint', { min: data.multiplier.min, max: data.multiplier.max })}
              </span>
            </div>
          ) : null}

          <div className="mt-6 flex flex-col gap-3 sm:flex-row sm:items-center">
            <div className="relative w-full sm:w-64">
              <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
              <Input
                aria-label={t('common.search')}
                className="pl-9"
                placeholder={t('model_market.search_placeholder')}
                value={keyword}
                onChange={(event) => setKeyword(event.target.value)}
              />
            </div>
            {tags.length > 0 ? (
              <div className="flex flex-wrap items-center gap-1.5">
                <button
                  className={`rounded-[var(--radius)] border px-2.5 py-1 text-xs transition-colors ${
                    tagFilter == null
                      ? 'border-primary bg-primary-subtle font-medium text-primary'
                      : 'border-border bg-surface text-text-secondary hover:text-text'
                  }`}
                  type="button"
                  onClick={() => setTagFilter(null)}
                >
                  {t('common.all')}
                </button>
                {tags.map((name) => (
                  <button
                    className={`rounded-[var(--radius)] border px-2.5 py-1 text-xs transition-colors ${
                      tagFilter === name
                        ? 'border-primary bg-primary-subtle font-medium text-primary'
                        : 'border-border bg-surface text-text-secondary hover:text-text'
                    }`}
                    key={name}
                    type="button"
                    onClick={() => setTagFilter(tagFilter === name ? null : name)}
                  >
                    {name}
                  </button>
                ))}
              </div>
            ) : null}
          </div>
        </section>

        <section className="mx-auto max-w-6xl px-6 pb-20 md:px-12">
          {isLoading ? (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {Array.from({ length: 6 }, (_, i) => (
                <div className="animate-pulse rounded-[var(--ag-radius-lg)] border border-border bg-surface p-5" key={i}>
                  <div className="h-4 w-2/3 rounded bg-border/60" />
                  <div className="mt-5 grid grid-cols-2 gap-3">
                    <div className="h-8 rounded bg-border/50" />
                    <div className="h-8 rounded bg-border/50" />
                  </div>
                </div>
              ))}
            </div>
          ) : rows.length === 0 ? (
            <div className="rounded-[var(--ag-radius-lg)] border border-border bg-surface py-16">
              <EmptyState>
                <span className="flex h-10 w-10 items-center justify-center rounded-full border border-border bg-surface">
                  <Inbox className="h-5 w-5 text-text-tertiary" />
                </span>
                <div className="text-sm text-text-secondary">{t('common.no_data')}</div>
              </EmptyState>
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {rows.map((row) => <MarketPriceCard key={row.model} row={row} t={t} />)}
            </div>
          )}

          {!isLoggedIn && rows.length > 0 ? (
            <div className="mt-10 flex flex-col items-center gap-3 rounded-[var(--ag-radius-lg)] border border-[var(--ag-glass-border)] bg-[var(--ag-glass)] px-6 py-8 text-center">
              <p className="text-sm text-text-secondary">{t('model_market.login_cta_desc')}</p>
              <Button variant="primary" onPress={() => navigate({ to: '/login' })}>
                {t('model_market.login_cta')}
              </Button>
            </div>
          ) : null}
        </section>
      </div>

      <footer className="relative z-10 border-t border-[var(--ag-glass-border)] py-5 text-center">
        <div className="flex items-center justify-center gap-4 text-xs text-text-tertiary">
          <span>© {new Date().getFullYear()} {site.site_name || 'AirGate'}</span>
        </div>
      </footer>
    </div>
  );
}
