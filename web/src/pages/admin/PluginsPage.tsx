import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Button,
  Checkbox,
  Chip,
  Input,
  Label,
  Modal,
  Spinner,
  TextField as HeroTextField,
  Tooltip,
  useOverlayState,
} from '@heroui/react';
import {
  FileCode2,
  FileSliders,
  Link2,
  PackagePlus,
  PlugZap,
  RefreshCw,
  Trash2,
  Upload,
} from 'lucide-react';
import { pluginsApi, type PluginStatus } from '../../shared/api/plugins';
import { groupsApi } from '../../shared/api/groups';
import { FETCH_ALL_PARAMS } from '../../shared/constants';
import { queryKeys } from '../../shared/queryKeys';
import { useToast } from '../../shared/ui';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';
import { DialogTriggerShim } from '../../shared/components/DialogTriggerShim';
import { NativeSwitch } from '../../shared/components/NativeSwitch';
import { RefreshButton } from '../../shared/components/RefreshButton';
import { formatDateTime } from '../../shared/utils/format';

const MAX_PLUGIN_SIZE = 500 * 1024 * 1024;

export default function PluginsPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const [installOpen, setInstallOpen] = useState(false);
  const [configTarget, setConfigTarget] = useState<PluginStatus | null>(null);
  const [enableAfterConfig, setEnableAfterConfig] = useState(false);
  const [updateTarget, setUpdateTarget] = useState<PluginStatus | null>(null);
  const [uninstallTarget, setUninstallTarget] = useState<PluginStatus | null>(null);

  const { data = [], isFetching, isLoading, refetch } = useQuery({
    queryKey: queryKeys.plugins(),
    queryFn: pluginsApi.list,
  });

  const refreshList = () => queryClient.invalidateQueries({ queryKey: queryKeys.plugins() });

  const toggleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => pluginsApi.setEnabled(id, enabled),
    onSuccess: (_result, variables) => {
      void refreshList();
      toast('success', variables.enabled ? t('plugins.enable_success') : t('plugins.disable_success'));
    },
    onError: (error: Error) => toast('error', error.message),
  });

  const reloadMutation = useMutation({
    mutationFn: (id: string) => pluginsApi.reload(id),
    onSuccess: () => {
      void refreshList();
      toast('success', t('plugins.reload_success'));
    },
    onError: (error: Error) => toast('error', error.message),
  });

  const uninstallMutation = useMutation({
    mutationFn: (id: string) => pluginsApi.uninstall(id),
    onSuccess: () => {
      setUninstallTarget(null);
      void refreshList();
      toast('success', t('plugins.uninstall_success'));
    },
    onError: (error: Error) => toast('error', error.message),
  });

  const handleToggle = (plugin: PluginStatus, enabled: boolean) => {
    if (enabled && plugin.config_schema?.fields.length && !plugin.config_ready) {
      setEnableAfterConfig(true);
      setConfigTarget(plugin);
      return;
    }
    toggleMutation.mutate({ id: plugin.id, enabled });
  };

  return (
    <div className="ag-page-body ag-plugins-page">
      <div className="ag-plugins-toolbar">
        <RefreshButton
          ariaLabel={t('common.refresh')}
          isRefreshing={isFetching}
          onRefresh={refetch}
        />
        <Button variant="primary" onPress={() => setInstallOpen(true)}>
          <PackagePlus className="h-4 w-4" />
          {t('plugins.install')}
        </Button>
      </div>

      {isLoading ? (
        <div className="ag-plugins-loading" role="status">
          <Spinner size="lg" />
          <span>{t('common.loading')}</span>
        </div>
      ) : data.length === 0 ? (
        <div
          aria-label={t('plugins.empty')}
          className="ag-plugins-empty-state"
          role="status"
        >
          <div className="ag-plugins-empty-state__visual" aria-hidden="true">
            <div className="ag-plugins-empty-state__core">
              <PlugZap className="h-7 w-7" />
            </div>
          </div>
        </div>
      ) : (
        <div className="ag-plugins-grid">
          {data.map((plugin) => {
            const isToggling = toggleMutation.isPending && toggleMutation.variables?.id === plugin.id;
            const isReloading = reloadMutation.isPending && reloadMutation.variables === plugin.id;
            return (
              <article
                className="ag-plugin-card"
                data-state={plugin.running ? 'running' : plugin.enabled ? 'enabled' : 'disabled'}
                key={plugin.id}
              >
                <header className="ag-plugin-card__header">
                  <div className="ag-plugin-card__icon">
                    <PlugZap className="h-[1.125rem] w-[1.125rem]" />
                  </div>
                  <div className="ag-plugin-card__runtime">
                    {isToggling ? <Spinner size="sm" /> : (
                      <Chip color={plugin.running ? 'success' : plugin.enabled ? 'warning' : 'default'} size="sm" variant="soft">
                        {plugin.running
                          ? t('plugins.status_running')
                          : plugin.enabled
                            ? t('plugins.status_waiting')
                            : t('status.disabled')}
                      </Chip>
                    )}
                    <NativeSwitch
                      ariaLabel={plugin.enabled ? t('common.disable') : t('common.enable')}
                      isDisabled={toggleMutation.isPending}
                      isSelected={plugin.enabled}
                      onChange={(enabled) => handleToggle(plugin, enabled)}
                    />
                  </div>
                </header>

                <div className="ag-plugin-card__identity">
                  <h2 className="ag-plugin-card__title" title={plugin.name || plugin.id}>
                    {plugin.name || plugin.id}
                  </h2>
                  <span className="ag-plugin-card__id" title={plugin.id}>{plugin.id}</span>
                  {plugin.description ? (
                    <p className="ag-plugin-card__description" title={plugin.description}>
                      {plugin.description}
                    </p>
                  ) : null}
                </div>

                {plugin.error ? (
                  <div className="ag-plugin-card__error" title={plugin.error}>
                    <div className="line-clamp-2">{plugin.error}</div>
                  </div>
                ) : null}

                <div className="ag-plugin-card__meta">
                  <div className="ag-plugin-card__meta-item">
                    <div className="ag-plugin-card__meta-label">{t('plugins.type')}</div>
                    <div className="ag-plugin-card__meta-value ag-plugin-card__meta-value--inline">
                      <Chip size="sm" variant="soft">{plugin.type || t('plugins.type_unknown')}</Chip>
                      <span className="ag-plugin-card__priority" title={t('plugins.priority')}>
                        P{plugin.priority}
                      </span>
                    </div>
                  </div>
                  <div className="ag-plugin-card__meta-item">
                    <div className="ag-plugin-card__meta-label">{t('plugins.version')}</div>
                    <div className="ag-plugin-card__meta-value ag-plugin-card__version">
                      {plugin.version || '—'}
                      {plugin.protocol_version ? (
                        <span className="ag-plugin-card__protocol-version">
                          {t('plugins.protocol_version', { version: plugin.protocol_version })}
                        </span>
                      ) : null}
                    </div>
                  </div>
                  <div className="ag-plugin-card__meta-item ag-plugin-card__meta-item--wide">
                    <div className="ag-plugin-card__meta-label">{t('plugins.source')}</div>
                    <div className="ag-plugin-card__meta-value ag-plugin-card__source" title={plugin.source}>
                      <span className="truncate">{formatSource(plugin.source, t)}</span>
                      <span className="ag-plugin-card__binary-size">{formatBytes(plugin.binary_size)}</span>
                    </div>
                  </div>
                  <div className="ag-plugin-card__meta-item ag-plugin-card__meta-item--capabilities">
                    <div className="ag-plugin-card__meta-label">{t('plugins.capabilities')}</div>
                    <div className="ag-plugin-card__capabilities">
                      {(plugin.capabilities ?? []).length > 0 ? plugin.capabilities.map((capability) => (
                        <span
                          className="ag-plugin-card__capability"
                          key={capability}
                          title={capability}
                        >
                          {capability}
                        </span>
                      )) : (
                        <span className="ag-plugin-card__capabilities-empty">{t('plugins.capabilities_none')}</span>
                      )}
                    </div>
                  </div>
                </div>

                <div className="ag-plugin-card__footer">
                  <span className="ag-plugin-card__updated-at">
                    {t('plugins.updated_at')} {formatDateTime(plugin.updated_at)}
                  </span>
                  <div className="ag-plugin-card__actions">
                    <Tooltip>
                      <Tooltip.Trigger className="inline-flex">
                        <Button
                          isIconOnly
                          aria-label={t('plugins.update')}
                          className="ag-plugin-card__update"
                          size="sm"
                          variant="secondary"
                          onPress={() => setUpdateTarget(plugin)}
                        >
                          <Upload className="h-3.5 w-3.5" />
                        </Button>
                      </Tooltip.Trigger>
                      <Tooltip.Content>{t('plugins.update')}</Tooltip.Content>
                    </Tooltip>
                    <Button
                      className="ag-plugin-card__action"
                      isDisabled={!plugin.config_schema?.fields.length}
                      size="sm"
                      variant="secondary"
                      onPress={() => {
                        setEnableAfterConfig(false);
                        setConfigTarget(plugin);
                      }}
                    >
                      <FileSliders className="h-3.5 w-3.5" />
                      {t('plugins.configure')}
                    </Button>
                    <Button
                      className="ag-plugin-card__action"
                      isDisabled={!plugin.enabled || reloadMutation.isPending}
                      size="sm"
                      variant="secondary"
                      onPress={() => reloadMutation.mutate(plugin.id)}
                    >
                      <RefreshCw className={`h-3.5 w-3.5 ${isReloading ? 'animate-spin' : ''}`} />
                      {t('plugins.reload')}
                    </Button>
                    <Button
                      isIconOnly
                      aria-label={t('plugins.uninstall')}
                      className="ag-plugin-card__delete text-danger"
                      size="sm"
                      variant="danger-soft"
                      onPress={() => setUninstallTarget(plugin)}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                </div>
              </article>
            );
          })}
        </div>
      )}

      <InstallPluginModal
        open={installOpen}
        onClose={() => setInstallOpen(false)}
        onInstalled={() => {
          setInstallOpen(false);
          void refreshList();
        }}
      />
      <UpdatePluginModal
        plugin={updateTarget}
        onClose={() => setUpdateTarget(null)}
        onUpdated={() => {
          setUpdateTarget(null);
          void refreshList();
        }}
      />
      <PluginConfigModal
        plugin={configTarget}
        onClose={() => {
          setEnableAfterConfig(false);
          setConfigTarget(null);
        }}
        onSaved={() => {
          const savedPlugin = configTarget;
          setConfigTarget(null);
          void refreshList();
          if (enableAfterConfig && savedPlugin) {
            setEnableAfterConfig(false);
            toggleMutation.mutate({ id: savedPlugin.id, enabled: true });
          }
        }}
      />
      <ConfirmDialog
        description={t('plugins.uninstall_confirm', { name: uninstallTarget?.name || uninstallTarget?.id })}
        loading={uninstallMutation.isPending}
        open={!!uninstallTarget}
        title={t('plugins.uninstall_title')}
        onConfirm={() => uninstallTarget && uninstallMutation.mutate(uninstallTarget.id)}
        onOpenChange={(open) => {
          if (!open) setUninstallTarget(null);
        }}
      />
    </div>
  );
}

