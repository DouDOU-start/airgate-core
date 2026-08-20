import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Button,
  Checkbox,
  Chip,
  Input,
  Label,
  ListBox,
  Modal,
  Select,
  Spinner,
  TextArea,
  TextField as HeroTextField,
  Tooltip,
  useOverlayState,
} from '@heroui/react';
import {
  AlertTriangle,
  CheckCheck,
  CircleDollarSign,
  FileCode2,
  FileSliders,
  Layers3,
  Link2,
  PackageOpen,
  PackagePlus,
  PlugZap,
  RefreshCw,
  Settings2,
  Trash2,
  Upload,
  X,
} from 'lucide-react';
import {
  pluginsApi,
  type PluginConfigField,
  type PluginProviderInventory,
  type PluginProviderManualOrderRequest,
  type PluginProviderOrderStatus,
  type PluginProviderOverview,
  type PluginStatus,
} from '../../shared/api/plugins';
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
const PROVIDER_MANAGEMENT_CAPABILITY = 'account_provider_management.v1';

export default function PluginsPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const [installOpen, setInstallOpen] = useState(false);
  const [configTarget, setConfigTarget] = useState<PluginStatus | null>(null);
  const [enableAfterConfig, setEnableAfterConfig] = useState(false);
  const [updateTarget, setUpdateTarget] = useState<PluginStatus | null>(null);
  const [pickupTarget, setPickupTarget] = useState<PluginStatus | null>(null);
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
            const supportsPickup = plugin.capabilities?.includes(PROVIDER_MANAGEMENT_CAPABILITY) ?? false;
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
                  <div className="ag-plugin-card__actions" data-has-provider={supportsPickup ? 'true' : 'false'}>
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
                    {supportsPickup ? (
                      <Button
                        className="ag-plugin-card__action"
                        isDisabled={!plugin.running}
                        size="sm"
                        variant="secondary"
                        onPress={() => setPickupTarget(plugin)}
                      >
                        <PackageOpen className="h-3.5 w-3.5" />
                        {t('plugins.pickup')}
                      </Button>
                    ) : null}
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
      <PluginPickupModal
        plugin={pickupTarget ? data.find((plugin) => plugin.id === pickupTarget.id) ?? pickupTarget : null}
        onClose={() => setPickupTarget(null)}
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
                      placeholder="https://example.com/airgate-codex-enhance"
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

