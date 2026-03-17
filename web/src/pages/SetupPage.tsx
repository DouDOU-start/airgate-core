import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from '@tanstack/react-router';
import { Button } from '../shared/components/Button';
import { Input, Select } from '../shared/components/Input';
import { Card } from '../shared/components/Card';
import { setupApi } from '../shared/api/setup';
import { resetSetupCache } from '../app/router';
import {
  Database,
  Server,
  UserCog,
  CheckCircle2,
  Zap,
  ArrowLeft,
  ArrowRight,
  Play,
  Plug2,
  Loader2,
  RefreshCw,
  CircleDot,
  ShieldCheck,
} from 'lucide-react';
import type { TestDBReq, TestRedisReq, AdminSetup } from '../shared/types';

// ==================== 步骤配置 ====================

const STEP_KEYS = [
  { labelKey: 'setup.step_db', icon: Database },
  { labelKey: 'setup.step_redis', icon: Server },
  { labelKey: 'setup.step_admin', icon: UserCog },
  { labelKey: 'setup.step_finish', icon: CheckCircle2 },
] as const;

// ==================== 步骤指示器 ====================

function Stepper({ current }: { current: number }) {
  const { t } = useTranslation();

  return (
    <div className="flex items-center justify-center mb-10">
      {STEP_KEYS.map((step, index) => {
        const isCompleted = index < current;
        const isCurrent = index === current;
        const Icon = step.icon;

        return (
          <div key={step.labelKey} className="flex items-center">
            <div className="flex flex-col items-center">
              <div
                className="relative flex items-center justify-center w-9 h-9 rounded-full transition-all duration-300"
                style={{
                  background: isCompleted || isCurrent
                    ? 'var(--ag-primary)'
                    : 'var(--ag-bg-surface)',
                  border: isCompleted || isCurrent
                    ? '1.5px solid var(--ag-primary)'
                    : '1.5px solid var(--ag-glass-border)',
                  boxShadow: isCurrent
                    ? '0 0 16px var(--ag-primary-glow)'
                    : 'none',
                }}
              >
                {isCompleted ? (
                  <CheckCircle2 className="w-4 h-4 text-text-inverse" />
                ) : (
                  <Icon
                    className="w-4 h-4"
                    style={{ color: isCurrent ? 'var(--ag-text-inverse)' : 'var(--ag-text-tertiary)' }}
                  />
                )}
              </div>
              <span
                className="text-[10px] mt-1.5 whitespace-nowrap font-medium font-mono uppercase tracking-wider transition-colors"
                style={{
                  color: isCompleted || isCurrent
                    ? 'var(--ag-primary)'
                    : 'var(--ag-text-tertiary)',
                }}
              >
                {t(step.labelKey)}
              </span>
            </div>
            {index < STEP_KEYS.length - 1 && (
              <div
                className="w-12 h-px mx-2.5 mb-5 rounded-full transition-all duration-500"
                style={{
                  background: isCompleted
                    ? 'var(--ag-primary)'
                    : 'var(--ag-glass-border)',
                  boxShadow: isCompleted
                    ? '0 0 4px var(--ag-primary-glow)'
                    : 'none',
                }}
              />
            )}
          </div>
        );
      })}
    </div>
  );
}

// ==================== 连接测试结果 ====================

function TestResultBanner({ result }: { result: { success: boolean; error_msg?: string } | null }) {
  const { t } = useTranslation();
  if (!result) return null;

  return (
    <div
      className="flex items-start gap-2.5 rounded-md px-4 py-3 text-sm"
      style={{
        background: result.success ? 'var(--ag-success-subtle)' : 'var(--ag-danger-subtle)',
        color: result.success ? 'var(--ag-success)' : 'var(--ag-danger)',
        borderLeft: `3px solid ${result.success ? 'var(--ag-success)' : 'var(--ag-danger)'}`,
      }}
    >
      {result.success ? (
        <CheckCircle2 className="w-4 h-4 mt-0.5 shrink-0" />
      ) : (
        <CircleDot className="w-4 h-4 mt-0.5 shrink-0" />
      )}
      <span>{result.success ? t('setup.test_success') : t('setup.test_failed', { error: result.error_msg || '' })}</span>
    </div>
  );
}

// ==================== Step 1: 数据库配置 ====================

interface DBStepProps {
  data: TestDBReq;
  onChange: (data: TestDBReq) => void;
  onNext: () => void;
}

