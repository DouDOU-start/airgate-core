import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '../../shared/components/Button';
import { Input } from '../../shared/components/Input';
import { setupApi } from '../../shared/api/setup';
import {
  ArrowLeft,
  ArrowRight,
  Plug2,
  ShieldCheck,
  CheckCircle2,
  CircleDot,
} from 'lucide-react';
import type { TestRedisReq } from '../../shared/types';

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

export interface StepRedisProps {
  data: TestRedisReq;
  onChange: (data: TestRedisReq) => void;
  onPrev: () => void;
  onNext: () => void;
}

export default function StepRedis({ data, onChange, onPrev, onNext }: StepRedisProps) {
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
          label={t('setup.password')}
          type="password"
          value={data.password || ''}
          onChange={(e) => update('password', e.target.value)}
          placeholder={t('setup.password')}
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
