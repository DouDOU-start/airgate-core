import { useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from '@tanstack/react-router';
import { useQuery } from '@tanstack/react-query';
import { Button, Chip, EmptyState, Input } from '@heroui/react';
import { Inbox, Languages, Moon, Search, Sparkles, Sun } from 'lucide-react';
import { modelMarketApi } from '../shared/api/modelMarket';
import { queryKeys } from '../shared/queryKeys';
import { useSiteSettings, defaultLogoUrl } from '../app/providers/SiteSettingsProvider';
import { useTheme } from '../app/providers/ThemeProvider';
import { useDebouncedValue } from '../shared/hooks/useDebouncedValue';
import { getToken } from '../shared/api/client';
import { setStoredLanguage } from '../i18n';
import type { ModelMarketItemResp } from '../shared/types';

type Translate = (key: string, options?: Record<string, unknown>) => string;

// 单次拉全量：模型广场是浏览/比价页，不做分页 UI；上限即后端 PageReq.page_size 允许的最大值。
const MARKET_PAGE_SIZE = 100;

function fmtPrice(value: number): string {
  if (!value) return '—';
  return `$${value}`;
}

// fmtPricePerM 单价单位为 USD / 1M tokens 的场景（缓存读取/写入），补上 /1M 后缀。
function fmtPricePerM(value: number): string {
  if (!value) return '—';
  return `${fmtPrice(value)}/1M`;
}

// fmtThreshold 阈值展示：>=1000 转 K 简写，与管理端模型卡片一致。
function fmtThreshold(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '';
  return value >= 1000 ? `>${value / 1000}K` : `>${value}`;
}

// tierLabel 已知服务档名复用管理端的翻译（优先 / 弹性），未知档名原样显示。
function tierLabel(name: string, t: Translate): string {
  const key = `model_prices.tier_${name}`;
  const label = t(key);
  return label === key ? name : label;
}

// longContextLine 长上下文阶梯独立行：阈值 + 各维度倍率，文案复用管理端 model_prices 命名空间。
function longContextLine(row: ModelMarketItemResp, t: Translate): ReactNode {
  const lc = row.long_context;
  if (!lc) return null;
  const threshold = fmtThreshold(lc.threshold_tokens);
  const parts: ReactNode[] = [];
  const push = (key: string, label: string, value: number) => {
    if (!Number.isFinite(value) || value <= 0) return;
    parts.push(
      <span className="whitespace-nowrap" key={key}>
        <span className="text-text-tertiary">{label} </span>
        <span className="font-medium">×{value}</span>
      </span>,
    );
  };
  push('in', t('model_prices.lc_input'), lc.input_multiplier);
  push('out', t('model_prices.lc_output'), lc.output_multiplier);
  push('cached', t('model_prices.lc_cached'), lc.cached_multiplier);
  if (!threshold || parts.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-x-1" title={t('model_prices.long_context_hint')}>
      <span className="whitespace-nowrap text-text-tertiary">
        {t('model_prices.pricing_extra_long_context')} <span className="font-medium text-text-secondary">{threshold}</span>：
      </span>
      {parts.map((p, i) => (i === 0 ? p : [<span className="text-text-tertiary/60" key={`s${i}`}> · </span>, p]))}
    </div>
  );
}

// serviceTiersLine 服务档倍率行（priority/flex 等），文案复用管理端 model_prices 命名空间。
function serviceTiersLine(row: ModelMarketItemResp, t: Translate): ReactNode {
  const tiers = row.service_tiers;
  if (!tiers) return null;
  const parts: ReactNode[] = [];
  for (const [name, mul] of Object.entries(tiers)) {
    if (!Number.isFinite(mul) || mul <= 0) continue;
    parts.push(
      <span className="whitespace-nowrap" key={name}>
        <span className="text-text-tertiary">{tierLabel(name, t)} </span>
        <span className="font-medium">×{mul}</span>
      </span>,
    );
  }
  if (parts.length === 0) return null;
  return parts.map((p, i) => (i === 0 ? p : [<span className="text-text-tertiary/60" key={`s${i}`}> · </span>, p]));
}

// imageSizePricesLine 图像分辨率价表：公开定价页逐档列出（质量·尺寸 $价/张），
// 键形态 "quality:size" 或裸 "size"，与后端计费口径一致。
function imageSizePricesLine(row: ModelMarketItemResp, t: Translate): ReactNode {
  const prices = row.image_size_prices;
  if (!prices) return null;
  const parts: ReactNode[] = [];
  for (const [key, price] of Object.entries(prices).sort(([a], [b]) => a.localeCompare(b))) {
    if (!Number.isFinite(price) || price <= 0) continue;
    parts.push(
      <span className="whitespace-nowrap" key={key}>
        <span className="text-text-tertiary">{key.replace(':', ' · ')} </span>
        <span className="font-medium text-warning">{fmtPrice(price)}</span>
      </span>,
    );
  }
  if (parts.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-x-1" title={t('model_market.image_size_prices_hint')}>
      <span className="whitespace-nowrap text-text-tertiary">{t('model_market.price_short_per_image')}：</span>
      {parts.map((p, i) => (i === 0 ? p : [<span className="text-text-tertiary/60" key={`s${i}`}> · </span>, p]))}
    </div>
  );
}

// cacheLine 缓存单价行，逻辑与管理端模型卡片一致。
function cacheLine(row: ModelMarketItemResp, t: Translate): ReactNode {
  const parts: ReactNode[] = [];
  const push = (key: string, label: string, value: number) => {
    if (value <= 0) return;
    parts.push(
      <span className="whitespace-nowrap" key={key}>
        <span className="text-text-tertiary">{label} </span>
        <span className="font-medium">{fmtPricePerM(value)}</span>
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
  const tiers = serviceTiersLine(row, t);
  const longContext = longContextLine(row, t);
  const imagePrices = imageSizePricesLine(row, t);
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
      {(cache || row.per_request_price > 0 || tiers || longContext || imagePrices) ? (
        <div className="mt-3 space-y-1 font-mono text-xs tabular-nums text-text-secondary">
          {cache ? <div className="flex flex-wrap gap-x-1">{cache}</div> : null}
          {row.per_request_price > 0 ? (
            <div className="flex flex-wrap gap-x-1">
              <span className="whitespace-nowrap font-medium text-warning">
                {t('model_market.price_short_per_request')} {fmtPrice(row.per_request_price)}
              </span>
            </div>
          ) : null}
          {imagePrices}
          {tiers ? <div className="flex flex-wrap gap-x-1">{tiers}</div> : null}
          {longContext}
        </div>
      ) : null}
    </div>
  );
}

export default function ModelMarketPage() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const site = useSiteSettings();
  const { theme, toggleTheme } = useTheme();
  const isLoggedIn = !!getToken();

  const toggleLanguage = () => {
    const nextLang = i18n.language === 'zh' ? 'en' : 'zh';
    i18n.changeLanguage(nextLang);
    setStoredLanguage(nextLang);
  };

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
            <Button
              aria-label={i18n.language === 'zh' ? 'Switch to English' : '切换为中文'}
              size="sm"
              variant="ghost"
              className="gap-1.5 px-2.5"
              onPress={toggleLanguage}
            >
              <Languages className="w-4 h-4" />
              <span className="font-mono text-xs uppercase">{i18n.language === 'zh' ? 'EN' : '中文'}</span>
            </Button>
            <Button
              aria-label={theme === 'dark' ? t('common.toggle_theme_light') : t('common.toggle_theme_dark')}
              isIconOnly
              size="sm"
              variant="ghost"
              onPress={toggleTheme}
            >
              {theme === 'dark' ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
            </Button>
            <Button size="sm" variant="primary" onPress={() => navigate({ to: isLoggedIn ? '/' : '/login' })}>
              {isLoggedIn ? t('home.go_dashboard') : t('home.login')}
            </Button>
          </div>
        </nav>

        <section className="mx-auto max-w-6xl px-6 pb-6 pt-10 md:px-12">
          <div className="mb-2 inline-flex items-center gap-2">
            <Sparkles className="h-5 w-5 text-text-tertiary" strokeWidth={2.25} />
            <span className="font-mono text-base font-medium uppercase tracking-[0.13em] text-text-tertiary">
              {t('model_market.badge')}
            </span>
          </div>
          <p className="max-w-2xl text-sm text-text-tertiary">{t('model_market.subtitle')}</p>

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
              <EmptyState className="flex flex-col items-center gap-2 text-center">
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