function DBStep({ data, onChange, onNext }: DBStepProps) {
  const { t } = useTranslation();
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ success: boolean; error_msg?: string } | null>(null);

  const update = (field: keyof TestDBReq, value: string | number) => {
    onChange({ ...data, [field]: value });
    setTestResult(null);
  };

  const handleTest = async () => {
    setTesting(true);
    setTestResult(null);
    try {
      const result = await setupApi.testDB(data);
      setTestResult(result);
    } catch (err) {
      setTestResult({ success: false, error_msg: err instanceof Error ? err.message : String(err) });
    } finally {
      setTesting(false);
    }
  };

  const sslOptions = [
    { value: 'disable', label: 'disable' },
    { value: 'require', label: 'require' },
    { value: 'verify-ca', label: 'verify-ca' },
    { value: 'verify-full', label: 'verify-full' },
  ];

  return (
    <div className="space-y-4">
      <p className="text-sm text-text-secondary mb-2">
        {t('setup.step_db_desc')}
      </p>
      <div className="grid grid-cols-2 gap-4">
        <Input
          label={t('setup.host')}
          value={data.host}
          onChange={(e) => update('host', e.target.value)}
          placeholder="localhost"
          required
        />
        <Input
          label={t('setup.port')}
          type="number"
          value={data.port}
          onChange={(e) => update('port', Number(e.target.value))}
          placeholder="5432"
          required
        />
      </div>
      <div className="grid grid-cols-2 gap-4">
        <Input
          label={t('setup.username')}
          value={data.user}
          onChange={(e) => update('user', e.target.value)}
          placeholder="postgres"
          required
        />
        <Input
          label={t('profile.old_password')}
          type="password"
          value={data.password || ''}
          onChange={(e) => update('password', e.target.value)}
          placeholder={t('setup.db_name')}
        />
      </div>
      <div className="grid grid-cols-2 gap-4">
        <Input
          label={t('setup.db_name')}
          value={data.dbname}
          onChange={(e) => update('dbname', e.target.value)}
          placeholder="airgate"
          required
        />
        <Select
          label={t('setup.ssl_mode')}
          value={data.sslmode || 'disable'}
          onChange={(e) => update('sslmode', e.target.value)}
          options={sslOptions}
        />
      </div>

      <TestResultBanner result={testResult} />

      {/* 操作按钮 */}
      <div className="flex justify-between pt-4">
        <Button
          variant="secondary"
          onClick={handleTest}
          loading={testing}
          icon={<Plug2 className="w-4 h-4" />}
        >
          {t('setup.test_connection')}
        </Button>
        <Button
          onClick={onNext}
          disabled={!testResult?.success}
          icon={<ArrowRight className="w-4 h-4" />}
        >
          {t('setup.step_redis')}
        </Button>
      </div>
    </div>
  );
}

// ==================== Step 2: Redis 配置 ====================

interface RedisStepProps {
  data: TestRedisReq;
  onChange: (data: TestRedisReq) => void;
  onPrev: () => void;
  onNext: () => void;
}

function RedisStep({ data, onChange, onPrev, onNext }: RedisStepProps) {
  const { t } = useTranslation();
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ success: boolean; error_msg?: string } | null>(null);

  const update = (field: keyof TestRedisReq, value: string | number | boolean) => {
    onChange({ ...data, [field]: value });
    setTestResult(null);
  };

  const handleTest = async () => {
    setTesting(true);
    setTestResult(null);
    try {
      const result = await setupApi.testRedis(data);
      setTestResult(result);
    } catch (err) {
      setTestResult({ success: false, error_msg: err instanceof Error ? err.message : String(err) });
    } finally {
      setTesting(false);
    }
  };

  return (
    <div className="space-y-4">
      <p className="text-sm text-text-secondary mb-2">
        {t('setup.step_redis_desc')}
      </p>
      <div className="grid grid-cols-2 gap-4">
        <Input
          label={t('setup.host')}
          value={data.host}
          onChange={(e) => update('host', e.target.value)}
          placeholder="localhost"
          required
        />
        <Input
          label={t('setup.port')}
          type="number"
          value={data.port}
          onChange={(e) => update('port', Number(e.target.value))}
          placeholder="6379"
          required
        />
      </div>
      <div className="grid grid-cols-2 gap-4">
        <Input
          label={t('profile.old_password')}
          type="password"
          value={data.password || ''}
          onChange={(e) => update('password', e.target.value)}
          placeholder={t('profile.old_password_placeholder')}
        />
        <Input
          label={t('setup.db_number')}
          type="number"
          value={data.db ?? 0}
          onChange={(e) => update('db', Number(e.target.value))}
          placeholder="0"
        />
      </div>
      {/* TLS 开关 */}
      <label
        className="flex items-center gap-3 px-3 py-2.5 rounded-md border border-glass-border bg-surface cursor-pointer transition-colors hover:border-border-focus"
      >
        <input
          type="checkbox"
          checked={data.tls || false}
          onChange={(e) => update('tls', e.target.checked)}
          className="h-4 w-4 rounded border-glass-border accent-[var(--ag-primary)]"
        />
        <div className="flex items-center gap-2">
          <ShieldCheck className="w-4 h-4 text-text-tertiary" />
          <span className="text-sm text-text-secondary">{t('setup.enable_tls')}</span>
        </div>
      </label>

      <TestResultBanner result={testResult} />

      {/* 操作按钮 */}
      <div className="flex justify-between pt-4">
        <div className="flex gap-2">
          <Button
            variant="ghost"
            onClick={onPrev}
            icon={<ArrowLeft className="w-4 h-4" />}
          >
            {t('setup.step_db')}
          </Button>
          <Button
            variant="secondary"
            onClick={handleTest}
            loading={testing}
            icon={<Plug2 className="w-4 h-4" />}
          >
            {t('setup.test_connection')}
          </Button>
        </div>
        <Button
          onClick={onNext}
          disabled={!testResult?.success}
          icon={<ArrowRight className="w-4 h-4" />}
        >
          {t('setup.step_admin')}
        </Button>
      </div>
    </div>
  );
}

