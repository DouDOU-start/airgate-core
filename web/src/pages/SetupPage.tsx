import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from '../shared/components/Card';
import {
  Database,
  Server,
  UserCog,
  CheckCircle2,
  Zap,
} from 'lucide-react';
import type { TestDBReq, TestRedisReq, AdminSetup } from '../shared/types';
import StepDatabase from './setup/StepDatabase';
import StepRedis from './setup/StepRedis';
import StepAdmin from './setup/StepAdmin';
import StepFinish from './setup/StepFinish';

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

// ==================== 安装向导主页面 ====================

export default function SetupPage() {
  const { t } = useTranslation();
  const [step, setStep] = useState(0);

  // 各步骤的表单数据
  const [dbConfig, setDBConfig] = useState<TestDBReq>({
    host: 'localhost',
    port: 5432,
    user: 'airgate',
    password: 'airgate',
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
            <StepDatabase data={dbConfig} onChange={setDBConfig} onNext={() => setStep(1)} />
          )}
          {step === 1 && (
            <StepRedis
              data={redisConfig}
              onChange={setRedisConfig}
              onPrev={() => setStep(0)}
              onNext={() => setStep(2)}
            />
          )}
          {step === 2 && (
            <StepAdmin
              data={adminConfig}
              onChange={setAdminConfig}
              onPrev={() => setStep(1)}
              onNext={() => setStep(3)}
            />
          )}
          {step === 3 && (
            <StepFinish
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
