import { useState, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Layers, Hash, Gauge } from 'lucide-react';
import { Button } from '../../../shared/components/Button';
import { Input, Select } from '../../../shared/components/Input';
import { Switch } from '../../../shared/components/Switch';
import { Modal } from '../../../shared/components/Modal';
import { accountsApi } from '../../../shared/api/accounts';
import { groupsApi } from '../../../shared/api/groups';
import { proxiesApi } from '../../../shared/api/proxies';
import { usePlatforms } from '../../../shared/hooks/usePlatforms';
import { queryKeys } from '../../../shared/queryKeys';
import { FETCH_ALL_PARAMS } from '../../../shared/constants';
import {
  usePluginAccountForm,
  createPluginOAuthBridge,
  detectCredentialAccountType,
  getSchemaSelectedAccountType,
  getSchemaVisibleFields,
  filterCredentialsForAccountType,
} from './accountUtils';
import { SchemaCredentialsForm, GroupCheckboxList } from './CredentialForm';
import type { AccountResp, UpdateAccountReq } from '../../../shared/types';

export function EditAccountModal({
  open,
  account,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  account: AccountResp;
  onClose: () => void;
  onSubmit: (data: UpdateAccountReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const { platformName: pName } = usePlatforms();
  const initialAccountType = account.type || detectCredentialAccountType(account.credentials);
  const [accountType, setAccountType] = useState(initialAccountType);
  const [form, setForm] = useState<UpdateAccountReq>({
    name: account.name,
    type: initialAccountType || undefined,
    status: account.status === 'error' ? 'active' : (account.status as 'active' | 'disabled'),
    priority: account.priority,
    max_concurrency: account.max_concurrency,
    rate_multiplier: account.rate_multiplier,
    proxy_id: account.proxy_id,
  });

  // 获取凭证字段定义，用于编辑凭证
  const { data: schema } = useQuery({
    queryKey: queryKeys.credentialsSchema(account.platform),
    queryFn: () => accountsApi.credentialsSchema(account.platform),
  });

  // 加载插件自定义表单组件
  const { Form: PluginAccountForm, pluginId } = usePluginAccountForm(account.platform);
  const pluginOAuth = createPluginOAuthBridge(pluginId);

  // 保留原始凭证，用于提交时回填未修改的密码字段
  const origCredentials = useRef(account.credentials);
  const [credentials, setCredentials] = useState<Record<string, string>>(
    account.credentials,
  );

  // schema 加载后，清空密码字段的显示值（避免回填）
  const passwordFieldsCleared = useRef(false);
  useEffect(() => {
    if (!schema || passwordFieldsCleared.current) return;
    const passwordKeys = getSchemaVisibleFields(schema, accountType)
      .filter((f) => f.type === 'password')
      .map((f) => f.key);
    if (passwordKeys.length === 0) return;
    passwordFieldsCleared.current = true;
    setCredentials((prev) => {
      const next = { ...prev };
      for (const key of passwordKeys) next[key] = '';
      return next;
    });
  }, [schema, accountType]);
  const [groupIds, setGroupIds] = useState<number[]>(account.group_ids ?? []);

  // 查询分组列表
  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
  });

  // 查询代理列表
  const { data: proxiesData } = useQuery({
    queryKey: queryKeys.proxiesAll(),
    queryFn: () => proxiesApi.list(FETCH_ALL_PARAMS),
  });

  useEffect(() => {
    const selectedType = getSchemaSelectedAccountType(schema, accountType);
    if (!selectedType || selectedType.key === accountType) return;
    setAccountType(selectedType.key);
    setForm((prev) => ({ ...prev, type: selectedType.key || undefined }));
  }, [schema, accountType]);

  const handleAccountTypeChange = (type: string) => {
    setAccountType(type);
    setForm({ ...form, type: type || undefined });
  };

  const handleSchemaAccountTypeChange = (type: string) => {
    const selectedType = getSchemaSelectedAccountType(schema, type);
    handleAccountTypeChange(type);
    setCredentials((prev) => filterCredentialsForAccountType(prev, selectedType));
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('accounts.edit')}
      width="560px"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button
            onClick={() => {
              // 提交时：仅将未修改的密码字段回填原值，允许普通字段被清空
              const merged = { ...credentials };
              const passwordKeys = new Set(
                getSchemaVisibleFields(schema, accountType)
                  .filter((field) => field.type === 'password')
                  .map((field) => field.key),
              );
              for (const [k, v] of Object.entries(origCredentials.current)) {
                if (passwordKeys.has(k) && merged[k] === '' && v) merged[k] = v;
              }
              onSubmit({ ...form, type: accountType || undefined, credentials: merged, group_ids: groupIds });
            }}
            loading={loading}
          >
            {t('common.save')}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Input label={t('accounts.platform')} value={pName(account.platform)} disabled />
        <Input
          label={t('common.name')}
          value={form.name ?? ''}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
          icon={<Layers className="w-4 h-4" />}
        />

        {/* 凭证编辑：插件自定义表单 or 默认 schema 驱动 */}
        {PluginAccountForm ? (
          <div
            className="ag-plugin-scope pt-4"
            style={{ borderTop: '1px solid var(--ag-border)' }}
          >
            <PluginAccountForm
              credentials={credentials}
              onChange={setCredentials}
              mode="edit"
              accountType={accountType}
              onAccountTypeChange={handleAccountTypeChange}
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
            mode="edit"
          />
        ) : null}

        <Switch
          label={t('accounts.enable_dispatch')}
          checked={form.status !== 'disabled'}
          onChange={(on) => setForm({ ...form, status: on ? 'active' : 'disabled' })}
        />
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
        <Select
          label={t('accounts.proxy')}
          value={form.proxy_id == null ? '' : String(form.proxy_id)}
          onChange={(e) =>
            setForm({
              ...form,
              proxy_id: e.target.value ? Number(e.target.value) : null,
            })
          }
          options={[
            { value: '', label: t('accounts.no_proxy') },
            ...(proxiesData?.list ?? []).map((p) => ({
              value: String(p.id),
              label: `${p.name} (${p.protocol}://${p.address}:${p.port})`,
            })),
          ]}
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
