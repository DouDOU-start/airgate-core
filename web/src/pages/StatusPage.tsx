import { useEffect, useState } from 'react';
import { Link } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { Activity, ArrowLeft, RefreshCw } from 'lucide-react';
import { useAuth } from '../app/providers/AuthProvider';
import { useSiteSettings, defaultLogoUrl } from '../app/providers/SiteSettingsProvider';
import { Alert } from '../shared/components/Alert';
import { Card } from '../shared/components/Card';
import { AppShell } from '../app/layout/AppShell';

// 状态页（SPA 内）
//
// 数据源：/status/api/summary?window=7d  （core 反向代理到 airgate-health 插件，无需登录）
// 脱敏：只展示 platform 维度的可用率与状态色
//
// 同一组件用于两种场景：
//   - 登录后：通过路由 /status 进入，被 AppShell 包裹，体验与其他页面一致
//   - 未登录：从登录页底部 Link 进入，独立渲染，顶部加一个返回登录的入口

interface DailyPoint {
  date: string;
  total: number;
  success: number;
  uptime_pct: number;
}

interface GroupHealth {
  group_id: number;
  group_name: string;
  platform: string;
  note?: string;
  uptime_pct: number;
  latency_p95: number;
  status_color: 'green' | 'yellow' | 'red' | 'gray';
  daily?: DailyPoint[];
}

interface Summary {
  window: string;
  groups: GroupHealth[];
}

function StatusContent({ hideHeader = false }: { hideHeader?: boolean } = {}) {
  const { t } = useTranslation();
  const [data, setData] = useState<Summary | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const reload = () => {
    setLoading(true);
    fetch('/status/api/summary?window=7d', {
      headers: { Accept: 'application/json' },
    })
      .then(async (r) => {
        const ct = r.headers.get('content-type') || '';
        if (!r.ok) {
          // 后端 / 插件未就绪：404 / 503 / HTML 占位
          if (r.status === 404) {
            throw new Error('状态页未启用：请管理员在「健康监控」插件设置中打开「公开状态页」开关');
          }
          throw new Error(`HTTP ${r.status}`);
        }
        if (!ct.includes('application/json')) {
          // 拿到 HTML（多半是 SPA fallback / 反代未生效）
          throw new Error('接口未生效：后端 /status/api 未代理到健康监控插件，请确认后端已重启或 vite 已重启');
        }
        return r.json();
      })
      .then((d) => {
        setData(d);
        setErr(null);
      })
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    reload();
    const t = setInterval(reload, 60_000);
    return () => clearInterval(t);
  }, []);

  return (
    <div className="space-y-4">
      {/* 顶部说明 + 刷新 */}
      {!hideHeader && (
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-xl font-semibold text-text flex items-center gap-2">
              <Activity className="w-5 h-5 text-primary" />
              {t('status_page.title')}
            </h1>
            <p className="text-xs text-text-tertiary mt-1">{t('status_page.subtitle')}</p>
          </div>
          <button
            onClick={reload}
            disabled={loading}
            className="inline-flex items-center gap-1.5 px-3 h-8 rounded-[10px] text-xs text-text-secondary hover:text-text bg-bg-hover hover:bg-bg-elevated border border-glass-border transition-colors disabled:opacity-50"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
            {t('status_page.refresh')}
          </button>
        </div>
      )}

      {err && <Alert variant="error">{t('status_page.load_failed')}: {err}</Alert>}

      {!data && !err && (
        <Card>
          <div className="text-center text-text-tertiary text-sm py-8">{t('status_page.loading')}</div>
        </Card>
      )}

      {data && (!data.groups || data.groups.length === 0) && (
        <Card>
          <div className="text-center text-text-tertiary text-sm py-8">{t('status_page.no_monitor_data')}</div>
        </Card>
      )}

      {data && data.groups && data.groups.length > 0 && (
        <div
          className="grid gap-4"
          style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(360px, 1fr))' }}
        >
          {data.groups.map((g) => <GroupCard key={g.group_id} g={g} />)}
        </div>
      )}
    </div>
  );
}

