import { useState, useRef, useEffect, type DragEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { pluginsApi } from '../../shared/api/plugins';
import { useToast } from '../../shared/components/Toast';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { FETCH_ALL_PARAMS } from '../../shared/constants';
import { Table, type Column } from '../../shared/components/Table';
import { Button } from '../../shared/components/Button';
import { Modal, ConfirmModal } from '../../shared/components/Modal';
import { Card } from '../../shared/components/Card';
import { Badge } from '../../shared/components/Badge';
import { Input } from '../../shared/components/Input';
import {
  Trash2, Download, Loader2, RefreshCw,
  Package, User, Tag, Plus, Upload, Github, Settings,
} from 'lucide-react';
import type { PluginResp, MarketplacePluginResp } from '../../shared/types';

// 插件类型 Badge 颜色
const typeVariant: Record<string, 'info' | 'success' | 'warning'> = {
  gateway: 'info',
  payment: 'success',
  extension: 'warning',
};

export default function PluginsPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const [activeTab, setActiveTab] = useState<'installed' | 'marketplace'>('installed');
  const [uninstallTarget, setUninstallTarget] = useState<PluginResp | null>(null);
  const [installOpen, setInstallOpen] = useState(false);
  const [configTarget, setConfigTarget] = useState<PluginResp | null>(null);

  // 已安装插件列表
  const { data: pluginsData, isLoading: pluginsLoading, refetch: refetchPlugins } = useQuery({
    queryKey: queryKeys.plugins(),
    queryFn: () => pluginsApi.list(FETCH_ALL_PARAMS),
  });

  // 插件市场列表
  const { data: marketData, isLoading: marketLoading } = useQuery({
    queryKey: queryKeys.marketplace(),
    queryFn: () => pluginsApi.marketplace(FETCH_ALL_PARAMS),
    enabled: activeTab === 'marketplace',
  });

  // 市场卡片直接安装（GitHub Release）
  const [installingRepo, setInstallingRepo] = useState<string | null>(null);
  const [isUpdating, setIsUpdating] = useState(false);
  const marketInstallMutation = useMutation({
    mutationFn: (repo: string) => pluginsApi.installGithub(repo),
    onSuccess: () => {
      toast('success', t(isUpdating ? 'plugins.update_success' : 'plugins.github_success'));
      // 插件前端模块需要整页重载才能生效
      window.location.reload();
    },
    onError: (err: Error) => {
      toast('error', err.message);
      setInstallingRepo(null);
      setIsUpdating(false);
    },
  });

  function handleMarketInstall(repo: string, update = false) {
    setInstallingRepo(repo);
    setIsUpdating(update);
    marketInstallMutation.mutate(repo);
  }

  // 强制从 GitHub 同步市场列表（点击右上角刷新按钮时触发）
  const refreshMarketMutation = useMutation({
    mutationFn: () => pluginsApi.refreshMarketplace(),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.marketplace() });
      toast('success', t('plugins.marketplace_refreshed'));
    },
    onError: (err: Error) => toast('error', err.message),
  });

  function handleHeaderRefresh() {
    if (activeTab === 'marketplace') {
      refreshMarketMutation.mutate();
    } else {
      refetchPlugins();
    }
  }

  // 卸载插件
  const uninstallMutation = useMutation({
    mutationFn: (name: string) => pluginsApi.uninstall(name),
    onSuccess: () => {
      toast('success', t('plugins.uninstall_success'));
      setUninstallTarget(null);
      // 插件卸载后需要整页重载以清理已加载的前端模块
      window.location.reload();
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 热加载插件
  const reloadMutation = useCrudMutation({
    mutationFn: (name: string) => pluginsApi.reload(name),
    successMessage: t('plugins.reload_success'),
    queryKey: queryKeys.plugins(),
  });

  const installedColumns: Column<PluginResp>[] = [
    {
      key: 'name',
      title: t('common.name'),
      render: (row) => (
        <div className="min-w-0 inline-flex items-center gap-2">
          <div className="text-text font-medium">
            {row.display_name || row.name}
          </div>
          {row.display_name && row.display_name !== row.name && (
            <span className="text-xs text-text-tertiary font-mono">
              {row.name}
            </span>
          )}
        </div>
      ),
    },
    {
      key: 'type',
      title: `${t('common.type')} / ${t('plugins.platform')}`,
      render: (row) => (
        <div className="flex items-center justify-center gap-2">
          <Badge variant={typeVariant[row.type || 'gateway'] || 'default'}>
            {row.type || 'gateway'}
          </Badge>
          {row.platform && (
            <span className="text-xs text-text-tertiary">{row.platform}</span>
          )}
          {row.version && (
            <span className="text-xs text-text-tertiary">
              {t('common.version')}: {row.version}
            </span>
          )}
          {row.is_dev && (
            <span className="text-[10px] font-medium uppercase tracking-wider text-warning/70 border border-warning/30 rounded px-1.5 py-px">
              {t('plugins.dev_badge')}
            </span>
          )}
        </div>
      ),
    },
    {
      key: 'actions',
      title: t('common.actions'),
      render: (row) => (
        <div className="flex gap-1 justify-center">
          {row.config_schema && row.config_schema.length > 0 && (
            <Button
              size="sm"
              variant="ghost"
              icon={<Settings className="w-3.5 h-3.5" />}
              onClick={() => setConfigTarget(row)}
            >
              配置
            </Button>
          )}
          {row.is_dev && (() => {
            // 只在当前正在重载的这一行显示 loading；用 mutation.variables 区分
            // 哪个 plugin 在途，避免点一个插件转全部
            const isReloadingThis =
              reloadMutation.isPending && reloadMutation.variables === row.name;
            return (
              <Button
                size="sm"
                variant="ghost"
                icon={<RefreshCw className={`w-3.5 h-3.5 ${isReloadingThis ? 'animate-spin' : ''}`} />}
                onClick={() => reloadMutation.mutate(row.name)}
                disabled={reloadMutation.isPending}
              >
                {t('plugins.reload')}
              </Button>
            );
          })()}
          <Button
            size="sm"
            variant="ghost"
            icon={<Trash2 className="w-3.5 h-3.5" />}
            className="text-danger"
            onClick={() => setUninstallTarget(row)}
          >
            {t('common.uninstall')}
          </Button>
        </div>
      ),
    },
  ];

  const tabs = [
    { key: 'installed' as const, label: t('plugins.installed_tab') },
    { key: 'marketplace' as const, label: t('plugins.marketplace_tab') },
  ];

  return (
    <div>
      {/* Tab 切换 + 操作按钮 */}
      <div className="flex items-center gap-1 mb-6 border-b border-border">
        {tabs.map((tab) => (
          <button
            key={tab.key}
            className={`px-4 py-2.5 text-xs font-semibold uppercase tracking-wider border-b-2 transition-all duration-200 cursor-pointer ${
              activeTab === tab.key
                ? 'border-primary text-primary shadow-[0_2px_8px_var(--ag-primary-glow)]'
                : 'border-transparent text-text-tertiary hover:text-text-secondary'
            }`}
            onClick={() => setActiveTab(tab.key)}
          >
            {tab.label}
          </button>
        ))}
        <div className="flex items-center gap-2 ml-auto pb-1">
          <button
            onClick={handleHeaderRefresh}
            disabled={refreshMarketMutation.isPending}
            className="flex items-center justify-center w-9 h-9 rounded-[10px] text-text-tertiary hover:text-text-secondary hover:bg-bg-hover transition-colors disabled:opacity-50"
          >
            <RefreshCw className={`w-4 h-4 ${refreshMarketMutation.isPending ? 'animate-spin' : ''}`} />
          </button>
          <Button
            icon={<Plus className="w-4 h-4" />}
            onClick={() => setInstallOpen(true)}
          >
            {t('plugins.install_plugin')}
          </Button>
        </div>
      </div>

      {/* 已安装 Tab */}
      {activeTab === 'installed' && (
        <Table
          columns={installedColumns}
          data={pluginsData?.list ?? []}
          loading={pluginsLoading}
          rowKey={(row) => row.name}
        />
      )}

      {/* 插件市场 Tab */}
      {activeTab === 'marketplace' && (
        <div>
          {marketLoading ? (
            <div className="flex items-center justify-center py-16">
              <Loader2 className="w-5 h-5 animate-spin text-primary" />
              <span className="ml-2 text-sm text-text-tertiary">{t('common.loading')}</span>
            </div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {(marketData?.list ?? []).map((plugin: MarketplacePluginResp) => (
                <MarketplaceCard
                  key={plugin.name}
                  plugin={plugin}
                  installing={installingRepo === plugin.github_repo && marketInstallMutation.isPending}
                  onInstall={handleMarketInstall}
                />
              ))}
              {(marketData?.list ?? []).length === 0 && (
                <div className="col-span-full text-center py-16 text-text-tertiary">
                  {t('plugins.no_plugins')}
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {/* 安装插件弹窗 */}
      <InstallPluginModal
        open={installOpen}
        onClose={() => setInstallOpen(false)}
        onInstalled={() => {
          setInstallOpen(false);
          // 插件前端模块需要整页重载才能生效
          window.location.reload();
        }}
      />

      {/* 卸载确认 */}
      <ConfirmModal
        open={!!uninstallTarget}
        onClose={() => setUninstallTarget(null)}
        onConfirm={() => uninstallTarget && uninstallMutation.mutate(uninstallTarget.name)}
        title={t('plugins.uninstall_title')}
        message={t('plugins.uninstall_confirm', { name: uninstallTarget?.name })}
        loading={uninstallMutation.isPending}
        danger
      />

      {/* 配置编辑 */}
      <PluginConfigModal
        plugin={configTarget}
        onClose={() => setConfigTarget(null)}
        onSaved={() => {
          setConfigTarget(null);
          refetchPlugins();
        }}
      />
    </div>
  );
}

// ============================================================================
// 插件配置编辑 Modal
// ============================================================================
function PluginConfigModal({
  plugin,
  onClose,
  onSaved,
}: {
  plugin: PluginResp | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { toast } = useToast();
  const [values, setValues] = useState<Record<string, string>>({});
  const open = !!plugin;

  // 拉取持久化配置作为初始值
  const { data: configData, isLoading } = useQuery({
    queryKey: ['plugin-config', plugin?.name],
    queryFn: () => pluginsApi.getConfig(plugin!.name),
    enabled: open,
  });

  useEffect(() => {
    if (!plugin) {
      setValues({});
      return;
    }
    // 用 schema 中的 default 兜底，再用持久化值覆盖
    const init: Record<string, string> = {};
    plugin.config_schema?.forEach((f) => {
      if (f.default !== undefined && f.default !== '') {
        init[f.key] = f.default;
      }
    });
    if (configData?.config) {
      Object.assign(init, configData.config);
    }
    setValues(init);
  }, [plugin, configData]);

  const saveMutation = useMutation({
    mutationFn: (cfg: Record<string, string>) => pluginsApi.updateConfig(plugin!.name, cfg),
    onSuccess: () => {
      toast('success', '配置已保存，插件已重新加载');
      onSaved();
    },
    onError: (err: Error) => toast('error', err.message),
  });

  function handleSave() {
    // 必填校验
    const missing = (plugin?.config_schema || [])
      .filter((f) => f.required && !values[f.key])
      .map((f) => f.label || f.key);
    if (missing.length > 0) {
      toast('error', `以下字段必填: ${missing.join(', ')}`);
      return;
    }
    saveMutation.mutate(values);
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={`配置 - ${plugin?.display_name || plugin?.name || ''}`}
      width="640px"
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={saveMutation.isPending}>
            取消
          </Button>
          <Button onClick={handleSave} loading={saveMutation.isPending}>
            保存并重新加载
          </Button>
        </>
      }
    >
      {isLoading ? (
        <div className="flex items-center justify-center py-10">
          <Loader2 className="w-5 h-5 animate-spin text-primary" />
        </div>
      ) : (
        <div className="space-y-4 max-h-[60vh] overflow-y-auto pr-2">
          {(plugin?.config_schema || []).map((field) => {
            const inputType =
              field.type === 'password' ? 'password' :
              field.type === 'int' || field.type === 'float' ? 'number' :
              'text';

            // bool 渲染为复选框
            if (field.type === 'bool') {
              const checked = values[field.key] === 'true';
              return (
                <div key={field.key}>
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={(e) => setValues({ ...values, [field.key]: e.target.checked ? 'true' : 'false' })}
                      className="w-4 h-4"
                    />
                    <span className="text-[13px] font-medium text-text-secondary">
                      {field.label || field.key}
                      {field.required && <span className="text-danger ml-1">*</span>}
                    </span>
                  </label>
                  {field.description && (
                    <p className="mt-1 ml-6 text-xs text-text-tertiary">{field.description}</p>
                  )}
                </div>
              );
            }

            return (
              <div key={field.key}>
                <Input
                  type={inputType}
                  label={`${field.label || field.key}${field.required ? ' *' : ''}`}
                  value={values[field.key] || ''}
                  placeholder={field.placeholder}
                  onChange={(e) => setValues({ ...values, [field.key]: e.target.value })}
                  hint={field.description}
                />
              </div>
            );
          })}
          {(!plugin?.config_schema || plugin.config_schema.length === 0) && (
            <p className="text-sm text-text-tertiary text-center py-4">
              该插件未声明任何配置项
            </p>
          )}
        </div>
      )}
    </Modal>
  );
}

// 安装插件弹窗
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
  const dragCounterRef = useRef(0);

  const [installTab, setInstallTab] = useState<'upload' | 'github'>('upload');
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [dragActive, setDragActive] = useState(false);
  const [pluginName, setPluginName] = useState('');
  const [githubRepo, setGithubRepo] = useState('');

  // 上传安装
  const uploadMutation = useMutation({
    mutationFn: () => pluginsApi.upload(selectedFile!, pluginName || undefined),
    onSuccess: () => {
      toast('success', t('plugins.upload_success'));
      resetForm();
      onInstalled();
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // GitHub 安装
  const githubMutation = useMutation({
    mutationFn: () => pluginsApi.installGithub(githubRepo),
    onSuccess: () => {
      toast('success', t('plugins.github_success'));
      resetForm();
      onInstalled();
    },
    onError: (err: Error) => toast('error', err.message),
  });

  function resetForm() {
    setSelectedFile(null);
    setPluginName('');
    setGithubRepo('');
    dragCounterRef.current = 0;
    if (fileInputRef.current) fileInputRef.current.value = '';
  }

  function handleClose() {
    resetForm();
    setDragActive(false);
    onClose();
  }

  function handleFileSelect(file: File | null) {
    setSelectedFile(file);
    setDragActive(false);
  }

  function handleDragEvent(e: DragEvent<HTMLDivElement>) {
    e.preventDefault();
    e.stopPropagation();
  }

  function handleDragEnter(e: DragEvent<HTMLDivElement>) {
    handleDragEvent(e);
    dragCounterRef.current += 1;
    setDragActive(true);
  }

  function handleDragLeave(e: DragEvent<HTMLDivElement>) {
    handleDragEvent(e);
    dragCounterRef.current = Math.max(0, dragCounterRef.current - 1);
    if (dragCounterRef.current === 0) {
      setDragActive(false);
    }
  }

  function handleDrop(e: DragEvent<HTMLDivElement>) {
    handleDragEvent(e);
    dragCounterRef.current = 0;
    handleFileSelect(e.dataTransfer.files?.[0] || null);
  }

  const installing = uploadMutation.isPending || githubMutation.isPending;

  const installTabs = [
    { key: 'upload' as const, label: t('plugins.upload_tab'), icon: <Upload className="w-3.5 h-3.5" /> },
    { key: 'github' as const, label: t('plugins.github_tab'), icon: <Github className="w-3.5 h-3.5" /> },
  ];

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title={t('plugins.install_plugin')}
      width="520px"
      footer={
        <>
          <Button variant="secondary" onClick={handleClose} disabled={installing}>
            {t('common.cancel')}
          </Button>
          {installTab === 'upload' ? (
            <Button
              onClick={() => uploadMutation.mutate()}
              loading={uploadMutation.isPending}
              disabled={!selectedFile}
            >
              {t('common.install')}
            </Button>
          ) : (
            <Button
              onClick={() => githubMutation.mutate()}
              loading={githubMutation.isPending}
              disabled={!githubRepo.trim()}
            >
              {t('common.install')}
            </Button>
          )}
        </>
      }
    >
      {/* 安装方式切换 */}
      <div className="flex gap-2 mb-5">
        {installTabs.map((tab) => (
          <button
            key={tab.key}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium transition-all cursor-pointer ${
              installTab === tab.key
                ? 'bg-primary text-white'
                : 'bg-surface text-text-secondary hover:bg-[var(--ag-bg-muted)]'
            }`}
            onClick={() => setInstallTab(tab.key)}
            disabled={installing}
          >
            {tab.icon}
            {tab.label}
          </button>
        ))}
      </div>

      {/* 上传安装 */}
      {installTab === 'upload' && (
        <div className="space-y-4">
          <div>
            <label className="block text-xs font-medium text-text-secondary uppercase tracking-wider mb-1.5">
              {t('plugins.plugin_file')} <span className="text-danger">*</span>
            </label>
            <div
              className={`border-2 border-dashed rounded-md p-6 text-center cursor-pointer transition-colors ${selectedFile
                  ? 'border-primary bg-primary-subtle'
                  : dragActive
                    ? 'border-border-focus bg-[var(--ag-bg-muted)]'
                    : 'border-glass-border hover:border-border-focus'
                }`}
              onClick={() => fileInputRef.current?.click()}
              onDragOver={handleDragEvent}
              onDragEnter={handleDragEnter}
              onDragLeave={handleDragLeave}
              onDrop={handleDrop}
            >
              <input
                ref={fileInputRef}
                type="file"
                className="hidden"
                onChange={(e) => handleFileSelect(e.target.files?.[0] || null)}
              />
              {selectedFile ? (
                <div className="flex items-center justify-center gap-2">
                  <Package className="w-5 h-5 text-primary" />
                  <span className="text-sm text-text">{selectedFile.name}</span>
                  <span className="text-xs text-text-tertiary">
                    ({(selectedFile.size / 1024 / 1024).toFixed(1)} MB)
                  </span>
                </div>
              ) : (
                <div>
                  <Upload className={`w-8 h-8 mx-auto mb-2 ${dragActive ? 'text-primary' : 'text-text-tertiary'}`} />
                  <p className="text-sm text-text-tertiary">
                    {t('plugins.upload_hint')}
                  </p>
                </div>
              )}
            </div>
          </div>
          <Input
            label={t('plugins.plugin_name')}
            value={pluginName}
            onChange={(e) => setPluginName(e.target.value)}
            placeholder={t('plugins.plugin_name_hint')}
          />
        </div>
      )}

      {/* GitHub 安装 */}
      {installTab === 'github' && (
        <div className="space-y-4">
          <Input
            label={t('plugins.github_repo')}
            value={githubRepo}
            onChange={(e) => setGithubRepo(e.target.value)}
            placeholder={t('plugins.github_repo_placeholder')}
            required
          />
          <p className="text-xs text-text-tertiary">
            {t('plugins.github_hint')}
          </p>
        </div>
      )}
    </Modal>
  );
}

// 插件市场卡片组件
function MarketplaceCard({
  plugin,
  installing,
  onInstall,
}: {
  plugin: MarketplacePluginResp;
  installing: boolean;
  onInstall: (repo: string, update?: boolean) => void;
}) {
  const { t } = useTranslation();
  const canInstall = !!plugin.github_repo;

  return (
    <Card>
      <div className="flex flex-col h-full">
        <div className="flex items-start justify-between mb-3">
          <div className="flex items-center gap-2">
            <Package className="w-4 h-4 text-primary" />
            <h3 className="font-semibold text-text">{plugin.name}</h3>
          </div>
          <Badge variant={typeVariant[plugin.type] || 'default'}>{plugin.type}</Badge>
        </div>
        <p className="text-sm text-text-tertiary flex-1 mb-4 leading-relaxed">
          {plugin.description || t('common.no_data_desc')}
        </p>
        <div className="flex items-center justify-between text-xs text-text-tertiary mb-3">
          <span className="flex items-center gap-1">
            <User className="w-3 h-3" />
            {plugin.author}
          </span>
          <span className="flex items-center gap-1 font-mono">
            <Tag className="w-3 h-3" />
            v{plugin.version}
          </span>
        </div>
        {plugin.github_repo && (
          <div className="flex items-center gap-1 text-xs text-text-tertiary mb-3 font-mono">
            <Github className="w-3 h-3" />
            {plugin.github_repo}
          </div>
        )}
        <div className="pt-3 border-t border-border">
          {plugin.installed ? (
            plugin.has_update ? (
              <div className="flex items-center gap-2">
                <Button
                  size="sm"
                  variant="primary"
                  icon={<RefreshCw className="w-3.5 h-3.5" />}
                  disabled={!canInstall || installing}
                  loading={installing}
                  onClick={() => plugin.github_repo && onInstall(plugin.github_repo, true)}
                >
                  {t('plugins.update_to', { version: plugin.version })}
                </Button>
                {plugin.installed_version && (
                  <span className="text-xs text-text-tertiary font-mono">
                    v{plugin.installed_version} → v{plugin.version}
                  </span>
                )}
              </div>
            ) : (
              <Badge variant="success">{t('plugins.already_installed')}</Badge>
            )
          ) : (
            <Button
              size="sm"
              icon={<Download className="w-3.5 h-3.5" />}
              disabled={!canInstall || installing}
              loading={installing}
              onClick={() => plugin.github_repo && onInstall(plugin.github_repo)}
            >
              {t('common.install')}
            </Button>
          )}
        </div>
      </div>
    </Card>
  );
}