function UpdatePluginModal({
  plugin,
  onClose,
  onUpdated,
}: {
  plugin: PluginStatus | null;
  onClose: () => void;
  onUpdated: () => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const open = !!plugin;

  useEffect(() => {
    if (!open) setFile(null);
  }, [open]);

  const updateMutation = useMutation({
    mutationFn: () => pluginsApi.updateBinary(plugin!.id, file!),
    onSuccess: () => {
      toast('success', t('plugins.update_success'));
      onUpdated();
    },
    onError: (error: Error) => toast('error', error.message),
  });

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen && !updateMutation.isPending) onClose();
    },
  });

  const handleUpdate = () => {
    if (!file) {
      toast('error', t('plugins.select_file'));
      return;
    }
    if (file.size > MAX_PLUGIN_SIZE) {
      toast('error', t('plugins.file_too_large'));
      return;
    }
    updateMutation.mutate();
  };

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="sm">
          <Modal.Dialog className="ag-elevation-modal ag-plugin-install-modal">
            <Modal.Header>
              <Modal.Heading>{t('plugins.update_title', { name: plugin?.name || plugin?.id })}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <input
                ref={fileInputRef}
                className="hidden"
                type="file"
                onChange={(event) => setFile(event.target.files?.[0] ?? null)}
              />
              <button
                className="flex w-full items-center gap-3 rounded-[var(--radius-lg)] border border-dashed border-border px-4 py-4 text-left transition-colors hover:border-primary hover:bg-primary-subtle"
                type="button"
                onClick={() => fileInputRef.current?.click()}
              >
                <div className="grid h-10 w-10 shrink-0 place-items-center rounded-[var(--radius)] bg-default-100 text-text-secondary">
                  <FileCode2 className="h-5 w-5" />
                </div>
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium text-text">{file?.name || t('plugins.choose_binary')}</div>
                  <div className="mt-0.5 text-xs text-text-tertiary">
                    {file ? formatBytes(file.size) : t('plugins.binary_limit')}
                  </div>
                </div>
              </button>
            </Modal.Body>
            <Modal.Footer>
              <Button isDisabled={updateMutation.isPending} variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button isDisabled={updateMutation.isPending} variant="primary" onPress={handleUpdate}>
                {updateMutation.isPending ? <Spinner size="sm" /> : <Upload className="h-4 w-4" />}
                {t('plugins.update')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

function InstallPluginModal({
  open,
  onClose,
  onInstalled,
}: {
  open: boolean;
  onClose: () => void;
  onInstalled: () => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [mode, setMode] = useState<'upload' | 'url'>('upload');
  const [file, setFile] = useState<File | null>(null);
  const [url, setURL] = useState('');

  useEffect(() => {
    if (!open) {
      setMode('upload');
      setFile(null);
      setURL('');
    }
  }, [open]);

  const installMutation = useMutation({
    mutationFn: () => mode === 'upload'
      ? pluginsApi.upload(file!)
      : pluginsApi.installURL({ url: url.trim() }),
    onSuccess: () => {
      toast('success', t('plugins.install_success'));
      onInstalled();
    },
    onError: (error: Error) => toast('error', error.message),
  });

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen && !installMutation.isPending) onClose();
    },
  });

  const handleInstall = () => {
    if (mode === 'upload') {
      if (!file) {
        toast('error', t('plugins.select_file'));
        return;
      }
      if (file.size > MAX_PLUGIN_SIZE) {
        toast('error', t('plugins.file_too_large'));
        return;
      }
    } else if (!url.trim()) {
      toast('error', t('plugins.enter_url'));
      return;
    }
    installMutation.mutate();
  };

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="sm">
          <Modal.Dialog className="ag-elevation-modal ag-plugin-install-modal">
            <Modal.Header>
              <Modal.Heading>{t('plugins.install_title')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-5">
                <div className="inline-flex rounded-[var(--radius)] border border-border bg-default-50 p-1">
                  <Button size="sm" variant={mode === 'upload' ? 'primary' : 'ghost'} onPress={() => setMode('upload')}>
                    <Upload className="h-4 w-4" />
                    {t('plugins.install_upload')}
                  </Button>
                  <Button size="sm" variant={mode === 'url' ? 'primary' : 'ghost'} onPress={() => setMode('url')}>
                    <Link2 className="h-4 w-4" />
                    {t('plugins.install_url')}
                  </Button>
                </div>

                {mode === 'upload' ? (
                  <div>
                    <input
                      ref={fileInputRef}
                      className="hidden"
                      type="file"
                      onChange={(event) => setFile(event.target.files?.[0] ?? null)}
                    />
                    <button
                      className="flex w-full items-center gap-3 rounded-[var(--radius-lg)] border border-dashed border-border px-4 py-4 text-left transition-colors hover:border-primary hover:bg-primary-subtle"
                      type="button"
                      onClick={() => fileInputRef.current?.click()}
                    >
                      <div className="grid h-10 w-10 shrink-0 place-items-center rounded-[var(--radius)] bg-default-100 text-text-secondary">
                        <FileCode2 className="h-5 w-5" />
                      </div>
                      <div className="min-w-0">
                        <div className="truncate text-sm font-medium text-text">{file?.name || t('plugins.choose_binary')}</div>
                        <div className="mt-0.5 text-xs text-text-tertiary">
                          {file ? formatBytes(file.size) : t('plugins.binary_limit')}
                        </div>
                      </div>
                    </button>
                  </div>
                ) : (
                  <HeroTextField fullWidth isRequired>
                    <Label>{t('plugins.download_url')}</Label>
                    <Input
                      placeholder="https://example.com/airgate-codex-overage"
                      value={url}
                      onChange={(event) => setURL(event.target.value)}
                    />
                  </HeroTextField>
                )}

              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button isDisabled={installMutation.isPending} variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button isDisabled={installMutation.isPending} variant="primary" onPress={handleInstall}>
                {installMutation.isPending ? <Spinner size="sm" /> : <PackagePlus className="h-4 w-4" />}
                {t('plugins.install')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

function PluginConfigModal({
  plugin,
  onClose,
  onSaved,
}: {
  plugin: PluginStatus | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const [values, setValues] = useState<Record<string, unknown>>({});
  const open = !!plugin;
  const { data, error, isLoading } = useQuery({
    queryKey: queryKeys.pluginConfig(plugin?.id || ''),
    queryFn: () => pluginsApi.getConfig(plugin!.id),
    enabled: open,
  });
  const needsGroups = data?.schema.fields.some(
    (field) => field.widget === 'multi_select' && field.data_source === 'groups',
  ) ?? false;
  const { data: groupsData, isLoading: groupsLoading } = useQuery({
    queryKey: queryKeys.groups(FETCH_ALL_PARAMS),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
    enabled: open && needsGroups,
  });
  const groups = groupsData?.list ?? [];

  useEffect(() => {
    if (open && data) setValues({ ...data.values });
    if (!open) setValues({});
  }, [data, open]);

  const saveMutation = useMutation({
    mutationFn: () => pluginsApi.updateConfig(plugin!.id, values),
    onSuccess: () => {
      toast('success', t('plugins.config_save_success'));
      onSaved();
    },
    onError: (error: Error) => toast('error', error.message),
  });

  const updateMultiSelect = (key: string, id: number, selected: boolean) => {
    setValues((current) => {
      const selectedIDs = numberArray(current[key]);
      return {
        ...current,
        [key]: selected
          ? Array.from(new Set([...selectedIDs, id]))
          : selectedIDs.filter((item) => item !== id),
      };
    });
  };

  const missingRequiredValue = data?.schema.fields.some((field) => {
    if (!field.required) return false;
    const value = values[field.key];
    return value == null || value === '' || (Array.isArray(value) && value.length === 0);
  }) ?? false;

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen && !saveMutation.isPending) onClose();
    },
  });

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="sm">
          <Modal.Dialog className="ag-elevation-modal ag-plugin-config-modal">
            <Modal.Header>
              <Modal.Heading>{t('plugins.config_title', { name: plugin?.name || plugin?.id })}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              {isLoading ? (
                <div className="flex min-h-64 items-center justify-center"><Spinner /></div>
              ) : error ? (
                <div className="py-8 text-center text-sm text-danger">{(error as Error).message}</div>
              ) : (
                <div className="space-y-5">
                  {data?.schema.fields.map((field) => {
                    const fieldValue = values[field.key];
                    if (field.widget === 'multi_select' && field.data_source === 'groups') {
                      const selectedIDs = numberArray(fieldValue);
                      return (
                        <fieldset key={field.key}>
                          <legend className="mb-2 text-sm font-medium text-text">
                            {field.label}{field.required ? <span className="ml-1 text-danger">*</span> : null}
                          </legend>
                          <div className="max-h-72 overflow-y-auto rounded-[var(--radius)] border border-border p-1">
                            {groupsLoading ? (
                              <div className="flex min-h-28 items-center justify-center"><Spinner size="sm" /></div>
                            ) : groups.length === 0 ? (
                              <div className="py-8 text-center text-xs text-text-tertiary">{t('common.no_data')}</div>
                            ) : groups.map((group) => (
                              <Checkbox
                                className="flex w-full rounded-[var(--radius-sm)] px-3 py-2.5 hover:bg-default-50"
                                isSelected={selectedIDs.includes(group.id)}
                                key={group.id}
                                onChange={(selected) => updateMultiSelect(field.key, group.id, selected)}
                              >
                                <Checkbox.Control>
                                  <Checkbox.Indicator />
                                </Checkbox.Control>
                                <span className="min-w-0 truncate text-sm text-text">{group.name}</span>
                              </Checkbox>
                            ))}
                          </div>
                        </fieldset>
                      );
                    }
                    if (field.widget === 'text') {
                      return (
                        <HeroTextField fullWidth isRequired={field.required} key={field.key}>
                          <Label>{field.label}</Label>
                          <Input
                            value={typeof fieldValue === 'string' ? fieldValue : ''}
                            onChange={(event) => setValues((current) => ({ ...current, [field.key]: event.target.value }))}
                          />
                        </HeroTextField>
                      );
                    }
                    return (
                      <div className="rounded-[var(--radius)] border border-border px-3 py-2 text-sm text-text-secondary" key={field.key}>
                        {field.label}: {t('plugins.config_widget_unsupported')}
                      </div>
                    );
                  })}
                </div>
              )}
            </Modal.Body>
            <Modal.Footer>
              <Button isDisabled={saveMutation.isPending} variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button
                isDisabled={isLoading || !!error || groupsLoading || missingRequiredValue || saveMutation.isPending}
                variant="primary"
                onPress={() => saveMutation.mutate()}
              >
                {saveMutation.isPending ? <Spinner size="sm" /> : <FileSliders className="h-4 w-4" />}
                {t('common.save')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

function numberArray(value: unknown): number[] {
  if (!Array.isArray(value)) return [];
  return value
    .map((item) => Number(item))
    .filter((item) => Number.isInteger(item) && item > 0);
}

function formatBytes(size: number): string {
  if (!Number.isFinite(size) || size <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  const index = Math.min(Math.floor(Math.log(size) / Math.log(1024)), units.length - 1);
  const value = size / 1024 ** index;
  return `${value >= 10 || index === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[index]}`;
}

function formatSource(source: string, t: (key: string) => string): string {
  if (source.startsWith('upload:')) return t('plugins.source_upload');
  if (source.startsWith('http://') || source.startsWith('https://')) return t('plugins.source_url');
  return t('plugins.source_manual');
}