function GroupCard({ g }: { g: GroupHealth }) {
  const { t } = useTranslation();
  const dotClass =
    g.status_color === 'green'
      ? 'bg-success shadow-[0_0_12px_var(--ag-success)]'
      : g.status_color === 'yellow'
        ? 'bg-warning shadow-[0_0_12px_var(--ag-warning)]'
        : g.status_color === 'red'
          ? 'bg-danger shadow-[0_0_12px_var(--ag-danger)]'
          : 'bg-text-tertiary';

  const statusText =
    g.status_color === 'green'
      ? t('status_page.operational')
      : g.status_color === 'yellow'
        ? t('status_page.degraded')
        : g.status_color === 'red'
          ? t('status_page.outage')
          : t('status_page.unknown');

  const statusFg =
    g.status_color === 'green'
      ? 'text-success'
      : g.status_color === 'yellow'
        ? 'text-warning'
        : g.status_color === 'red'
          ? 'text-danger'
          : 'text-text-tertiary';

  return (
    <Card>
      {/* 头部：状态点 + 分组名(备注) + 平台标签 + 状态文本 + 可用率 */}
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-3 min-w-0">
          <span className={`inline-block w-2.5 h-2.5 rounded-full flex-shrink-0 ${dotClass}`} />
          <strong className="text-base font-semibold text-text truncate">
            {g.group_name}
            {g.note && (
              <span className="ml-1.5 font-normal text-text-secondary">({g.note})</span>
            )}
          </strong>
          <span className="text-xs text-text-tertiary hidden sm:inline flex-shrink-0">· {g.platform}</span>
          <span className={`text-xs font-medium ${statusFg} hidden sm:inline mt-0.5 flex-shrink-0`}>{statusText}</span>
        </div>
        <div className="text-xl font-semibold text-text tabular-nums tracking-tight flex-shrink-0">
          {g.uptime_pct < 0 ? '—' : g.uptime_pct.toFixed(2) + '%'}
        </div>
      </div>


      {/* 90 天日级方格条 */}
      <DailyGrid daily={g.daily || []} />

      {/* 底部元信息：p95 延迟 */}
      <div className="flex items-center justify-end text-xs text-text-secondary mt-3">
        <span className="tabular-nums">p95 {g.latency_p95}ms</span>
      </div>
    </Card>
  );
}

// 把 daily 数组按日期补齐到 90 格（缺数据用 placeholder 占位）
// 这样无论实际监控了多少天，方格条永远是 90 列，视觉一致
function padDailyTo90(daily: DailyPoint[]): DailyPoint[] {
  const SLOTS = 90;
  // 用日期字符串建索引，方便查找
  const byDate = new Map<string, DailyPoint>();
  for (const d of daily) byDate.set(d.date, d);

  // 以"今天"为锚点向前推 89 天
  const today = new Date();
  today.setHours(0, 0, 0, 0);

  const result: DailyPoint[] = [];
  for (let i = SLOTS - 1; i >= 0; i--) {
    const dt = new Date(today);
    dt.setDate(today.getDate() - i);
    const key = formatDate(dt);
    const existing = byDate.get(key);
    if (existing) {
      result.push(existing);
    } else {
      result.push({ date: key, total: 0, success: 0, uptime_pct: 0 });
    }
  }
  return result;
}

