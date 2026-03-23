import { useState, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { pluginsApi } from '../../shared/api/plugins';
import { useToast } from '../../shared/components/Toast';
import { PageHeader } from '../../shared/components/PageHeader';
import { Table, type Column } from '../../shared/components/Table';
import { Button } from '../../shared/components/Button';
import { Modal, ConfirmModal } from '../../shared/components/Modal';
import { Card } from '../../shared/components/Card';
import { Badge } from '../../shared/components/Badge';
import { Input } from '../../shared/components/Input';
import {
  Trash2, Download, Loader2, RefreshCw,
  Package, User, Tag, Plus, Upload, Github,
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

  // 已安装插件列表
  const { data: pluginsData, isLoading: pluginsLoading } = useQuery({
    queryKey: ['plugins'],
    queryFn: () => pluginsApi.list({ page: 1, page_size: 100 }),
  });

  // 插件市场列表
  const { data: marketData, isLoading: marketLoading } = useQuery({
    queryKey: ['marketplace'],
    queryFn: () => pluginsApi.marketplace({ page: 1, page_size: 100 }),
    enabled: activeTab === 'marketplace',
  });

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
  const reloadMutation = useMutation({
    mutationFn: (name: string) => pluginsApi.reload(name),
    onSuccess: () => {
      toast('success', t('plugins.reload_success'));
      queryClient.invalidateQueries({ queryKey: ['plugins'] });
    },
    onError: (err: Error) => toast('error', err.message),
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
      title: t('common.type'),
      render: (row) => (
        <div className="flex items-center gap-2">
          <Badge variant={typeVariant[row.type || 'gateway'] || 'default'}>
            {row.type || 'gateway'}
          </Badge>
          {row.version && (
            <span className="text-xs text-text-tertiary">
              {t('common.version')}: {row.version}
            </span>
          )}
        </div>
      ),
    },
    {
      key: 'platform',
      title: t('plugins.platform'),
      render: (row) => (
        <div className="flex items-center justify-center gap-2">
          <span>{row.platform}</span>
          {row.is_dev && <Badge variant="warning">{t('plugins.dev_badge')}</Badge>}
        </div>
      ),
    },
    {
      key: 'actions',
      title: t('common.actions'),
      render: (row) => (
        <div className="flex gap-1 justify-center">
          {row.is_dev && (
            <Button
              size="sm"
              variant="ghost"
              icon={<RefreshCw className={`w-3.5 h-3.5 ${reloadMutation.isPending ? 'animate-spin' : ''}`} />}
              onClick={() => reloadMutation.mutate(row.name)}
              disabled={reloadMutation.isPending}
            >
              {t('plugins.reload')}
            </Button>
          )}
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
      <PageHeader
        title={t('plugins.title')}
        description={t('plugins.description')}
        actions={
          <Button
            icon={<Plus className="w-4 h-4" />}
            onClick={() => setInstallOpen(true)}
          >
            {t('plugins.install_plugin')}
          </Button>
        }
      />

      {/* Tab 切换 */}
      <div className="flex gap-1 mb-6 border-b border-border">
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
    </div>
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

  const [installTab, setInstallTab] = useState<'upload' | 'github'>('upload');
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
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
    if (fileInputRef.current) fileInputRef.current.value = '';
  }

  function handleClose() {
    resetForm();
    onClose();
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
              className={`border-2 border-dashed rounded-md p-6 text-center cursor-pointer transition-colors ${
                selectedFile
                  ? 'border-primary bg-primary-subtle'
                  : 'border-glass-border hover:border-border-focus'
              }`}
              onClick={() => fileInputRef.current?.click()}
            >
              <input
                ref={fileInputRef}
                type="file"
                className="hidden"
                onChange={(e) => setSelectedFile(e.target.files?.[0] || null)}
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
                  <Upload className="w-8 h-8 mx-auto mb-2 text-text-tertiary" />
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
}: {
  plugin: MarketplacePluginResp;
}) {
  const { t } = useTranslation();

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
        <div className="pt-3 border-t border-border">
          {plugin.installed ? (
            <Badge variant="success">{t('plugins.already_installed')}</Badge>
          ) : (
            <Button
              size="sm"
              icon={<Download className="w-3.5 h-3.5" />}
              disabled
            >
              {t('common.install')}
            </Button>
          )}
        </div>
      </div>
    </Card>
  );
}