// ==================== Step 3: 管理员账户 ====================

interface AdminStepProps {
  data: AdminSetup & { confirmPassword: string };
  onChange: (data: AdminSetup & { confirmPassword: string }) => void;
  onPrev: () => void;
  onNext: () => void;
}

function AdminStep({ data, onChange, onPrev, onNext }: AdminStepProps) {
  const { t } = useTranslation();

  const update = (field: string, value: string) => {
    onChange({ ...data, [field]: value });
  };

  // 密码强度检查
  const getPasswordStrength = (pwd: string): { label: string; color: string; width: string } => {
    if (pwd.length < 6) return { label: t('setup.password_too_short'), color: 'var(--ag-danger)', width: '20%' };
    if (pwd.length < 8) return { label: t('setup.strength_weak'), color: 'var(--ag-danger)', width: '35%' };
    const hasUpper = /[A-Z]/.test(pwd);
    const hasLower = /[a-z]/.test(pwd);
    const hasNumber = /\d/.test(pwd);
    const hasSpecial = /[^A-Za-z0-9]/.test(pwd);
    const score = [hasUpper, hasLower, hasNumber, hasSpecial].filter(Boolean).length;
    if (score >= 3 && pwd.length >= 10) return { label: t('setup.strength_strong'), color: 'var(--ag-success)', width: '100%' };
    if (score >= 2) return { label: t('setup.strength_fair'), color: 'var(--ag-warning)', width: '65%' };
    return { label: t('setup.strength_weak'), color: 'var(--ag-danger)', width: '35%' };
  };

  const passwordMismatch = data.confirmPassword && data.password !== data.confirmPassword;
  const passwordTooShort = data.password.length > 0 && data.password.length < 8;
  const strength = data.password ? getPasswordStrength(data.password) : null;

  const canProceed =
    data.email.trim() !== '' &&
    data.password.length >= 8 &&
    data.password === data.confirmPassword;

  return (
    <div className="space-y-4">
      <p className="text-sm text-text-secondary mb-2">
        {t('setup.step_admin_desc')}
      </p>
      <Input
        label={t('setup.admin_email')}
        type="email"
        value={data.email}
        onChange={(e) => update('email', e.target.value)}
        placeholder="admin@example.com"
        required
      />
      <div>
        <Input
          label={t('profile.new_password')}
          type="password"
          value={data.password}
          onChange={(e) => update('password', e.target.value)}
          placeholder={t('setup.password_too_short')}
          required
          error={passwordTooShort ? t('setup.password_too_short') : undefined}
        />
        {strength && !passwordTooShort && (
          <div className="mt-2 space-y-1">
            <div
              className="h-1 rounded-full overflow-hidden"
              style={{ background: 'var(--ag-bg-surface)' }}
            >
              <div
                className="h-full rounded-full transition-all duration-300"
                style={{ width: strength.width, background: strength.color }}
              />
            </div>
            <p className="text-xs" style={{ color: strength.color }}>
              {t('setup.password_strength')}:{strength.label}
            </p>
          </div>
        )}
      </div>
      <Input
        label={t('profile.confirm_new_password')}
        type="password"
        value={data.confirmPassword}
        onChange={(e) => update('confirmPassword', e.target.value)}
        placeholder={t('profile.confirm_placeholder')}
        required
        error={passwordMismatch ? t('profile.password_mismatch') : undefined}
      />

      {/* 操作按钮 */}
      <div className="flex justify-between pt-4">
        <Button
          variant="ghost"
          onClick={onPrev}
          icon={<ArrowLeft className="w-4 h-4" />}
        >
          {t('setup.step_redis')}
        </Button>
        <Button
          onClick={onNext}
          disabled={!canProceed}
          icon={<ArrowRight className="w-4 h-4" />}
        >
          {t('setup.step_finish')}
        </Button>
      </div>
    </div>
  );
}