function PluginPickupModal({
  plugin,
  onClose,
}: {
  plugin: PluginStatus | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const initializedPluginRef = useRef('');
  const open = !!plugin;
  const [product, setProduct] = useState<'oauth_30d' | 'oauth_7d'>('oauth_30d');
  const [quantity, setQuantity] = useState(1);
  const [groupIDs, setGroupIDs] = useState<number[]>([]);
  const [priority, setPriority] = useState(50);
  const [maxConcurrency, setMaxConcurrency] = useState(10);
  const [inventory, setInventory] = useState<PluginProviderInventory | null>(null);
  const [orderStatus, setOrderStatus] = useState<PluginProviderOrderStatus | null>(null);

  const overviewQuery = useQuery({
    queryKey: ['plugin-provider-overview', plugin?.id ?? ''],
    queryFn: () => pluginsApi.action<PluginProviderOverview>(plugin!.id, 'overview'),
    enabled: open && plugin?.running === true,
  });
  const { data: groupsData, isLoading: groupsLoading } = useQuery({
    queryKey: queryKeys.groups(FETCH_ALL_PARAMS),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
    enabled: open,
  });
  const groups = groupsData?.list ?? [];

  useEffect(() => {
    if (!open) {
      initializedPluginRef.current = '';
      setInventory(null);
      setOrderStatus(null);
      return;
    }
    const overview = overviewQuery.data;
    if (!overview || initializedPluginRef.current === plugin?.id) return;
    initializedPluginRef.current = plugin?.id ?? '';
    setProduct(overview.defaults.product);
    setQuantity(overview.defaults.quantity);
    setGroupIDs(overview.defaults.group_ids);
    setPriority(overview.defaults.priority);
    setMaxConcurrency(overview.defaults.max_concurrency);
    setOrderStatus(overview.order);
  }, [open, overviewQuery.data, plugin?.id]);

  const statusQuery = useQuery({
    queryKey: ['plugin-provider-order-status', plugin?.id ?? ''],
    queryFn: () => pluginsApi.action<PluginProviderOrderStatus>(plugin!.id, 'orders/status'),
    enabled: open && plugin?.running === true && orderStatus?.pending === true,
    refetchInterval: 3000,
  });

  useEffect(() => {
    if (!statusQuery.data) return;
    setOrderStatus(statusQuery.data);
    if (!statusQuery.data.pending) void overviewQuery.refetch();
  }, [statusQuery.data]);

  const inventoryMutation = useMutation({
    mutationFn: () => pluginsApi.action<PluginProviderInventory>(plugin!.id, 'inventory', { product, quantity }),
    onSuccess: setInventory,
    onError: (error: Error) => toast('error', error.message),
  });

  const orderMutation = useMutation({
    mutationFn: (payload: PluginProviderManualOrderRequest) => (
      pluginsApi.action<PluginProviderOrderStatus>(plugin!.id, 'orders', payload)
    ),
    onSuccess: (result) => {
      setOrderStatus(result);
      toast('success', t('plugins.pickup_order_created'));
      void overviewQuery.refetch();
    },
    onError: (error: Error) => toast('error', error.message),
  });

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen && !inventoryMutation.isPending && !orderMutation.isPending) onClose();
    },
  });

  const toggleGroup = (id: number, selected: boolean) => {
    setGroupIDs((current) => selected
      ? Array.from(new Set([...current, id]))
      : current.filter((item) => item !== id));
  };

  const queryInventory = () => {
    if (quantity < 1 || quantity > 100) {
      toast('error', t('plugins.pickup_quantity_invalid'));
      return;
    }
    inventoryMutation.mutate();
  };

  const createOrder = () => {
    if (quantity < 1 || quantity > 100) {
      toast('error', t('plugins.pickup_quantity_invalid'));
      return;
    }
    if (groupIDs.length === 0) {
      toast('error', t('plugins.pickup_groups_required'));
      return;
    }
    if (priority < 0 || priority > 999) {
      toast('error', t('plugins.pickup_priority_invalid'));
      return;
    }
    if (maxConcurrency < 1 || maxConcurrency > 10000) {
      toast('error', t('plugins.pickup_concurrency_invalid'));
      return;
    }
    orderMutation.mutate({
      product, quantity, group_ids: groupIDs, priority, max_concurrency: maxConcurrency,
    });
  };

  const overview = overviewQuery.data;
  const displayedOrder = orderStatus?.order ?? orderStatus?.last_order ?? overview?.order.order ?? overview?.order.last_order;
  const pending = orderStatus?.pending ?? overview?.order.pending ?? false;
  const unavailable = plugin?.running !== true;

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="lg">
          <Modal.Dialog className="ag-elevation-modal ag-plugin-pickup-modal">
            <Modal.Header className="ag-plugin-pickup-modal__header">
              <div className="ag-plugin-pickup-modal__title">
                <span className="ag-plugin-pickup-modal__icon"><PackageOpen className="h-4 w-4" /></span>
                <span>
                  <Modal.Heading>{t('plugins.pickup_title')}</Modal.Heading>
                  <span className="ag-plugin-pickup-modal__plugin-name">{plugin?.name || plugin?.id}</span>
                </span>
              </div>
              <div className="ag-plugin-pickup-modal__header-actions">
                <Button
                  isIconOnly
                  aria-label={t('common.refresh')}
                  isDisabled={unavailable || overviewQuery.isFetching}
                  size="sm"
                  variant="ghost"
                  onPress={() => {
                    void overviewQuery.refetch();
                    if (pending) void statusQuery.refetch();
                  }}
                >
                  <RefreshCw className={`h-4 w-4 ${overviewQuery.isFetching ? 'animate-spin' : ''}`} />
                </Button>
                <Modal.CloseTrigger />
              </div>
            </Modal.Header>
            <Modal.Body className="ag-plugin-pickup-modal__body">
              {unavailable ? (
                <div className="ag-plugin-pickup-unavailable" role="status">
                  <AlertTriangle className="h-4 w-4" />
                  {t('plugins.pickup_unavailable')}
                </div>
              ) : overviewQuery.isLoading ? (
                <div className="ag-plugin-pickup-loading"><Spinner size="lg" /></div>
              ) : overviewQuery.error ? (
                <div className="ag-plugin-pickup-unavailable" role="alert">
                  <AlertTriangle className="h-4 w-4" />
                  <span>{overviewQuery.error.message}</span>
                  <Button size="sm" variant="secondary" onPress={() => void overviewQuery.refetch()}>
                    {t('common.retry')}
                  </Button>
                </div>
              ) : overview ? (
                <div className="ag-plugin-pickup-content">
                  <section className="ag-plugin-pickup-section ag-plugin-pickup-section--balance">
                    <div className="ag-plugin-pickup-section__heading">
                      <CircleDollarSign className="h-4 w-4" />
                      <h3>{t('plugins.pickup_balance')}</h3>
                      <Chip color={overview.auto_refill_enabled ? 'success' : 'default'} size="sm" variant="soft">
                        {overview.auto_refill_enabled ? t('plugins.pickup_auto_on') : t('plugins.pickup_auto_off')}
                      </Chip>
                    </div>
                    <div className="ag-plugin-pickup-stats">
                      <PickupStat label={t('plugins.pickup_total_balance')} value={formatFen(overview.balance.balance_fen)} />
                      <PickupStat label={t('plugins.pickup_held_balance')} value={formatFen(overview.balance.held_fen)} tone="warning" />
                      <PickupStat label={t('plugins.pickup_available_balance')} value={formatFen(overview.balance.available_fen)} tone="success" />
                    </div>
                  </section>

                  {displayedOrder ? (
                    <section className="ag-plugin-pickup-order" data-pending={pending ? 'true' : 'false'}>
                      <div className="ag-plugin-pickup-order__status">
                        {pending ? <Spinner size="sm" /> : <CheckCheck className="h-4 w-4" />}
                        <span>{pending ? t('plugins.pickup_order_processing') : t('plugins.pickup_order_latest')}</span>
                      </div>
                      <code>{displayedOrder.id || t('plugins.pickup_order_creating')}</code>
                      <span>{pickupStatusLabel(displayedOrder.status, t)}</span>
                      <span>{t('plugins.pickup_order_quantity', { count: displayedOrder.quantity })}</span>
                    </section>
                  ) : null}

                  <section className="ag-plugin-pickup-section">
                    <div className="ag-plugin-pickup-section__heading">
                      <PackageOpen className="h-4 w-4" />
                      <h3>{t('plugins.pickup_quote_and_order')}</h3>
                    </div>
                    <div className="ag-plugin-pickup-form-grid">
                      <div className="ag-plugin-pickup-field">
                        <Label>{t('plugins.pickup_product')}</Label>
                        <Select
                          aria-label={t('plugins.pickup_product')}
                          fullWidth
                          selectedKey={product}
                          onSelectionChange={(key) => {
                            if (key === 'oauth_30d' || key === 'oauth_7d') {
                              setProduct(key);
                              setInventory(null);
                            }
                          }}
                        >
                          <Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger>
                          <Select.Popover>
                            <ListBox>
                              <ListBox.Item id="oauth_30d" textValue="Bug Team · 30D">Bug Team · 30D</ListBox.Item>
                              <ListBox.Item id="oauth_7d" textValue="普通 Team · 7D">普通 Team · 7D</ListBox.Item>
                            </ListBox>
                          </Select.Popover>
                        </Select>
                      </div>
                      <HeroTextField className="ag-plugin-pickup-field" fullWidth>
                        <Label>{t('plugins.pickup_quantity')}</Label>
                        <Input min={1} max={100} type="number" value={String(quantity)} onChange={(event) => {
                          setQuantity(Number(event.target.value));
                          setInventory(null);
                        }} />
                      </HeroTextField>
                      <div className="ag-plugin-pickup-query-action">
                        <Button isDisabled={inventoryMutation.isPending} variant="secondary" onPress={queryInventory}>
                          {inventoryMutation.isPending ? <Spinner size="sm" /> : <RefreshCw className="h-4 w-4" />}
                          {t('plugins.pickup_query_quote')}
                        </Button>
                      </div>
                    </div>

                    {inventory ? (
                      <div className="ag-plugin-pickup-quote" data-shortage={inventory.missing > 0 ? 'true' : 'false'}>
                        <PickupStat label={t('plugins.pickup_inventory')} value={String(inventory.available)} />
                        <PickupStat label={t('plugins.pickup_missing')} value={String(inventory.missing)} tone={inventory.missing > 0 ? 'warning' : 'success'} />
                        <PickupStat label={t('plugins.pickup_unit_price')} value={formatFen(inventory.estimated_unit_price_fen)} />
                        <PickupStat label={t('plugins.pickup_estimated_total')} value={formatFen(inventory.estimated_total_fen)} tone="accent" />
                        <span className="ag-plugin-pickup-quote__remaining">
                          {t('plugins.pickup_remaining_range', {
                            range: formatRemainingRange(inventory.minimum_remaining_seconds, inventory.maximum_remaining_seconds),
                          })}
                        </span>
                        {inventory.needs_production ? (
                          <Chip color="warning" size="sm" variant="soft">{t('plugins.pickup_needs_production')}</Chip>
                        ) : null}
                      </div>
                    ) : null}
                  </section>

                  <section className="ag-plugin-pickup-section">
                    <div className="ag-plugin-pickup-section__heading">
                      <Layers3 className="h-4 w-4" />
                      <h3>{t('plugins.pickup_import_settings')}</h3>
                      <span>{t('plugins.config_selected_count', { count: groupIDs.length })}</span>
                    </div>
                    <div className="ag-plugin-pickup-groups">
                      {groupsLoading ? <Spinner size="sm" /> : groups.map((group) => {
                        const selected = groupIDs.includes(group.id);
                        return (
                          <Checkbox
                            className="ag-plugin-config-option"
                            data-selected={selected ? 'true' : 'false'}
                            isSelected={selected}
                            key={group.id}
                            onChange={(nextSelected) => toggleGroup(group.id, nextSelected)}
                          >
                            <Checkbox.Control><Checkbox.Indicator /></Checkbox.Control>
                            <span className="ag-plugin-config-option__label" title={group.name}>{group.name}</span>
                          </Checkbox>
                        );
                      })}
                    </div>
                    <div className="ag-plugin-pickup-import-grid">
                      <HeroTextField className="ag-plugin-pickup-field" fullWidth>
                        <Label>{t('plugins.pickup_account_priority')}</Label>
                        <Input min={0} max={999} type="number" value={String(priority)} onChange={(event) => setPriority(Number(event.target.value))} />
                      </HeroTextField>
                      <HeroTextField className="ag-plugin-pickup-field" fullWidth>
                        <Label>{t('plugins.pickup_max_concurrency')}</Label>
                        <Input min={1} max={10000} type="number" value={String(maxConcurrency)} onChange={(event) => setMaxConcurrency(Number(event.target.value))} />
                      </HeroTextField>
                    </div>
                  </section>
                </div>
              ) : null}
            </Modal.Body>
            <Modal.Footer>
              <Button isDisabled={orderMutation.isPending} variant="secondary" onPress={onClose}>{t('common.close')}</Button>
              <Button
                isDisabled={unavailable || !overview || pending || orderMutation.isPending}
                variant="primary"
                onPress={createOrder}
              >
                {orderMutation.isPending ? <Spinner size="sm" /> : <PackagePlus className="h-4 w-4" />}
                {pending ? t('plugins.pickup_order_processing') : t('plugins.pickup_create_order')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

function PickupStat({ label, value, tone = 'default' }: { label: string; value: string; tone?: string }) {
  return (
    <div className="ag-plugin-pickup-stat" data-tone={tone}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function formatFen(value: number): string {
  return `¥${(Number(value || 0) / 100).toFixed(2)}`;
}

function formatRemainingRange(minimum: number, maximum: number): string {
  const format = (seconds: number) => `${Math.max(0, Math.floor(seconds / 60))} 分钟`;
  if (minimum <= 0 && maximum <= 0) return '—';
  if (minimum === maximum) return format(minimum);
  return `${format(minimum)} - ${format(maximum)}`;
}

function pickupStatusLabel(status: string, t: ReturnType<typeof useTranslation>['t']): string {
  const key = `plugins.pickup_status_${String(status || 'creating').toLowerCase()}`;
  return t(key, { defaultValue: status || t('plugins.pickup_order_creating') });
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
  const { data, error, isLoading, refetch } = useQuery({
    queryKey: queryKeys.pluginConfig(plugin?.id || ''),
    queryFn: () => pluginsApi.getConfig(plugin!.id),
    enabled: open,
  });
  const needsGroups = data?.schema.fields.some(
    (field) => (field.widget === 'multi_select' || field.widget === 'single_select')
      && field.data_source === 'groups',
  ) ?? false;
  const { data: groupsData, isLoading: groupsLoading } = useQuery({
    queryKey: queryKeys.groups(FETCH_ALL_PARAMS),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
    enabled: open && needsGroups,
  });
  const groups = groupsData?.list ?? [];
  const fields = data?.schema.fields ?? [];
  const scopeFields = fields.filter(
    (field) => field.widget === 'multi_select' && field.data_source === 'groups',
  );
  const settingFields = fields.filter(
    (field) => field.widget !== 'multi_select' || field.data_source !== 'groups',
  );

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

  const replaceMultiSelect = (key: string, ids: number[]) => {
    setValues((current) => ({ ...current, [key]: ids }));
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

  const renderScopeField = (field: PluginConfigField) => {
    const selectedIDs = numberArray(values[field.key]);
    const allSelected = groups.length > 0 && groups.every((group) => selectedIDs.includes(group.id));

    return (
      <fieldset className="ag-plugin-config-scope-field" key={field.key}>
        <div className="ag-plugin-config-scope-field__header">
          <div className="min-w-0">
            <legend className="ag-plugin-config-field-label">
              {field.label}
              {field.required ? <span className="text-danger">*</span> : null}
            </legend>
            {field.description ? (
              <p className="ag-plugin-config-field-description">{field.description}</p>
            ) : null}
          </div>
          <span className="ag-plugin-config-selection-count">
            {t('plugins.config_selected_count', { count: selectedIDs.length })}
          </span>
        </div>

        <div className="ag-plugin-config-selection-tools">
          <Button
            className="ag-plugin-config-selection-tool"
            isDisabled={groupsLoading || groups.length === 0 || allSelected}
            size="sm"
            variant="ghost"
            onPress={() => replaceMultiSelect(field.key, groups.map((group) => group.id))}
          >
            <CheckCheck className="h-3.5 w-3.5" />
            {t('plugins.config_select_all')}
          </Button>
          <Button
            className="ag-plugin-config-selection-tool"
            isDisabled={groupsLoading || selectedIDs.length === 0}
            size="sm"
            variant="ghost"
            onPress={() => replaceMultiSelect(field.key, [])}
          >
            <X className="h-3.5 w-3.5" />
            {t('plugins.config_clear')}
          </Button>
        </div>

        <div className="ag-plugin-config-options">
          {groupsLoading ? (
            <div className="ag-plugin-config-options__state"><Spinner size="sm" /></div>
          ) : groups.length === 0 ? (
            <div className="ag-plugin-config-options__state">{t('common.no_data')}</div>
          ) : groups.map((group) => {
            const selected = selectedIDs.includes(group.id);
            return (
              <Checkbox
                className="ag-plugin-config-option"
                data-selected={selected ? 'true' : 'false'}
                isSelected={selected}
                key={group.id}
                onChange={(nextSelected) => updateMultiSelect(field.key, group.id, nextSelected)}
              >
                <Checkbox.Control>
                  <Checkbox.Indicator />
                </Checkbox.Control>
                <span className="ag-plugin-config-option__label" title={group.name}>{group.name}</span>
              </Checkbox>
            );
          })}
        </div>
      </fieldset>
    );
  };

  const renderSettingField = (field: PluginConfigField) => {
    const fieldValue = values[field.key];
    if (field.widget === 'single_select' && field.data_source === 'groups') {
      const selectedID = Number(fieldValue) || null;
      const selectedGroup = groups.find((group) => group.id === selectedID);
      return (
        <div className="ag-plugin-config-text-field" key={field.key}>
          <Label className="ag-plugin-config-field-label">
            {field.label}
            {field.required ? <span className="text-danger">*</span> : null}
          </Label>
          {field.description ? (
            <p className="ag-plugin-config-field-description">{field.description}</p>
          ) : null}
          <Select
            aria-label={field.label}
            fullWidth
            selectedKey={selectedID}
            onSelectionChange={(key) => setValues((current) => ({
              ...current,
              [field.key]: key == null ? '' : Number(key),
            }))}
          >
            <Select.Trigger>
              <Select.Value>{selectedGroup?.name ?? field.label}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={groups}>
                {(group) => (
                  <ListBox.Item id={group.id} textValue={group.name}>
                    {group.name}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>
      );
    }
    if (field.widget === 'text') {
      return (
        <HeroTextField className="ag-plugin-config-text-field" fullWidth isRequired={field.required} key={field.key}>
          <Label className="ag-plugin-config-field-label">{field.label}</Label>
          {field.description ? (
            <p className="ag-plugin-config-field-description">{field.description}</p>
          ) : null}
          <Input
            autoComplete={field.secret ? 'new-password' : undefined}
            type={field.secret ? 'password' : 'text'}
            value={typeof fieldValue === 'string' ? fieldValue : String(fieldValue ?? '')}
            onChange={(event) => setValues((current) => ({ ...current, [field.key]: event.target.value }))}
          />
        </HeroTextField>
      );
    }
    if (field.widget === 'number') {
      return (
        <HeroTextField className="ag-plugin-config-text-field" fullWidth isRequired={field.required} key={field.key}>
          <Label className="ag-plugin-config-field-label">{field.label}</Label>
          {field.description ? (
            <p className="ag-plugin-config-field-description">{field.description}</p>
          ) : null}
          <Input
            max={field.max}
            min={field.min}
            step={field.step}
            type="number"
            value={String(fieldValue ?? '')}
            onChange={(event) => setValues((current) => ({ ...current, [field.key]: event.target.value }))}
          />
        </HeroTextField>
      );
    }
    if (field.widget === 'textarea') {
      const textValue = typeof fieldValue === 'string' ? fieldValue : '';
      return (
        <HeroTextField className="ag-plugin-config-textarea-field" fullWidth isRequired={field.required} key={field.key}>
          <div className="ag-plugin-config-textarea-field__header">
            <div className="min-w-0">
              <Label className="ag-plugin-config-field-label">{field.label}</Label>
              {field.description ? (
                <p className="ag-plugin-config-field-description">{field.description}</p>
              ) : null}
            </div>
            <span className="ag-plugin-config-character-count">
              {t('plugins.config_character_count', { count: textValue.length })}
            </span>
          </div>
          <TextArea
            className="ag-plugin-config-textarea"
            rows={7}
            spellCheck={false}
            value={textValue}
            onChange={(event) => setValues((current) => ({ ...current, [field.key]: event.target.value }))}
          />
        </HeroTextField>
      );
    }
    if (field.widget === 'switch') {
      const selected = boolValue(fieldValue, boolValue(field.default, false));
      return (
        <div className="ag-plugin-config-switch-field" data-selected={selected ? 'true' : 'false'} key={field.key}>
          <div className="ag-plugin-config-switch-field__copy">
            <div className="ag-plugin-config-field-label">
              {field.label}
              {field.required ? <span className="text-danger">*</span> : null}
            </div>
            {field.description ? (
              <p className="ag-plugin-config-field-description">{field.description}</p>
            ) : null}
          </div>
          <NativeSwitch
            ariaLabel={field.label}
            className="ag-plugin-config-switch-field__control"
            isSelected={selected}
            onChange={(nextSelected) => setValues((current) => ({ ...current, [field.key]: nextSelected }))}
          />
        </div>
      );
    }
    return (
      <div className="ag-plugin-config-unsupported" key={field.key}>
        <AlertTriangle className="h-4 w-4" />
        <span>{field.label}: {t('plugins.config_widget_unsupported')}</span>
      </div>
    );
  };

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="lg">
          <Modal.Dialog className="ag-elevation-modal ag-plugin-config-modal">
            <Modal.Header className="ag-plugin-config-modal__header">
              <div className="ag-plugin-config-modal__identity">
                <div className="ag-plugin-config-modal__icon" aria-hidden="true">
                  <FileSliders className="h-5 w-5" />
                </div>
                <div className="min-w-0">
                  <Modal.Heading>{t('plugins.config_title', { name: plugin?.name || plugin?.id })}</Modal.Heading>
                  <p className="ag-plugin-config-modal__subtitle">
                    {t('plugins.config_subtitle')}
                    {plugin?.id ? <code>{plugin.id}</code> : null}
                  </p>
                </div>
              </div>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body className="ag-plugin-config-modal__body">
              {isLoading ? (
                <div className="ag-plugin-config-state" role="status">
                  <Spinner />
                  <span>{t('plugins.config_loading')}</span>
                </div>
              ) : error ? (
                <div className="ag-plugin-config-state ag-plugin-config-state--error" role="alert">
                  <div className="ag-plugin-config-state__icon"><AlertTriangle className="h-5 w-5" /></div>
                  <strong>{t('plugins.config_load_failed')}</strong>
                  <span>{(error as Error).message}</span>
                  <Button size="sm" variant="secondary" onPress={() => void refetch()}>
                    <RefreshCw className="h-3.5 w-3.5" />
                    {t('plugins.config_retry')}
                  </Button>
                </div>
              ) : fields.length === 0 ? (
                <div className="ag-plugin-config-state" role="status">
                  <Settings2 className="h-5 w-5" />
                  <span>{t('plugins.config_empty')}</span>
                </div>
              ) : (
                <div className="ag-plugin-config-layout" data-has-scope={scopeFields.length > 0 ? 'true' : 'false'}>
                  {scopeFields.length > 0 ? (
                    <aside className="ag-plugin-config-scope">
                      <div className="ag-plugin-config-section-heading">
                        <span className="ag-plugin-config-section-heading__icon"><Layers3 className="h-4 w-4" /></span>
                        <div>
                          <h3>{t('plugins.config_scope_title')}</h3>
                          <p>{t('plugins.config_scope_description')}</p>
                        </div>
                      </div>
                      {scopeFields.map(renderScopeField)}
                    </aside>
                  ) : null}
                  {settingFields.length > 0 ? (
                    <section className="ag-plugin-config-settings">
                      <div className="ag-plugin-config-section-heading">
                        <span className="ag-plugin-config-section-heading__icon"><Settings2 className="h-4 w-4" /></span>
                        <div>
                          <h3>{t('plugins.config_settings_title')}</h3>
                          <p>{t('plugins.config_settings_description')}</p>
                        </div>
                      </div>
                      <div className="ag-plugin-config-settings__fields">
                        {settingFields.map(renderSettingField)}
                      </div>
                    </section>
                  ) : null}
                </div>
              )}
            </Modal.Body>
            <Modal.Footer className="ag-plugin-config-modal__footer">
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

function boolValue(value: unknown, fallback = false): boolean {
  if (typeof value === 'boolean') return value;
  if (typeof value === 'number') return value !== 0;
  if (typeof value === 'string') {
    const normalized = value.trim().toLowerCase();
    if (normalized === 'true' || normalized === '1' || normalized === 'yes' || normalized === 'on') return true;
    if (normalized === 'false' || normalized === '0' || normalized === 'no' || normalized === 'off') return false;
  }
  return fallback;
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
