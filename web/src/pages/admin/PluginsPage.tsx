import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Button,
  Chip,
  Input,
  Label,
  Modal,
  Spinner,
  TextArea,
  TextField as HeroTextField,
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

  const enabledCount = data.filter((plugin) => plugin.enabled).length;
  const runningPlugin = data.find((plugin) => plugin.running);

  return (
    <div className="ag-page-body ag-plugins-page">
      <header className="ag-plugins-overview">
        <div className="ag-plugins-overview__side">
          <dl className="ag-plugins-overview__stats">
            <div>
              <dt>{t('plugins.installed_label')}</dt>
              <dd>{data.length}</dd>
            </div>
            <div>
              <dt>{t('plugins.enabled_label')}</dt>
              <dd>{enabledCount}</dd>
            </div>
            <div className="ag-plugins-overview__runtime" data-running={runningPlugin ? 'true' : 'false'}>
              <dt>{t('plugins.runtime_label')}</dt>
              <dd>
                <span />
                {runningPlugin ? t('plugins.status_running') : t('plugins.runtime_idle')}
              </dd>
            </div>
          </dl>

          <div className="ag-plugins-overview__actions">
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
        </div>
      </header>

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
                <div className="flex items-start gap-3">
                  <div className="ag-plugin-card__icon">
                    <PlugZap className="h-4 w-4" />
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                      <h2 className="truncate text-sm font-semibold text-text">{plugin.name || plugin.id}</h2>
                      <span className="font-mono text-[10px] text-text-tertiary">{plugin.id}</span>
                    </div>
                    {plugin.description ? (
                      <p className="mt-1 line-clamp-2 text-xs leading-5 text-text-secondary" title={plugin.description}>
                        {plugin.description}
                      </p>
                    ) : null}
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
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
                      onChange={(enabled) => toggleMutation.mutate({ id: plugin.id, enabled })}
                    />
                  </div>
                </div>

                {plugin.error ? (
                  <div className="mt-3 border-l-2 border-danger bg-danger/5 px-3 py-2 text-xs text-danger" title={plugin.error}>
                    <div className="line-clamp-2">{plugin.error}</div>
                  </div>
                ) : null}

                <div className="ag-plugin-card__meta">
                  <div className="min-w-0">
                    <div className="text-[10px] text-text-tertiary">{t('plugins.type')}</div>
                    <div className="mt-1 flex min-w-0 items-center gap-2">
                      <Chip size="sm" variant="soft">{plugin.type || t('plugins.type_unknown')}</Chip>
                      <span className="font-mono text-[10px] text-text-tertiary" title={t('plugins.priority')}>
                        P{plugin.priority}
                      </span>
                    </div>
                  </div>
                  <div className="min-w-0">
                    <div className="text-[10px] text-text-tertiary">{t('plugins.version')}</div>
                    <div className="mt-1 truncate font-mono text-xs text-text">
                      {plugin.version || '—'}
                      {plugin.protocol_version ? (
                        <span className="ml-2 text-[10px] text-text-tertiary">
                          {t('plugins.protocol_version', { version: plugin.protocol_version })}
                        </span>
                      ) : null}
                    </div>
                  </div>
                  <div className="min-w-0">
                    <div className="text-[10px] text-text-tertiary">{t('plugins.source')}</div>
                    <div className="mt-1 truncate text-xs text-text" title={plugin.source}>
                      {formatSource(plugin.source, t)}
                      <span className="ml-2 font-mono text-[10px] text-text-tertiary">{formatBytes(plugin.binary_size)}</span>
                    </div>
                  </div>
                  <div className="col-span-2 min-w-0 sm:col-span-3">
                    <div className="text-[10px] text-text-tertiary">{t('plugins.capabilities')}</div>
                    <div className="mt-1 flex min-h-5 flex-wrap gap-1">
                      {(plugin.capabilities ?? []).length > 0 ? plugin.capabilities.map((capability) => (
                        <span
                          className="max-w-full truncate rounded-[var(--radius-sm)] border border-border px-1.5 py-0.5 font-mono text-[10px] text-text-secondary"
                          key={capability}
                          title={capability}
                        >
                          {capability}
                        </span>
                      )) : (
                        <span className="text-[10px] text-text-tertiary">{t('plugins.capabilities_none')}</span>
                      )}
                    </div>
                  </div>
                </div>

                <div className="ag-plugin-card__footer">
                  <span className="mr-auto text-[10px] text-text-tertiary">
                    {t('plugins.updated_at')} {formatDateTime(plugin.updated_at)}
                  </span>
                  <Button size="sm" variant="secondary" onPress={() => setConfigTarget(plugin)}>
                    <FileSliders className="h-3.5 w-3.5" />
                    {t('plugins.configure')}
                  </Button>
                  <Button
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
                    className="text-danger"
                    size="sm"
                    variant="danger-soft"
                    onPress={() => setUninstallTarget(plugin)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
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
      <PluginConfigModal
        plugin={configTarget}
        onClose={() => setConfigTarget(null)}
        onSaved={() => {
          setConfigTarget(null);
          void refreshList();
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
                      placeholder="https://example.com/airgate-overage"
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
  const [config, setConfig] = useState('');
  const open = !!plugin;
  const { data, isLoading } = useQuery({
    queryKey: queryKeys.pluginConfig(plugin?.id || ''),
    queryFn: () => pluginsApi.getConfig(plugin!.id),
    enabled: open,
  });

  useEffect(() => {
    if (open && data) setConfig(data.config);
    if (!open) setConfig('');
  }, [data, open]);

  const saveMutation = useMutation({
    mutationFn: () => pluginsApi.updateConfig(plugin!.id, config),
    onSuccess: () => {
      toast('success', t('plugins.config_save_success'));
      onSaved();
    },
    onError: (error: Error) => toast('error', error.message),
  });

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
        <Modal.Container placement="center" scroll="inside" size="lg">
          <Modal.Dialog className="ag-elevation-modal" style={{ maxWidth: '760px', width: 'min(100%, calc(100vw - 2rem))' }}>
            <Modal.Header>
              <Modal.Heading>{t('plugins.config_title', { name: plugin?.name || plugin?.id })}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              {isLoading ? (
                <div className="flex min-h-64 items-center justify-center"><Spinner /></div>
              ) : (
                <HeroTextField fullWidth>
                  <Label>{t('plugins.yaml_config')}</Label>
                  <TextArea
                    autoFocus
                    className="min-h-80 font-mono text-xs leading-5"
                    rows={20}
                    spellCheck={false}
                    value={config}
                    onChange={(event) => setConfig(event.target.value)}
                  />
                </HeroTextField>
              )}
              {plugin?.running ? (
                <div className="mt-3 text-xs text-text-tertiary">{t('plugins.config_reload_hint')}</div>
              ) : null}
            </Modal.Body>
            <Modal.Footer>
              <Button isDisabled={saveMutation.isPending} variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button isDisabled={isLoading || saveMutation.isPending} variant="primary" onPress={() => saveMutation.mutate()}>
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
