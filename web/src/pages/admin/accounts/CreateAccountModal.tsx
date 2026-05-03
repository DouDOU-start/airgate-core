import { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Button, Checkbox, Form, Input, Label, ListBox, Modal, Select, Switch, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { Layers, Hash, Gauge } from 'lucide-react';
import type {
  PluginBatchAccountInput,
  PluginBatchImportResult,
} from '@airgate/theme/plugin';
import { accountsApi } from '../../../shared/api/accounts';
import { groupsApi } from '../../../shared/api/groups';
import { proxiesApi } from '../../../shared/api/proxies';
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
import { SchemaCredentialsForm } from './CredentialForm';
import type { CreateAccountReq, AccountExportItem } from '../../../shared/types';

export function CreateAccountModal({
  open,
  onClose,
  onSubmit,
  onBatchImport,
  loading,
  platforms,
}: {
  open: boolean;
  onClose: () => void;
  onSubmit: (data: CreateAccountReq) => void;
  onBatchImport?: (accounts: AccountExportItem[]) => Promise<PluginBatchImportResult>;
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
  const [batchMode, setBatchMode] = useState(false);

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

  // 查询代理列表
  const { data: proxiesData } = useQuery({
    queryKey: queryKeys.proxiesAll(),
    queryFn: () => proxiesApi.list(FETCH_ALL_PARAMS),
  });

  // 加载插件自定义表单组件
  const { Form: PluginAccountForm, pluginId } = usePluginAccountForm(platform);
  const pluginOAuth = createPluginOAuthBridge(pluginId);

  useEffect(() => {
    const selectedType = getSchemaSelectedAccountType(schema, accountType);
    if (!selectedType || selectedType.key === accountType) return;
    setAccountType(selectedType.key);
  }, [schema, accountType]);

  // 弹窗关闭时重置所有内部状态，避免父组件直接 setShowCreateModal(false)
  // 绕过 handleClose 导致的状态残留（例如重开后停留在第 2 步）
  useEffect(() => {
    if (open) return;
    setPlatform('');
    setAccountType('');
    setForm({ name: '', priority: 0, max_concurrency: 5, rate_multiplier: 1, upstream_is_pool: false });
    setCredentials({});
    setGroupIds([]);
    setBatchMode(false);
  }, [open]);

  // 平台变化时重置凭证和账号类型
  const handlePlatformChange = (newPlatform: string) => {
    setPlatform(newPlatform);
    setCredentials({});
    setAccountType('');
    setGroupIds([]);
    setBatchMode(false);
  };

  // 插件表单触发的批量导入：补全 platform/元数据后交给外层 import
  // 命名规则：
  //  1. 填了名称 → 作为前缀，生成 {prefix}1 / {prefix}2 / ...
  //  2. 未填名称 → 优先用插件返回的账号名（通常是邮箱）
  //  3. 兜底 → "Claude Code {i+1}"
  const handlePluginBatchImport = async (
    accounts: PluginBatchAccountInput[],
  ): Promise<PluginBatchImportResult> => {
    if (!onBatchImport) return { imported: 0, failed: accounts.length };
    const prefix = form.name.trim();
    const toImport: AccountExportItem[] = accounts.map((a, i) => ({
      name: prefix ? `${prefix}${i + 1}` : a.name || `Claude Code ${i + 1}`,
      platform,
      type: a.type || 'oauth',
      credentials: a.credentials,
      priority: form.priority ?? 0,
      max_concurrency: form.max_concurrency ?? 5,
      rate_multiplier: form.rate_multiplier ?? 1,
      group_ids: groupIds.length ? groupIds : undefined,
      proxy_id: form.proxy_id,
    }));
    return onBatchImport(toImport);
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
    setForm({ name: '', priority: 0, max_concurrency: 5, rate_multiplier: 1, upstream_is_pool: false });
    setCredentials({});
    setGroupIds([]);
    setBatchMode(false);
    onClose();
  };
  const platformOptions = [
    { id: '', label: t('accounts.select_platform') },
    ...platforms.map((p) => ({ id: p, label: pName(p) })),
  ];
  const selectedPlatformLabel = platformOptions.find((item) => item.id === platform)?.label ?? t('accounts.select_platform');
  const proxyOptions = [
    { id: '', label: t('accounts.no_proxy') },
    ...(proxiesData?.list ?? []).map((p) => ({
      id: String(p.id),
      label: `${p.name} (${p.protocol}://${p.address}:${p.port})`,
    })),
  ];
  const selectedProxyLabel =
    proxyOptions.find((item) => item.id === (form.proxy_id == null ? '' : String(form.proxy_id)))?.label ?? t('accounts.no_proxy');
  const availableGroups = groupsData?.list ?? [];
  const toggleGroup = (id: number) => {
    setGroupIds((prev) =>
      prev.includes(id) ? prev.filter((groupId) => groupId !== id) : [...prev, id],
    );
  };
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) handleClose();
    },
  });

  return (
    <Modal state={modalState}>
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="lg">
          <Modal.Dialog
            className="ag-elevation-modal ag-create-account-modal"
          >
            <Modal.Header>
              <Modal.Heading>{t('accounts.create')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <Form className="ag-form-scroll-safe ag-create-account-form" onSubmit={(e) => e.preventDefault()}>
                <section className="space-y-4">
                  <div className="grid gap-4 md:grid-cols-2">
                    <Select
                      fullWidth
                      isRequired
                      selectedKey={platform}
                      onSelectionChange={(key) => handlePlatformChange(key == null ? '' : String(key))}
                    >
                      <Label>{t('accounts.platform')}</Label>
                      <Select.Trigger>
                        <Select.Value>{selectedPlatformLabel}</Select.Value>
                        <Select.Indicator />
                      </Select.Trigger>
                      <Select.Popover>
                        <ListBox items={platformOptions}>
                          {(item) => (
                            <ListBox.Item id={item.id} textValue={item.label}>
                              {item.label}
                            </ListBox.Item>
                          )}
                        </ListBox>
                      </Select.Popover>
                    </Select>

                    <HeroTextField fullWidth isRequired={!batchMode}>
                      <Label>{t('common.name')}</Label>
                      <div className="relative">
                        <Layers className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
                        <Input
                          className="pl-9"
                          name="name"
                          autoComplete="off"
                          value={form.name}
                          onChange={(e) => setForm({ ...form, name: e.target.value })}
                          required={!batchMode}
                        />
                      </div>
                    </HeroTextField>
                  </div>
                </section>

                {PluginAccountForm ? (
                  <section
                    className="ag-plugin-scope border-t border-border pt-4"
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
                      onBatchModeChange={setBatchMode}
                      onBatchImport={handlePluginBatchImport}
                      oauth={pluginOAuth}
                    />
                  </section>
                ) : schema && getSchemaVisibleFields(schema, accountType).length > 0 ? (
                  <SchemaCredentialsForm
                    schema={schema}
                    accountType={accountType}
                    onAccountTypeChange={handleSchemaAccountTypeChange}
                    credentials={credentials}
                    onCredentialsChange={setCredentials}
                  />
                ) : null}

                <section className="ag-create-account-advanced space-y-4">
                  <div className="grid gap-4 md:grid-cols-2">
                    <HeroTextField fullWidth>
                      <Label>{t('accounts.priority_hint')}</Label>
                      <div className="relative">
                        <Hash className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
                        <Input
                          className="pl-9"
                          type="number"
                          min={0}
                          max={999}
                          step={1}
                          value={String(form.priority ?? 50)}
                          onChange={(e) => {
                            const v = Math.round(Number(e.target.value));
                            setForm({ ...form, priority: Math.max(0, Math.min(999, v)) });
                          }}
                        />
                      </div>
                    </HeroTextField>

                    <HeroTextField fullWidth>
                      <Label>{t('accounts.concurrency')}</Label>
                      <div className="relative">
                        <Gauge className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
                        <Input
                          className="pl-9"
                          type="number"
                          value={String(form.max_concurrency ?? 5)}
                          onChange={(e) =>
                            setForm({ ...form, max_concurrency: Number(e.target.value) })
                          }
                        />
                      </div>
                    </HeroTextField>

                    <HeroTextField fullWidth>
                      <Label>{t('accounts.rate_multiplier')}</Label>
                      <Input
                        type="number"
                        step="0.1"
                        value={String(form.rate_multiplier ?? 1)}
                        onChange={(e) =>
                          setForm({ ...form, rate_multiplier: Number(e.target.value) })
                        }
                      />
                    </HeroTextField>

                    <Select
                      fullWidth
                      selectedKey={form.proxy_id == null ? '' : String(form.proxy_id)}
                      onSelectionChange={(key) =>
                        setForm({
                          ...form,
                          proxy_id: key ? Number(key) : undefined,
                        })
                      }
                    >
                      <Label>{t('accounts.proxy')}</Label>
                      <Select.Trigger>
                        <Select.Value>{selectedProxyLabel}</Select.Value>
                        <Select.Indicator />
                      </Select.Trigger>
                      <Select.Popover>
                        <ListBox items={proxyOptions}>
                          {(item) => (
                            <ListBox.Item id={item.id} textValue={item.label}>
                              {item.label}
                            </ListBox.Item>
                          )}
                        </ListBox>
                      </Select.Popover>
                    </Select>
                  </div>

                  <Switch
                    className="ag-create-account-pool-switch"
                    isSelected={form.upstream_is_pool ?? false}
                    onChange={(checked) => setForm({ ...form, upstream_is_pool: checked })}
                  >
                    <Switch.Control>
                      <Switch.Thumb />
                    </Switch.Control>
                    <Switch.Content>{t('accounts.upstream_is_pool', '池模式')}</Switch.Content>
                  </Switch>

                  {availableGroups.length > 0 && (
                    <div className="ag-create-account-groups">
                      <Label>{t('accounts.groups')}</Label>
                      <div className="ag-create-account-group-list">
                        {availableGroups.map((group) => (
                          <Checkbox
                            key={group.id}
                            className="ag-create-account-group-item"
                            isSelected={groupIds.includes(group.id)}
                            onChange={() => toggleGroup(group.id)}
                          >
                            <Checkbox.Control>
                              <Checkbox.Indicator />
                            </Checkbox.Control>
                            <Checkbox.Content>
                              <span className="min-w-0">
                                <span className="block truncate">{group.name}</span>
                                <span className="block truncate text-[10px] text-text-tertiary">
                                  {pName(group.platform)}
                                </span>
                              </span>
                            </Checkbox.Content>
                          </Checkbox>
                        ))}
                      </div>
                    </div>
                  )}
                </section>
              </Form>
            </Modal.Body>
            <Modal.Footer>
              <div className="flex justify-end gap-2 w-full">
                <Button variant="secondary" onPress={handleClose}>
                  {t('common.cancel')}
                </Button>
                {!batchMode && (
                  <Button
                    variant="primary"
                    onPress={handleSubmit}
                    isDisabled={loading || !platform || !form.name}
                    aria-busy={loading}
                  >
                    {t('common.create')}
                  </Button>
                )}
              </div>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
