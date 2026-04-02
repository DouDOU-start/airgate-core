import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from '@tanstack/react-router';
import { Button } from '../../shared/components/Button';
import { setupApi } from '../../shared/api/setup';
import { resetSetupCache } from '../../app/router';
import {
  Database,
  Server,
  UserCog,
  CheckCircle2,
  ArrowLeft,
  Play,
  Loader2,
  RefreshCw,
  CircleDot,
} from 'lucide-react';
import type { TestDBReq, TestRedisReq, AdminSetup } from '../../shared/types';

export interface StepFinishProps {
  dbConfig: TestDBReq;
  redisConfig: TestRedisReq;
  adminConfig: AdminSetup;
  onPrev: () => void;
}

export default function StepFinish({ dbConfig, redisConfig, adminConfig, onPrev }: StepFinishProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [installing, setInstalling] = useState(false);
  const [status, setStatus] = useState<'idle' | 'installing' | 'restarting' | 'done' | 'error'>('idle');
  const [errorMsg, setErrorMsg] = useState('');

  // 轮询服务状态，等待重启完成
  const pollStatus = () => {
    setStatus('restarting');
    const maxAttempts = 30;
    let attempt = 0;

    const poll = () => {
      attempt++;
      setupApi
        .status()
        .then((resp) => {
          if (!resp.needs_setup) {
            setStatus('done');
            resetSetupCache();
            setTimeout(() => navigate({ to: '/login' }), 1500);
          } else if (attempt < maxAttempts) {
            setTimeout(poll, 2000);
          } else {
            setStatus('done');
            resetSetupCache();
            setTimeout(() => navigate({ to: '/login' }), 1500);
          }
        })
        .catch(() => {
          if (attempt < maxAttempts) {
            setTimeout(poll, 2000);
          } else {
            setStatus('done');
            resetSetupCache();
            setTimeout(() => navigate({ to: '/login' }), 1500);
          }
        });
    };

    setTimeout(poll, 3000);
  };

  const handleInstall = async () => {
    setInstalling(true);
    setStatus('installing');
    setErrorMsg('');
    try {
      await setupApi.install({
        database: dbConfig,
        redis: redisConfig,
        admin: adminConfig,
      });
      pollStatus();
    } catch (err) {
      setStatus('error');
      setErrorMsg(err instanceof Error ? err.message : t('setup.install_failed'));
      setInstalling(false);
    }
  };

  // 配置摘要项
  const summaryItems = [
    {
      icon: Database,
      title: t('setup.config_summary_db'),
      details: [
        { label: t('setup.config_host'), value: `${dbConfig.host}:${dbConfig.port}` },
        { label: t('setup.config_user'), value: dbConfig.user },
        { label: t('setup.config_database'), value: dbConfig.dbname },
        { label: t('setup.config_ssl'), value: dbConfig.sslmode || 'disable' },
      ],
    },
    {
      icon: Server,
      title: t('setup.config_summary_redis'),
      details: [
        { label: t('setup.config_host'), value: `${redisConfig.host}:${redisConfig.port}` },
        { label: t('setup.config_database'), value: String(redisConfig.db ?? 0) },
{ label: t('setup.config_tls'), value: redisConfig.tls ? t('common.enable') : t('common.disable') },
      ],
    },
    {
      icon: UserCog,
      title: t('setup.config_summary_admin'),
      details: [
        { label: t('setup.config_email'), value: adminConfig.email },
      ],
    },
  ];

  return (
    <div className="space-y-6">
      <p className="text-sm text-text-secondary">
        {t('setup.confirm_config')}
      </p>

      {/* 配置摘要 */}
      <div className="space-y-3">
        {summaryItems.map((item) => {
          const Icon = item.icon;
          return (
            <div
              key={item.title}
              className="border border-glass-border bg-bg-elevated shadow-sm rounded-lg p-4"
            >
              <div className="flex items-center gap-2 mb-3">
                <Icon className="w-4 h-4 text-primary" />
                <h4 className="text-sm font-semibold text-text">{item.title}</h4>
              </div>
              <div className="grid grid-cols-2 gap-x-4 gap-y-1.5">
                {item.details.map((d) => (
                  <div key={d.label} className="flex items-center gap-2 text-xs">
                    <span className="text-text-tertiary">{d.label}:</span>
                    <span className="text-text-secondary font-mono">
                      {d.value}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          );
        })}
      </div>

      {/* 安装状态 */}
      {status === 'installing' && (
        <div
          className="flex items-center gap-2.5 rounded-md px-4 py-3 text-sm"
          style={{
            background: 'var(--ag-info-subtle)',
            color: 'var(--ag-info)',
            borderLeft: '3px solid var(--ag-info)',
          }}
        >
          <Loader2 className="w-4 h-4 animate-spin" />
          {t('setup.installing')}
        </div>
      )}
      {status === 'restarting' && (
        <div
          className="flex items-center gap-2.5 rounded-md px-4 py-3 text-sm"
          style={{
            background: 'var(--ag-warning-subtle)',
            color: 'var(--ag-warning)',
            borderLeft: '3px solid var(--ag-warning)',
          }}
        >
          <RefreshCw className="w-4 h-4 animate-spin" />
          {t('setup.install_waiting')}
        </div>
      )}
      {status === 'done' && (
        <div
          className="relative overflow-hidden rounded-md px-4 py-3 text-sm"
          style={{
            background: 'var(--ag-success-subtle)',
            color: 'var(--ag-success)',
            borderLeft: '3px solid var(--ag-success)',
          }}
        >
          {/* 成功发光效果 */}
          <div
            className="absolute inset-0 opacity-30 animate-pulse"
            style={{ background: 'radial-gradient(circle at center, var(--ag-success), transparent 70%)' }}
          />
          <div className="relative flex items-center gap-2.5">
            <CheckCircle2 className="w-4 h-4" />
            {t('setup.install_complete')}
          </div>
        </div>
      )}
      {status === 'error' && (
        <div
          className="flex items-start gap-2.5 rounded-md px-4 py-3 text-sm"
          style={{
            background: 'var(--ag-danger-subtle)',
            color: 'var(--ag-danger)',
            borderLeft: '3px solid var(--ag-danger)',
          }}
        >
          <CircleDot className="w-4 h-4 mt-0.5 shrink-0" />
          {t('setup.install_failed')}:{errorMsg}
        </div>
      )}

      {/* 操作按钮 */}
      <div className="flex justify-between pt-2">
        <Button
          variant="ghost"
          onClick={onPrev}
          disabled={installing}
          icon={<ArrowLeft className="w-4 h-4" />}
        >
          {t('setup.step_admin')}
        </Button>
        <Button
          onClick={handleInstall}
          loading={installing}
          disabled={status === 'done'}
          icon={<Play className="w-4 h-4" />}
        >
          {status === 'idle' || status === 'error' ? t('setup.run_install') : t('setup.installing_btn')}
        </Button>
      </div>
    </div>
  );
}
