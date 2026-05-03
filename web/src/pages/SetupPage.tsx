import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from '@heroui/react';
import {
  Database,
  Server,
  UserCog,
  CheckCircle2,
  Zap,
} from 'lucide-react';
import type { TestDBReq, TestRedisReq, AdminSetup } from '../shared/types';
import { setupApi } from '../shared/api/setup';
import StepDatabase from './setup/StepDatabase';
import StepRedis from './setup/StepRedis';
import StepAdmin from './setup/StepAdmin';
import StepFinish from './setup/StepFinish';

// ==================== 步骤配置 ====================
//
// docker compose 之类的部署会通过环境变量预先注入 DB / Redis 连接信息，
// 此时后端 /setup/status 会返回 env_db / env_redis 提示，wizard 会自动隐藏对应步骤，
// 用户只需要建管理员账号即可。

type StepKey = 'db' | 'redis' | 'admin' | 'finish';

const STEP_DEF: Record<StepKey, { labelKey: string; icon: typeof Database }> = {
  db: { labelKey: 'setup.step_db', icon: Database },
  redis: { labelKey: 'setup.step_redis', icon: Server },
  admin: { labelKey: 'setup.step_admin', icon: UserCog },
  finish: { labelKey: 'setup.step_finish', icon: CheckCircle2 },
};

// ==================== 步骤指示器 ====================

function Stepper({ current, steps }: { current: number; steps: StepKey[] }) {
  const { t } = useTranslation();

  return (
    <div className="flex items-center justify-center mb-10">
      {steps.map((key, index) => {
        const step = STEP_DEF[key];
        const isCompleted = index < current;
        const isCurrent = index === current;
        const Icon = step.icon;

        return (
          <div key={key} className="flex items-center">
            <div className="flex flex-col items-center">
              <div
                className={[
                  'relative flex items-center justify-center w-9 h-9 rounded-[var(--radius)] border transition-all duration-300',
                  isCompleted || isCurrent
                    ? 'border-primary bg-primary text-text-inverse'
                    : 'border-glass-border bg-surface text-text-tertiary',
                  isCurrent ? 'shadow-[0_0_16px_var(--ag-primary-glow)]' : '',
                ].filter(Boolean).join(' ')}
              >
                {isCompleted ? (
                  <CheckCircle2 className="w-4 h-4 text-text-inverse" />
                ) : (
                  <Icon className="w-4 h-4" />
                )}
              </div>
              <span
                className={[
                  'text-[10px] mt-1.5 whitespace-nowrap font-medium font-mono uppercase transition-colors',
                  isCompleted || isCurrent ? 'text-primary' : 'text-text-tertiary',
                ].join(' ')}
              >
                {t(step.labelKey)}
              </span>
            </div>
            {index < steps.length - 1 && (
              <div
                className={[
                  'w-12 h-px mx-2.5 mb-5 rounded-[var(--radius)] transition-all duration-500',
                  isCompleted ? 'bg-primary shadow-[0_0_4px_var(--ag-primary-glow)]' : 'bg-glass-border',
                ].join(' ')}
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

  // 是否有 env 提供的配置（影响是否跳过对应步骤）
  const [envDBProvided, setEnvDBProvided] = useState(false);
  const [envRedisProvided, setEnvRedisProvided] = useState(false);

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

  // 拉取后端 status，预填环境变量已提供的字段并标记跳过
  useEffect(() => {
    setupApi.status().then((resp) => {
      if (resp.env_db) {
        setEnvDBProvided(true);
        setDBConfig((prev) => ({
          ...prev,
          host: resp.env_db!.host,
          port: resp.env_db!.port,
          user: resp.env_db!.user,
          dbname: resp.env_db!.dbname,
          sslmode: resp.env_db!.sslmode,
          // password 由后端 install 时从 env 取，前端只占位以通过表单校验
          password: '__env__',
        }));
      }
      if (resp.env_redis) {
        setEnvRedisProvided(true);
        setRedisConfig((prev) => ({
          ...prev,
          host: resp.env_redis!.host,
          port: resp.env_redis!.port,
          db: resp.env_redis!.db,
          // password 同上
          password: '__env__',
        }));
      }
    }).catch(() => {
      // 状态接口不可用时降级为完整 wizard，不阻塞用户
    });
  }, []);

  // 动态步骤列表：env 提供的步骤直接被去掉
  const visibleSteps = useMemo<StepKey[]>(() => {
    const list: StepKey[] = [];
    if (!envDBProvided) list.push('db');
    if (!envRedisProvided) list.push('redis');
    list.push('admin');
    list.push('finish');
    return list;
  }, [envDBProvided, envRedisProvided]);

  const currentStepKey = visibleSteps[step] ?? 'finish';

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
          <div className="inline-flex items-center justify-center w-12 h-12 rounded-[var(--radius)] bg-primary-subtle mb-4 shadow-glow">
            <Zap className="w-6 h-6 text-primary" />
          </div>
          <h1 className="text-xl font-semibold text-text">
            AirGate
          </h1>
          <p className="text-xs text-text-tertiary mt-1.5 font-mono uppercase">
            {t('setup.title')}
          </p>
        </div>

        {/* 步骤指示器 */}
        <Stepper current={step} steps={visibleSteps} />

        {/* 表单卡片 */}
        <Card>
          <Card.Content className="p-6">
            {currentStepKey === 'db' && (
              <StepDatabase data={dbConfig} onChange={setDBConfig} onNext={() => setStep(step + 1)} />
            )}
            {currentStepKey === 'redis' && (
              <StepRedis
                data={redisConfig}
                onChange={setRedisConfig}
                onPrev={() => setStep(step - 1)}
                onNext={() => setStep(step + 1)}
              />
            )}
            {currentStepKey === 'admin' && (
              <StepAdmin
                data={adminConfig}
                onChange={setAdminConfig}
                // 当 db / redis 都来自 env 时，admin 是第一步，没有上一步可返回
                onPrev={step > 0 ? () => setStep(step - 1) : undefined}
                onNext={() => setStep(step + 1)}
              />
            )}
            {currentStepKey === 'finish' && (
              <StepFinish
                dbConfig={dbConfig}
                redisConfig={redisConfig}
                adminConfig={{ email: adminConfig.email, password: adminConfig.password }}
                envDBProvided={envDBProvided}
                envRedisProvided={envRedisProvided}
                onPrev={() => setStep(step - 1)}
              />
            )}
          </Card.Content>
        </Card>

        {/* 底部 */}
        <p className="text-center text-[10px] text-text-tertiary mt-8 font-mono uppercase">
          Powered by AirGate
        </p>
      </div>
    </div>
  );
}
