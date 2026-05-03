import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Form, Input, Label, Switch, TextField as HeroTextField } from '@heroui/react';
import { setupApi } from '../../shared/api/setup';
import {
  ArrowLeft,
  ArrowRight,
  Plug2,
  ShieldCheck,
} from 'lucide-react';
import type { TestRedisReq } from '../../shared/types';

function TestResultBanner({ result }: { result: { success: boolean; error_msg?: string } | null }) {
  const { t } = useTranslation();
  if (!result) return null;

  return (
    <Alert status={result.success ? 'success' : 'danger'}>
      <Alert.Content>
        <Alert.Description>
          {result.success ? t('setup.test_success') : t('setup.test_failed', { error: result.error_msg || '' })}
        </Alert.Description>
      </Alert.Content>
    </Alert>
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

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (testResult?.success) onNext();
  };

  return (
    <Form className="space-y-4" onSubmit={handleSubmit}>
      <p className="text-sm text-text-secondary mb-2">
        {t('setup.step_redis_desc')}
      </p>
      <div className="grid grid-cols-2 gap-4">
        <HeroTextField fullWidth isRequired>
          <Label>{t('setup.host')}</Label>
          <Input
            name="host"
            autoComplete="off"
            value={data.host}
            onChange={(e) => update('host', e.target.value)}
            placeholder="localhost"
            required
          />
        </HeroTextField>
        <HeroTextField fullWidth isRequired>
          <Label>{t('setup.port')}</Label>
          <Input
            name="port"
            type="number"
            value={data.port}
            onChange={(e) => update('port', Number(e.target.value))}
            placeholder="6379"
            required
          />
        </HeroTextField>
      </div>
      <div className="grid grid-cols-2 gap-4">
        <HeroTextField fullWidth>
          <Label>{t('setup.password')}</Label>
          <Input
            name="password"
            type="password"
            value={data.password || ''}
            onChange={(e) => update('password', e.target.value)}
            placeholder={t('setup.password')}
            autoComplete="off"
          />
        </HeroTextField>
        <HeroTextField fullWidth>
          <Label>{t('setup.db_number')}</Label>
          <Input
            name="db"
            type="number"
            value={data.db ?? 0}
            onChange={(e) => update('db', Number(e.target.value))}
            placeholder="0"
          />
        </HeroTextField>
      </div>
      <Switch
        isSelected={data.tls || false}
        onChange={(selected) => update('tls', selected)}
      >
        <Switch.Control>
          <Switch.Thumb />
        </Switch.Control>
        <Switch.Content>
          <span className="flex items-center gap-2 text-sm font-medium text-text">
            <ShieldCheck className="w-4 h-4 text-text-tertiary" />
            {t('setup.enable_tls')}
          </span>
        </Switch.Content>
      </Switch>

      <TestResultBanner result={testResult} />

      {/* 操作按钮 */}
      <div className="flex justify-between pt-4">
        <div className="flex gap-2">
          <Button
            type="button"
            variant="ghost"
            onPress={onPrev}
          >
            <ArrowLeft className="w-4 h-4" />
            {t('setup.step_db')}
          </Button>
          <Button
            type="button"
            variant="secondary"
            onPress={handleTest}
            isDisabled={testing}
            aria-busy={testing}
          >
            <Plug2 className="w-4 h-4" />
            {t('setup.test_connection')}
          </Button>
        </div>
        <Button
          type="submit"
          isDisabled={!testResult?.success}
        >
          <ArrowRight className="w-4 h-4" />
          {t('setup.step_admin')}
        </Button>
      </div>
    </Form>
  );
}
