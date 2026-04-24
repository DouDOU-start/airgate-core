import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '../../shared/components/Button';
import { Input, Select } from '../../shared/components/Input';
import { setupApi } from '../../shared/api/setup';
import {
  ArrowRight,
  Plug2,
  CheckCircle2,
  CircleDot,
} from 'lucide-react';
import type { TestDBReq } from '../../shared/types';

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

export interface StepDatabaseProps {
  data: TestDBReq;
  onChange: (data: TestDBReq) => void;
  onNext: () => void;
}

export default function StepDatabase({ data, onChange, onNext }: StepDatabaseProps) {
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

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (testResult?.success) onNext();
  };

  return (
    <form className="space-y-4" onSubmit={handleSubmit} noValidate>
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
          placeholder="airgate"
          required
        />
        <Input
          label={t('setup.password')}
          type="password"
          value={data.password || ''}
          onChange={(e) => update('password', e.target.value)}
          placeholder={t('setup.password')}
          autoComplete="off"
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
          type="button"
          variant="secondary"
          onClick={handleTest}
          loading={testing}
          icon={<Plug2 className="w-4 h-4" />}
        >
          {t('setup.test_connection')}
        </Button>
        <Button
          type="submit"
          disabled={!testResult?.success}
          icon={<ArrowRight className="w-4 h-4" />}
        >
          {t('setup.step_redis')}
        </Button>
      </div>
    </form>
  );
}