function formatDate(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

function DailyGrid({ daily }: { daily: DailyPoint[] }) {
  const { t } = useTranslation();
  const slots = padDailyTo90(daily);

  return (
    <div className="mt-4">
      <div className="flex gap-[2px] h-[28px]">
        {slots.map((d) => {
          const cls =
            d.total === 0
              ? 'bg-bg-hover opacity-30'
              : d.uptime_pct >= 99.5
                ? 'bg-success hover:brightness-110'
                : d.uptime_pct >= 95
                  ? 'bg-warning hover:brightness-110'
                  : 'bg-danger hover:brightness-110';
          return (
            <div
              key={d.date}
              title={`${d.date} · ${
                d.total === 0
                  ? t('status_page.no_data_short')
                  : d.uptime_pct.toFixed(2) + '% (' + d.success + '/' + d.total + ')'
              }`}
              className={`flex-1 min-w-0 rounded-[2px] transition-all cursor-default ${cls}`}
            />
          );
        })}
      </div>
      {/* 时间轴：90 days ago ←—————→ Today */}
      <div className="flex items-center justify-between mt-2 text-[10px] text-text-tertiary">
        <span>{t('status_page.days_ago', { count: 90 })}</span>
        <div className="flex-1 mx-3 h-px bg-border" />
        <span>{t('status_page.today')}</span>
      </div>
    </div>
  );
}

/**
 * 公开入口（未登录访问），独立布局，顶部放一个返回登录的入口
 */
export default function StatusPage() {
  const { user } = useAuth();
  const { t } = useTranslation();
  const site = useSiteSettings();

  // 已登录：套 AppShell，与其他页面一致
  if (user) {
    return (
      <AppShell>
        <StatusContent />
      </AppShell>
    );
  }

  // 未登录：独立全屏布局，带 hero + 背景光晕
  return (
    <div className="min-h-screen bg-bg-deep text-text relative overflow-hidden">
      {/* 背景装饰光晕 */}
      <div className="absolute inset-0 pointer-events-none overflow-hidden">
        <div
          className="absolute -top-[300px] left-1/2 -translate-x-1/2 w-[800px] h-[800px] rounded-full opacity-[0.07]"
          style={{ background: 'radial-gradient(circle, var(--ag-primary), transparent 65%)' }}
        />
        <div
          className="absolute top-[40%] -right-[200px] w-[500px] h-[500px] rounded-full opacity-[0.04]"
          style={{ background: 'radial-gradient(circle, var(--ag-info), transparent 65%)' }}
        />
      </div>

      {/* 顶部导航 */}
      <nav className="relative z-10 max-w-5xl mx-auto px-6 md:px-8 py-5 flex items-center justify-between">
        <Link to="/home" className="flex items-center gap-2.5 hover:opacity-80 transition-opacity">
          <img
            src={site.site_logo || defaultLogoUrl}
            alt=""
            className="w-8 h-8 rounded-sm object-cover"
          />
          <span className="text-base font-bold tracking-tight">
            {site.site_name || 'AirGate'}
          </span>
        </Link>
        <Link
          to="/login"
          className="inline-flex items-center gap-1.5 px-4 py-1.5 text-xs font-medium rounded-lg border border-glass-border text-text-secondary hover:text-text hover:bg-bg-hover transition-colors"
        >
          <ArrowLeft className="w-3.5 h-3.5" />
          {t('status_page.back_to_login')}
        </Link>
      </nav>

      {/* Hero */}
      <section className="relative z-10 max-w-5xl mx-auto px-6 md:px-8 pt-10 md:pt-16 pb-8 text-center">
        <div className="inline-flex items-center gap-2 px-4 py-1.5 rounded-full text-xs font-medium mb-5 border border-glass-border bg-bg-surface">
          <Activity className="w-3.5 h-3.5 text-primary" />
          <span className="text-text-secondary">{t('status_page.title')}</span>
        </div>
        <h1 className="text-3xl md:text-4xl font-bold tracking-tight mb-3">
          {site.site_name || 'AirGate'} Status
        </h1>
        <p className="text-sm md:text-base text-text-tertiary max-w-xl mx-auto leading-relaxed">
          {t('status_page.subtitle')}
        </p>
      </section>

      {/* 状态内容 */}
      <main className="relative z-10 max-w-5xl mx-auto px-6 md:px-8 pb-16">
        <StatusContent hideHeader />
      </main>

      {/* 底部 */}
      <footer className="relative z-10 border-t border-glass-border py-6 text-center">
        <div className="text-xs text-text-tertiary">
          Powered by {site.site_name || 'AirGate'}
        </div>
      </footer>
    </div>
  );
}
