import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  Button,
  Checkbox,
  Form,
  Input,
  Label,
  ListBox,
  Modal,
  Select,
  Switch,
  TextField as HeroTextField,
  useOverlayState,
} from '@heroui/react';
import { Gauge, Hash, Layers } from 'lucide-react';
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
import { SchemaCredentialsForm } from './CredentialForm';
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
    state: account.state === 'disabled' ? 'disabled' : 'active',
    priority: account.priority,
    max_concurrency: account.max_concurrency,
    rate_multiplier: account.rate_multiplier,
    upstream_is_pool: account.upstream_is_pool,
    proxy_id: account.proxy_id,
  });
  const origCredentials = useRef(account.credentials);
  const [credentials, setCredentials] = useState<Record<string, string>>(account.credentials);
  const [groupIds, setGroupIds] = useState<number[]>(account.group_ids ?? []);

  const { data: schema } = useQuery({
    queryKey: queryKeys.credentialsSchema(account.platform),
    queryFn: () => accountsApi.credentialsSchema(account.platform),
  });

  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
  });

  const { data: proxiesData } = useQuery({
    queryKey: queryKeys.proxiesAll(),
    queryFn: () => proxiesApi.list(FETCH_ALL_PARAMS),
  });

  const { Form: PluginAccountForm, pluginId } = usePluginAccountForm(account.platform);
  const pluginOAuth = createPluginOAuthBridge(pluginId);
  const passwordFieldsCleared = useRef(false);

  useEffect(() => {
    if (!schema || passwordFieldsCleared.current) return;
    const passwordKeys = getSchemaVisibleFields(schema, accountType)
      .filter((field) => field.type === 'password')
      .map((field) => field.key);
    if (passwordKeys.length === 0) return;

    passwordFieldsCleared.current = true;
    setCredentials((prev) => {
      const next = { ...prev };
      for (const key of passwordKeys) next[key] = '';
      return next;
    });
  }, [schema, accountType]);

  useEffect(() => {
    const selectedType = getSchemaSelectedAccountType(schema, accountType);
    if (!selectedType || selectedType.key === accountType) return;
    setAccountType(selectedType.key);
    setForm((prev) => ({ ...prev, type: selectedType.key || undefined }));
  }, [schema, accountType]);

  const handleAccountTypeChange = (type: string) => {
    setAccountType(type);
    setForm((prev) => ({ ...prev, type: type || undefined }));
  };

  const handleSchemaAccountTypeChange = (type: string) => {
    const selectedType = getSchemaSelectedAccountType(schema, type);
    handleAccountTypeChange(type);
    setCredentials((prev) => filterCredentialsForAccountType(prev, selectedType));
  };

  const handleSubmit = () => {
    const merged = { ...credentials };
    const passwordKeys = new Set(
      getSchemaVisibleFields(schema, accountType)
        .filter((field) => field.type === 'password')
        .map((field) => field.key),
    );

    for (const [key, value] of Object.entries(origCredentials.current)) {
      if (passwordKeys.has(key) && merged[key] === '' && value) merged[key] = value;
    }

    onSubmit({
      ...form,
      type: accountType || undefined,
      credentials: merged,
      group_ids: groupIds,
    });
  };

  const proxyOptions = [
    { id: '', label: t('accounts.no_proxy') },
    ...(proxiesData?.list ?? []).map((proxy) => ({
      id: String(proxy.id),
      label: `${proxy.name} (${proxy.protocol}://${proxy.address}:${proxy.port})`,
    })),
  ];
  const selectedProxyLabel =
    proxyOptions.find((item) => item.id === (form.proxy_id == null ? '' : String(form.proxy_id)))
      ?.label ?? t('accounts.no_proxy');
  const availableGroups = (groupsData?.list ?? []).filter(
    (group) => group.platform === account.platform,
  );

  const toggleGroup = (id: number) => {
    setGroupIds((prev) =>
      prev.includes(id) ? prev.filter((groupId) => groupId !== id) : [...prev, id],
    );
  };

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  return (
    <Modal state={modalState}>
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="lg">
          <Modal.Dialog className="ag-elevation-modal ag-create-account-modal">
            <Modal.Header>
              <Modal.Heading>{t('accounts.edit')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <Form
                className="ag-form-scroll-safe ag-create-account-form"
                onSubmit={(event) => event.preventDefault()}
                noValidate
              >
                <section className="space-y-4">
                  <div className="grid gap-4 md:grid-cols-2">
                    <HeroTextField fullWidth isDisabled>
                      <Label>{t('accounts.platform')}</Label>
                      <Input name="platform" value={pName(account.platform)} disabled />
                    </HeroTextField>

                    <HeroTextField fullWidth isRequired>
                      <Label>{t('common.name')}</Label>
                      <div className="relative">
                        <Layers className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                        <Input
                          className="pl-9"
                          name="name"
                          autoComplete="off"
                          value={form.name ?? ''}
                          onChange={(event) => setForm({ ...form, name: event.target.value })}
                          required
                        />
                      </div>
                    </HeroTextField>
                  </div>
                </section>

                {PluginAccountForm ? (
                  <section className="ag-plugin-scope border-t border-border pt-4">
                    <PluginAccountForm
                      credentials={credentials}
                      onChange={setCredentials}
                      mode="edit"
                      accountType={accountType}
                      onAccountTypeChange={handleAccountTypeChange}
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
                    mode="edit"
                  />
                ) : null}

                <section className="ag-create-account-advanced space-y-4">
                  <Switch
                    isSelected={form.state !== 'disabled'}
                    onChange={(enabled) =>
                      setForm({ ...form, state: enabled ? 'active' : 'disabled' })
                    }
                  >
                    <Switch.Control>
                      <Switch.Thumb />
                    </Switch.Control>
                    <Switch.Content>{t('accounts.enable_dispatch')}</Switch.Content>
                  </Switch>

                  <div className="grid gap-4 md:grid-cols-2">
                    <HeroTextField fullWidth>
                      <Label>{t('accounts.priority_hint')}</Label>
                      <div className="relative">
                        <Hash className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                        <Input
                          className="pl-9"
                          type="number"
                          min={0}
                          max={999}
                          step={1}
                          value={String(form.priority ?? 50)}
                          onChange={(event) => {
                            const value = Math.round(Number(event.target.value));
                            setForm({
                              ...form,
                              priority: Math.max(0, Math.min(999, value)),
                            });
                          }}
                        />
                      </div>
                    </HeroTextField>

                    <HeroTextField fullWidth>
                      <Label>{t('accounts.concurrency')}</Label>
                      <div className="relative">
                        <Gauge className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                        <Input
                          className="pl-9"
                          type="number"
                          value={String(form.max_concurrency ?? 5)}
                          onChange={(event) =>
                            setForm({ ...form, max_concurrency: Number(event.target.value) })
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
                        onChange={(event) =>
                          setForm({ ...form, rate_multiplier: Number(event.target.value) })
                        }
                      />
                    </HeroTextField>

                    <Select
                      fullWidth
                      selectedKey={form.proxy_id == null ? '' : String(form.proxy_id)}
                      onSelectionChange={(key) =>
                        setForm({
                          ...form,
                          proxy_id: key ? Number(key) : null,
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
              <div className="flex w-full justify-end gap-2">
                <Button variant="secondary" onPress={onClose}>
                  {t('common.cancel')}
                </Button>
                <Button
                  variant="primary"
                  onPress={handleSubmit}
                  isDisabled={loading || !form.name}
                  aria-busy={loading}
                >
                  {t('common.save')}
                </Button>
              </div>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
