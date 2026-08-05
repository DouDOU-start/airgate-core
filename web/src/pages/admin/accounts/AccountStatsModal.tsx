import { useEffect, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Modal, Spinner, useOverlayState } from '@heroui/react';
import {
  Activity,
  AlertTriangle,
  BarChart3,
  CalendarDays,
  Coins,
  MousePointerClick,
  PackageSearch,
} from 'lucide-react';
import { accountsApi, type AccountUsageStats } from '../../../shared/api/accounts';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import type { AccountResp } from '../../../shared/types';
import { AccountSummaryCard } from './AccountSummaryCard';

function fmtNum(n: number) {
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 }).format(n || 0);
}

function fmtCost(n: number) {
  return (n || 0).toFixed(4);
}

function fmtTokens(n: number) {
  if (!n) return '0';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(n);
}

function fmtDuration(ms: number) {
  if (!ms || ms < 0) return '—';
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}

/** 账号使用统计弹窗：近 30 天汇总、日趋势与模型分布。 */
export function AccountStatsModal({
  account,
  onClose,
}: {
  account: AccountResp | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const open = !!account;
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (next) => {
      if (!next) onClose();
    },
  });
  const [loading, setLoading] = useState(false);
  const [stats, setStats] = useState<AccountUsageStats | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!account) {
      setStats(null);
      setError('');
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError('');
    setStats(null);
    void accountsApi
      .stats(account.id, 30)
      .then((data) => {
        if (!cancelled) setStats(data);
      })
      .catch((err: Error) => {
        if (!cancelled) setError(err.message || t('accounts.stats_load_failed'));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [account?.id, t]);

  const s = stats?.summary;
  const hasHistory = (stats?.history.length ?? 0) > 0;
  const hasModels = (stats?.models.length ?? 0) > 0;
  const maxCost = Math.max(0, ...(stats?.history.map((item) => item.actual_cost) || [0]));
  const maxReq = Math.max(0, ...(stats?.history.map((item) => item.requests) || [0]));

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" size="lg" scroll="inside">
          <Modal.Dialog className="ag-elevation-modal ag-account-stats-modal">
            <Modal.Header>
              <Modal.Heading>{t('accounts.stats_title')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body className="ag-account-stats-modal__body">
              {account ? (
                <AccountSummaryCard
                  account={account}
                  context={t('accounts.stats_last_30_days')}
                />
              ) : null}

              {loading ? (
                <div className="ag-account-stats-state">
                  <Spinner />
                  <span>{t('common.loading')}</span>
                </div>
              ) : null}

              {!loading && error ? (
                <div className="ag-account-stats-state" data-status="error">
                  <AlertTriangle className="h-5 w-5" aria-hidden="true" />
                  <span>{error}</span>
                </div>
              ) : null}

              {!loading && !error && stats && s ? (
                <>
                  <div className="ag-account-stats-kpis">
                    <StatCard
                      icon={<Coins className="h-4 w-4" />}
                      label={t('accounts.stats_total_cost')}
                      value={`$${fmtCost(s.total_cost)}`}
                      hint={`${t('accounts.stats_user_cost')} $${fmtCost(s.total_user_cost)}`}
                      tone="emerald"
                    />
                    <StatCard
                      icon={<MousePointerClick className="h-4 w-4" />}
                      label={t('accounts.stats_total_requests')}
                      value={fmtNum(s.total_requests)}
                      hint={t('accounts.stats_total_calls')}
                      tone="blue"
                    />
                    <StatCard
                      icon={<CalendarDays className="h-4 w-4" />}
                      label={t('accounts.stats_avg_daily_cost')}
                      value={`$${fmtCost(s.avg_daily_cost)}`}
                      hint={t('accounts.stats_based_on_days', { days: s.actual_days_used })}
                      tone="amber"
                    />
                    <StatCard
                      icon={<Activity className="h-4 w-4" />}
                      label={t('accounts.stats_avg_daily_requests')}
                      value={fmtNum(Math.round(s.avg_daily_requests))}
                      hint={t('accounts.stats_avg_daily_usage')}
                      tone="indigo"
                    />
                  </div>

                  <div className="ag-account-stats-insights">
                    <InfoCard title={t('accounts.stats_today')}>
                      <Row label={t('accounts.stats_cost')} value={`$${fmtCost(s.today?.cost || 0)}`} />
                      <Row label={t('accounts.stats_user_cost')} value={`$${fmtCost(s.today?.user_cost || 0)}`} />
                      <Row label={t('accounts.stats_requests')} value={fmtNum(s.today?.requests || 0)} />
                      <Row label={t('accounts.stats_tokens')} value={fmtTokens(s.today?.tokens || 0)} />
                    </InfoCard>
                    <InfoCard title={t('accounts.stats_highest_cost_day')}>
                      <Row label={t('accounts.stats_date')} value={s.highest_cost_day?.label || '—'} />
                      <Row label={t('accounts.stats_cost')} value={`$${fmtCost(s.highest_cost_day?.cost || 0)}`} />
                      <Row label={t('accounts.stats_requests')} value={fmtNum(s.highest_cost_day?.requests || 0)} />
                    </InfoCard>
                    <InfoCard title={t('accounts.stats_performance')}>
                      <Row label={t('accounts.stats_total_tokens')} value={fmtTokens(s.total_tokens)} />
                      <Row label={t('accounts.stats_avg_duration')} value={fmtDuration(s.avg_duration_ms)} />
                      <Row
                        label={t('accounts.stats_days_active')}
                        value={`${s.actual_days_used} / ${s.days}`}
                      />
                    </InfoCard>
                  </div>

                  <div className="ag-account-stats-visuals">
                    <section className="ag-account-stats-panel">
                      <div className="ag-account-stats-panel__header">
                        <div className="ag-account-stats-panel__title">
                          <span className="ag-account-stats-panel__icon" data-tone="blue">
                            <BarChart3 className="h-4 w-4" aria-hidden="true" />
                          </span>
                          <h3>{t('accounts.stats_usage_trend')}</h3>
                        </div>
                        {hasHistory ? (
                          <div className="ag-account-stats-legend">
                            <span>
                              <i data-tone="cost" />
                              {t('accounts.stats_cost')}
                            </span>
                            <span>
                              <i data-tone="request" />
                              {t('accounts.stats_requests')}
                            </span>
                          </div>
                        ) : null}
                      </div>

                      {!hasHistory ? (
                        <StatsEmpty
                          icon={<BarChart3 className="h-5 w-5" />}
                          label={t('accounts.stats_no_data')}
                        />
                      ) : (
                        <div className="ag-account-stats-trend-scroll">
                          <div className="ag-account-stats-trend">
                            {stats.history.map((item) => {
                              const costHeight = maxCost > 0 ? (item.actual_cost / maxCost) * 100 : 0;
                              const requestHeight = maxReq > 0 ? (item.requests / maxReq) * 100 : 0;
                              return (
                                <div
                                  key={item.date}
                                  className="ag-account-stats-trend__day"
                                  title={`${item.date}\n${t('accounts.stats_cost')} $${fmtCost(item.actual_cost)} · ${t('accounts.stats_requests')} ${fmtNum(item.requests)} · ${t('accounts.stats_tokens')} ${fmtTokens(item.tokens)}`}
                                >
                                  <div className="ag-account-stats-trend__bars">
                                    <i
                                      data-tone="cost"
                                      style={{ height: `${Math.max(costHeight, item.actual_cost > 0 ? 4 : 0)}%` }}
                                    />
                                    <i
                                      data-tone="request"
                                      style={{ height: `${Math.max(requestHeight, item.requests > 0 ? 4 : 0)}%` }}
                                    />
                                  </div>
                                  <span>{item.label}</span>
                                </div>
                              );
                            })}
                          </div>
                        </div>
                      )}
                    </section>

                    <section className="ag-account-stats-panel">
                      <div className="ag-account-stats-panel__header">
                        <div className="ag-account-stats-panel__title">
                          <span className="ag-account-stats-panel__icon" data-tone="amber">
                            <PackageSearch className="h-4 w-4" aria-hidden="true" />
                          </span>
                          <h3>{t('accounts.stats_model_dist')}</h3>
                        </div>
                      </div>

                      {!hasModels ? (
                        <StatsEmpty
                          icon={<PackageSearch className="h-5 w-5" />}
                          label={t('accounts.stats_no_data')}
                        />
                      ) : (
                        <div className="ag-account-stats-models">
                          {stats.models.slice(0, 12).map((item) => {
                            const percentage = s.total_requests > 0
                              ? Math.round((item.requests / s.total_requests) * 100)
                              : 0;
                            return (
                              <div key={item.model} className="ag-account-stats-model">
                                <div className="ag-account-stats-model__header">
                                  <span title={item.model}>{item.model}</span>
                                  <strong>
                                    {fmtNum(item.requests)} · ${fmtCost(item.actual_cost)} · {percentage}%
                                  </strong>
                                </div>
                                <div className="ag-account-stats-model__track">
                                  <i
                                    style={{ width: `${Math.max(percentage, item.requests > 0 ? 2 : 0)}%` }}
                                  />
                                </div>
                              </div>
                            );
                          })}
                        </div>
                      )}
                    </section>
                  </div>
                </>
              ) : null}
            </Modal.Body>
            <Modal.Footer className="flex justify-end">
              <Button variant="secondary" onPress={onClose}>
                {t('common.close')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

function StatCard({
  icon,
  label,
  value,
  hint,
  tone,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  hint?: string;
  tone: 'emerald' | 'blue' | 'amber' | 'indigo';
}) {
  return (
    <div className="ag-account-stats-kpi" data-tone={tone}>
      <div className="ag-account-stats-kpi__header">
        <span className="ag-account-stats-kpi__icon">{icon}</span>
        <span>{label}</span>
      </div>
      <strong>{value}</strong>
      {hint ? <small>{hint}</small> : null}
    </div>
  );
}

function InfoCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="ag-account-stats-insight">
      <h3>{title}</h3>
      <div>{children}</div>
    </section>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="ag-account-stats-row">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function StatsEmpty({ icon, label }: { icon: ReactNode; label: string }) {
  return (
    <div className="ag-account-stats-empty">
      <span>{icon}</span>
      <p>{label}</p>
    </div>
  );
}
