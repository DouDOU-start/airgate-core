import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Form, Input, Label, ListBox, Select, TextField as HeroTextField } from '@heroui/react';
import { setupApi } from '../../shared/api/setup';
import {
  ArrowRight,
  Plug2,
} from 'lucide-react';
import type { TestDBReq } from '../../shared/types';

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
    { id: 'disable', label: 'disable' },
    { id: 'require', label: 'require' },
    { id: 'verify-ca', label: 'verify-ca' },
    { id: 'verify-full', label: 'verify-full' },
  ];

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (testResult?.success) onNext();
  };

  return (
    <Form className="space-y-4" onSubmit={handleSubmit}>
      <p className="text-sm text-text-secondary mb-2">
        {t('setup.step_db_desc')}
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
            placeholder="5432"
            required
          />
        </HeroTextField>
      </div>
      <div className="grid grid-cols-2 gap-4">
        <HeroTextField fullWidth isRequired>
          <Label>{t('setup.username')}</Label>
          <Input
            name="username"
            autoComplete="username"
            value={data.user}
            onChange={(e) => update('user', e.target.value)}
            placeholder="airgate"
            required
          />
        </HeroTextField>
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
      </div>
      <div className="grid grid-cols-2 gap-4">
        <HeroTextField fullWidth isRequired>
          <Label>{t('setup.db_name')}</Label>
          <Input
            name="dbname"
            value={data.dbname}
            onChange={(e) => update('dbname', e.target.value)}
            placeholder="airgate"
            required
          />
        </HeroTextField>
        <Select
          fullWidth
          selectedKey={data.sslmode || 'disable'}
          onSelectionChange={(key) => update('sslmode', String(key ?? 'disable'))}
        >
          <Label>{t('setup.ssl_mode')}</Label>
          <Select.Trigger>
            <Select.Value>{data.sslmode || 'disable'}</Select.Value>
            <Select.Indicator />
          </Select.Trigger>
          <Select.Popover>
            <ListBox items={sslOptions}>
              {(item) => (
                <ListBox.Item id={item.id} textValue={item.label}>
                  {item.label}
                </ListBox.Item>
              )}
            </ListBox>
          </Select.Popover>
        </Select>
      </div>

      <TestResultBanner result={testResult} />

      {/* 操作按钮 */}
      <div className="flex justify-between pt-4">
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
        <Button
          type="submit"
          isDisabled={!testResult?.success}
        >
          <ArrowRight className="w-4 h-4" />
          {t('setup.step_redis')}
        </Button>
      </div>
    </Form>
  );
}
