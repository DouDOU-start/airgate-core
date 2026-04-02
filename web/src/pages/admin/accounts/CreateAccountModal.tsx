import { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Layers, Hash, Gauge } from 'lucide-react';
import { Button } from '../../../shared/components/Button';
import { Input, Select } from '../../../shared/components/Input';
import { Modal } from '../../../shared/components/Modal';
import { accountsApi } from '../../../shared/api/accounts';
import { groupsApi } from '../../../shared/api/groups';
import { usePlatforms } from '../../../shared/hooks/usePlatforms';
import { queryKeys } from '../../../shared/queryKeys';
import { FETCH_ALL_PARAMS } from '../../../shared/constants';
import {
  usePluginAccountForm,
  createPluginOAuthBridge,
  getSchemaSelectedAccountType,
  getSchemaVisibleFields,
  filterCredentialsForAccountType,
} from './accountUtils';
import { SchemaCredentialsForm, GroupCheckboxList } from './CredentialForm';
import type { CreateAccountReq } from '../../../shared/types';

export function CreateAccountModal({
  open,
  onClose,
  onSubmit,
  loading,
  platforms,
}: {
  open: boolean;
  onClose: () => void;
  onSubmit: (data: CreateAccountReq) => void;
  loading: boolean;
  platforms: string[];
}) {
  const { t } = useTranslation();
  const { platformName: pName } = usePlatforms();
  const [platform, setPlatform] = useState('');
  const [accountType, setAccountType] = useState('');
  const [form, setForm] = useState<Omit<CreateAccountReq, 'platform' | 'credentials' | 'type'>>({
    name: '',
    priority: 0,
    max_concurrency: 5,
    rate_multiplier: 1,
  });
  const [credentials, setCredentials] = useState<Record<string, string>>({});
  const [groupIds, setGroupIds] = useState<number[]>([]);

  // 根据平台获取凭证字段定义
  const { data: schema } = useQuery({
    queryKey: queryKeys.credentialsSchema(platform),
    queryFn: () => accountsApi.credentialsSchema(platform),
    enabled: !!platform,
  });

  // 查询分组列表
  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
  });

  // 加载插件自定义表单组件
  const { Form: PluginAccountForm, pluginId } = usePluginAccountForm(platform);
  const pluginOAuth = createPluginOAuthBridge(pluginId);

  useEffect(() => {
    const selectedType = getSchemaSelectedAccountType(schema, accountType);
    if (!selectedType || selectedType.key === accountType) return;
    setAccountType(selectedType.key);
  }, [schema, accountType]);

  // 平台变化时重置凭证和账号类型
  const handlePlatformChange = (newPlatform: string) => {
    setPlatform(newPlatform);
    setCredentials({});
    setAccountType('');
  };

  const handleSchemaAccountTypeChange = (type: string) => {
    const selectedType = getSchemaSelectedAccountType(schema, type);
    setAccountType(type);
    setCredentials((prev) => filterCredentialsForAccountType(prev, selectedType));
  };

  const handleSubmit = () => {
    if (!platform || !form.name) return;
    onSubmit({
      ...form,
      platform,
      type: accountType || undefined,
      credentials,
      group_ids: groupIds,
    });
  };

  const handleClose = () => {
    setPlatform('');
    setAccountType('');
    setForm({ name: '', priority: 0, max_concurrency: 5, rate_multiplier: 1 });
    setCredentials({});
    setGroupIds([]);
    onClose();
  };

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title={t('accounts.create')}
      width="560px"
      footer={
        <>
          <Button variant="secondary" onClick={handleClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={handleSubmit} loading={loading} disabled={!platform}>
            {t('common.create')}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Select
          label={t('accounts.platform')}
          required
          value={platform}
          onChange={(e) => handlePlatformChange(e.target.value)}
          options={[
            { value: '', label: t('accounts.select_platform') },
            ...platforms.map((p) => ({ value: p, label: pName(p) })),
          ]}
        />

        <Input
          label={t('common.name')}
          required
          value={form.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
          icon={<Layers className="w-4 h-4" />}
        />

        {/* 凭证区域：插件自定义表单 or 默认 schema 驱动 */}
        {PluginAccountForm ? (
          <div
            className="ag-plugin-scope pt-4"
            style={{ borderTop: '1px solid var(--ag-border)' }}
          >
            <PluginAccountForm
              credentials={credentials}
              onChange={setCredentials}
              mode="create"
              accountType={accountType}
              onAccountTypeChange={setAccountType}
              onSuggestedName={(name) =>
                setForm((prev) => (prev.name ? prev : { ...prev, name }))
              }
              oauth={pluginOAuth}
            />
          </div>
        ) : schema && getSchemaVisibleFields(schema, accountType).length > 0 ? (
          <SchemaCredentialsForm
            schema={schema}
            accountType={accountType}
            onAccountTypeChange={handleSchemaAccountTypeChange}
            credentials={credentials}
            onCredentialsChange={setCredentials}
          />
        ) : null}

        <Input
          label={t('accounts.priority_hint')}
          type="number"
          min={0}
          max={999}
          step={1}
          value={String(form.priority ?? 50)}
          onChange={(e) => {
            const v = Math.round(Number(e.target.value));
            setForm({ ...form, priority: Math.max(0, Math.min(999, v)) });
          }}
          icon={<Hash className="w-4 h-4" />}
        />
        <Input
          label={t('accounts.concurrency')}
          type="number"
          value={String(form.max_concurrency ?? 5)}
          onChange={(e) =>
            setForm({ ...form, max_concurrency: Number(e.target.value) })
          }
          icon={<Gauge className="w-4 h-4" />}
        />
        <Input
          label={t('accounts.rate_multiplier')}
          type="number"
          step="0.1"
          value={String(form.rate_multiplier ?? 1)}
          onChange={(e) =>
            setForm({ ...form, rate_multiplier: Number(e.target.value) })
          }
        />

        {/* 分组选择 */}
        <GroupCheckboxList
          groups={groupsData?.list ?? []}
          selectedIds={groupIds}
          onChange={setGroupIds}
        />
      </div>
    </Modal>
  );
}
