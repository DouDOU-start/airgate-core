import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from '@tanstack/react-router';
import { Alert, Button, Card, Chip } from '@heroui/react';
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
} from 'lucide-react';
import type { TestDBReq, TestRedisReq, AdminSetup } from '../../shared/types';

export interface StepFinishProps {
  dbConfig: TestDBReq;
  redisConfig: TestRedisReq;
  adminConfig: AdminSetup;
  // 这两个标志由 env 注入触发：摘要里只展示主机/端口，不显示密码占位
  envDBProvided?: boolean;
  envRedisProvided?: boolean;
  onPrev: () => void;
}

export default function StepFinish({ dbConfig, redisConfig, adminConfig, envDBProvided, envRedisProvided, onPrev }: StepFinishProps) {
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
      fromEnv: envDBProvided,
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
      fromEnv: envRedisProvided,
      details: [
        { label: t('setup.config_host'), value: `${redisConfig.host}:${redisConfig.port}` },
        { label: t('setup.config_database'), value: String(redisConfig.db ?? 0) },
        { label: t('setup.config_tls'), value: redisConfig.tls ? t('common.enable') : t('common.disable') },
      ],
    },
    {
      icon: UserCog,
      title: t('setup.config_summary_admin'),
      fromEnv: false,
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
            <Card key={item.title}>
              <Card.Content className="p-4">
                <div className="flex items-center gap-2 mb-3">
                  <Icon className="w-4 h-4 text-primary" />
                  <h4 className="text-sm font-semibold text-text">{item.title}</h4>
                  {item.fromEnv && (
                    <Chip className="ml-auto font-mono uppercaser" color="accent" size="sm" variant="soft">
                      {t('setup.from_env')}
                    </Chip>
                  )}
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
              </Card.Content>
            </Card>
          );
        })}
      </div>

      {/* 安装状态 */}
      {status === 'installing' && (
        <Alert status="accent">
          <Alert.Content>
            <Alert.Description>
              <span className="flex items-center gap-2">
                <Loader2 className="w-4 h-4 animate-spin" />
                {t('setup.installing')}
              </span>
            </Alert.Description>
          </Alert.Content>
        </Alert>
      )}
      {status === 'restarting' && (
        <Alert status="warning">
          <Alert.Content>
            <Alert.Description>
              <span className="flex items-center gap-2">
                <RefreshCw className="w-4 h-4 animate-spin" />
                {t('setup.install_waiting')}
              </span>
            </Alert.Description>
          </Alert.Content>
        </Alert>
      )}
      {status === 'done' && (
        <Alert status="success">
          <Alert.Content>
            <Alert.Description>
              <span className="flex items-center gap-2">
                <CheckCircle2 className="w-4 h-4" />
                {t('setup.install_complete')}
              </span>
            </Alert.Description>
          </Alert.Content>
        </Alert>
      )}
      {status === 'error' && (
        <Alert status="danger">
          <Alert.Content>
            <Alert.Description>
              {t('setup.install_failed')}:{errorMsg}
            </Alert.Description>
          </Alert.Content>
        </Alert>
      )}

      {/* 操作按钮 */}
      <div className="flex justify-between pt-2">
        <Button
          variant="ghost"
          onPress={onPrev}
          isDisabled={installing}
        >
          <ArrowLeft className="w-4 h-4" />
          {t('setup.step_admin')}
        </Button>
        <Button
          onPress={handleInstall}
          isDisabled={installing || status === 'done'}
          aria-busy={installing}
        >
          <Play className="w-4 h-4" />
          {status === 'idle' || status === 'error' ? t('setup.run_install') : t('setup.installing_btn')}
        </Button>
      </div>
    </div>
  );
}