// ==================== Step 4: 完成安装 ====================

interface FinishStepProps {
  dbConfig: TestDBReq;
  redisConfig: TestRedisReq;
  adminConfig: AdminSetup;
  onPrev: () => void;
}

function FinishStep({ dbConfig, redisConfig, adminConfig, onPrev }: FinishStepProps) {
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
        { label: t('profile.old_password'), value: redisConfig.password ? '******' : t('common.no') },
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

// ==================== 安装向导主页面 ====================

export default function SetupPage() {
  const { t } = useTranslation();
  const [step, setStep] = useState(0);

  // 各步骤的表单数据
  const [dbConfig, setDBConfig] = useState<TestDBReq>({
    host: 'localhost',
    port: 5432,
    user: 'postgres',
    password: '',
    dbname: 'airgate',
    sslmode: 'disable',
  });

  const [redisConfig, setRedisConfig] = useState<TestRedisReq>({
    host: 'localhost',
    port: 6379,
    password: '',
    db: 0,
    tls: false,
  });

  const [adminConfig, setAdminConfig] = useState<AdminSetup & { confirmPassword: string }>({
    email: '',
    password: '',
    confirmPassword: '',
  });

  return (
    <div className="min-h-screen flex items-center justify-center p-4 relative overflow-hidden">
      {/* 背景 */}
      <div className="absolute inset-0 pointer-events-none">
        <div
          className="absolute inset-0 opacity-[0.04]"
          style={{
            backgroundImage: `linear-gradient(var(--ag-text-tertiary) 1px, transparent 1px), linear-gradient(90deg, var(--ag-text-tertiary) 1px, transparent 1px)`,
            backgroundSize: '64px 64px',
          }}
        />
        <div
          className="absolute -top-[30%] -left-[15%] w-[700px] h-[700px] rounded-full opacity-[0.06]"
          style={{ background: 'radial-gradient(circle, var(--ag-primary), transparent 65%)' }}
        />
        <div
          className="absolute -bottom-[25%] -right-[10%] w-[500px] h-[500px] rounded-full opacity-[0.04]"
          style={{ background: 'radial-gradient(circle, var(--ag-info), transparent 65%)' }}
        />
        <div
          className="absolute top-0 left-0 right-0 h-px"
          style={{ background: 'linear-gradient(90deg, transparent, var(--ag-primary-glow), transparent)' }}
        />
      </div>

      <div
        className="relative w-full max-w-xl"
        style={{ animation: 'ag-slide-up 0.45s cubic-bezier(0.16, 1, 0.3, 1)' }}
      >
        {/* 标题 */}
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-12 h-12 rounded-lg bg-primary-subtle mb-4 shadow-glow">
            <Zap className="w-6 h-6 text-primary" />
          </div>
          <h1 className="text-xl font-semibold text-text tracking-tight">
            AirGate
          </h1>
          <p className="text-xs text-text-tertiary mt-1.5 tracking-wide font-mono uppercase">
            {t('setup.title')}
          </p>
        </div>

        {/* 步骤指示器 */}
        <Stepper current={step} />

        {/* 表单卡片 */}
        <Card>
          {step === 0 && (
            <DBStep data={dbConfig} onChange={setDBConfig} onNext={() => setStep(1)} />
          )}
          {step === 1 && (
            <RedisStep
              data={redisConfig}
              onChange={setRedisConfig}
              onPrev={() => setStep(0)}
              onNext={() => setStep(2)}
            />
          )}
          {step === 2 && (
            <AdminStep
              data={adminConfig}
              onChange={setAdminConfig}
              onPrev={() => setStep(1)}
              onNext={() => setStep(3)}
            />
          )}
          {step === 3 && (
            <FinishStep
              dbConfig={dbConfig}
              redisConfig={redisConfig}
              adminConfig={{ email: adminConfig.email, password: adminConfig.password }}
              onPrev={() => setStep(2)}
            />
          )}
        </Card>

        {/* 底部 */}
        <p className="text-center text-[10px] text-text-tertiary mt-8 font-mono uppercase tracking-[0.15em]">
          Powered by AirGate
        </p>
      </div>
    </div>
  );
}
