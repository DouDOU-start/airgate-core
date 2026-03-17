import { useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apikeysApi } from '../../shared/api/apikeys';
import { groupsApi } from '../../shared/api/groups';
import { useToast } from '../../shared/components/Toast';
import { PageHeader } from '../../shared/components/PageHeader';
import { Table, type Column } from '../../shared/components/Table';
import { Button } from '../../shared/components/Button';
import { Input, Select } from '../../shared/components/Input';
import { DatePicker } from '../../shared/components/DatePicker';
import { Modal, ConfirmModal } from '../../shared/components/Modal';
import { StatusBadge } from '../../shared/components/Badge';
import {
  Plus,
  Pencil,
  Trash2,
  Key,
  Copy,
  AlertTriangle,
  Eye,
  Ban,
  CheckCircle,
  Terminal,
  Upload,
} from 'lucide-react';
import type { APIKeyResp, CreateAPIKeyReq, UpdateAPIKeyReq, GroupResp } from '../../shared/types';

interface KeyForm {
  name: string;
  group_id: string;
  quota_usd: string;
  expires_at: string;
}

const emptyForm: KeyForm = {
  name: '',
  group_id: '',
  quota_usd: '',
  expires_at: '',
};

export default function UserKeysPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const [page, setPage] = useState(1);
  const [modalOpen, setModalOpen] = useState(false);
  const [editingKey, setEditingKey] = useState<APIKeyResp | null>(null);
  const [form, setForm] = useState<KeyForm>(emptyForm);
  const [deleteTarget, setDeleteTarget] = useState<APIKeyResp | null>(null);

  // 显示新创建密钥的弹窗
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [revealedKey, setRevealedKey] = useState<string | null>(null);

  // 使用配置弹窗
  const [useKeyTarget, setUseKeyTarget] = useState<APIKeyResp | null>(null);
  const [useKeyValue, setUseKeyValue] = useState<string | null>(null);
  const [useKeyTab, setUseKeyTab] = useState<'claude' | 'codex'>('claude');
  const [useKeyShell, setUseKeyShell] = useState<'unix' | 'cmd' | 'powershell'>('unix');

  // CCS 导入弹窗
  const [ccsTarget, setCcsTarget] = useState<APIKeyResp | null>(null);
  const [ccsKeyValue, setCcsKeyValue] = useState<string | null>(null);

  // 密钥列表
  const { data, isLoading } = useQuery({
    queryKey: ['user-keys', page],
    queryFn: () => apikeysApi.list({ page, page_size: 20 }),
  });

  // 分组列表（用于选择）
  const { data: groupsData } = useQuery({
    queryKey: ['groups-for-keys'],
    queryFn: () => groupsApi.list({ page: 1, page_size: 100 }),
  });

  // 创建密钥
  const createMutation = useMutation({
    mutationFn: (data: CreateAPIKeyReq) => apikeysApi.create(data),
    onSuccess: (result) => {
      toast('success', t('user_keys.create_success'));
      queryClient.invalidateQueries({ queryKey: ['user-keys'] });
      closeModal();
      // 显示完整密钥
      if (result.key) {
        setCreatedKey(result.key);
      }
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 更新密钥
  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: UpdateAPIKeyReq }) =>
      apikeysApi.update(id, data),
    onSuccess: () => {
      toast('success', t('user_keys.update_success'));
      queryClient.invalidateQueries({ queryKey: ['user-keys'] });
      closeModal();
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 删除密钥
  const deleteMutation = useMutation({
    mutationFn: (id: number) => apikeysApi.delete(id),
    onSuccess: () => {
      toast('success', t('user_keys.delete_success'));
      queryClient.invalidateQueries({ queryKey: ['user-keys'] });
      setDeleteTarget(null);
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 查看密钥
  const revealMutation = useMutation({
    mutationFn: (id: number) => apikeysApi.reveal(id),
    onSuccess: (resp) => {
      if (resp.key) {
        setRevealedKey(resp.key);
      }
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 禁用/启用密钥
  const toggleStatusMutation = useMutation({
    mutationFn: ({ id, status }: { id: number; status: 'active' | 'disabled' }) =>
      apikeysApi.update(id, { status }),
    onSuccess: (_resp, variables) => {
      toast(
        'success',
        variables.status === 'active'
          ? t('user_keys.enable_success')
          : t('user_keys.disable_success'),
      );
      queryClient.invalidateQueries({ queryKey: ['user-keys'] });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  function openCreate() {
    setEditingKey(null);
    setForm(emptyForm);
    setModalOpen(true);
  }

  function openEdit(key: APIKeyResp) {
    setEditingKey(key);
    setForm({
      name: key.name,
      group_id: String(key.group_id),
      quota_usd: key.quota_usd ? String(key.quota_usd) : '',
      expires_at: key.expires_at ? key.expires_at.slice(0, 10) : '',
    });
    setModalOpen(true);
  }

  function closeModal() {
    setModalOpen(false);
    setEditingKey(null);
    setForm(emptyForm);
  }

  function handleSubmit() {
    if (!form.name) {
      toast('error', t('user_keys.name_placeholder'));
      return;
    }
    if (!editingKey && !form.group_id) {
      toast('error', t('user_keys.select_group'));
      return;
    }

    // 后端要求 RFC3339 格式
    const expiresAt = form.expires_at ? `${form.expires_at}T23:59:59Z` : undefined;

    if (editingKey) {
      const payload: UpdateAPIKeyReq = {
        name: form.name,
        group_id: form.group_id ? Number(form.group_id) : undefined,
        quota_usd: form.quota_usd ? Number(form.quota_usd) : undefined,
        expires_at: expiresAt,
      };
      updateMutation.mutate({ id: editingKey.id, data: payload });
    } else {
      const payload: CreateAPIKeyReq = {
        name: form.name,
        group_id: Number(form.group_id),
        quota_usd: form.quota_usd ? Number(form.quota_usd) : undefined,
        expires_at: expiresAt,
      };
      createMutation.mutate(payload);
    }
  }

  // 查找分组
  const groupList = groupsData?.list ?? [];
  const groupMap = new Map<number, GroupResp>(groupList.map((g) => [g.id, g]));
  const getGroupPlatform = (groupId: number) => groupMap.get(groupId)?.platform || '';

  // 分组选项
  const groupOptions = [
    { value: '', label: t('user_keys.select_group') },
    ...groupList.map((g) => ({
      value: String(g.id),
      label: `${g.name} (${g.platform})`,
    })),
  ];

  // ========== 使用配置相关 ==========
  const openUseKeyModal = useCallback(
    async (row: APIKeyResp) => {
      setUseKeyTarget(row);
      setUseKeyTab('claude');
      setUseKeyShell('unix');
      try {
        const resp = await apikeysApi.reveal(row.id);
        setUseKeyValue(resp.key || null);
      } catch {
        toast('error', t('user_keys.reveal_failed'));
        setUseKeyTarget(null);
      }
    },
    [toast, t],
  );

  const baseUrl = window.location.origin;

  function getUseKeyConfig(
    platform: string,
    tab: 'claude' | 'codex',
    shell: 'unix' | 'cmd' | 'powershell',
    apiKey: string,
  ): { files: Array<{ path: string; content: string; hint?: string }> } {
    // OpenAI 平台同时支持 Claude Code（通过 /v1/messages 适配）和 Codex CLI
    if (platform === 'openai') {
      if (tab === 'claude') {
        // Claude Code 配置 — 通过 OpenAI 插件的 Anthropic 协议适配
        if (shell === 'unix') {
          return {
            files: [
              {
                path: '~/.bashrc 或 ~/.zshrc',
                content: `export ANTHROPIC_BASE_URL="${baseUrl}"\nexport ANTHROPIC_API_KEY="${apiKey}"`,
              },
            ],
          };
        } else if (shell === 'cmd') {
          return {
            files: [
              {
                path: 'CMD',
                content: `set ANTHROPIC_BASE_URL=${baseUrl}\nset ANTHROPIC_API_KEY=${apiKey}`,
              },
            ],
          };
        } else {
          return {
            files: [
              {
                path: 'PowerShell',
                content: `$env:ANTHROPIC_BASE_URL="${baseUrl}"\n$env:ANTHROPIC_API_KEY="${apiKey}"`,
              },
            ],
          };
        }
      } else {
        // Codex CLI 配置
        if (shell === 'unix') {
          return {
            files: [
              {
                path: '~/.codex/config.toml',
                content: `model = "o4-mini"\n\n[api]\napi_key_env = "OPENAI_API_KEY"\nbase_url = "${baseUrl}/v1"`,
              },
              {
                path: '~/.bashrc 或 ~/.zshrc',
                content: `export OPENAI_API_KEY="${apiKey}"`,
              },
            ],
          };
        } else if (shell === 'cmd') {
          return {
            files: [
              {
                path: '%USERPROFILE%\\.codex\\config.toml',
                content: `model = "o4-mini"\n\n[api]\napi_key_env = "OPENAI_API_KEY"\nbase_url = "${baseUrl}/v1"`,
              },
              {
                path: 'CMD',
                content: `set OPENAI_API_KEY=${apiKey}`,
              },
            ],
          };
        } else {
          return {
            files: [
              {
                path: '$HOME\\.codex\\config.toml',
                content: `model = "o4-mini"\n\n[api]\napi_key_env = "OPENAI_API_KEY"\nbase_url = "${baseUrl}/v1"`,
              },
              {
                path: 'PowerShell',
                content: `$env:OPENAI_API_KEY="${apiKey}"`,
              },
            ],
          };
        }
      }
    }

    // 默认/其他平台 — 使用 Claude 标准配置
    if (shell === 'unix') {
      return {
        files: [
          {
            path: '~/.bashrc 或 ~/.zshrc',
            content: `export ANTHROPIC_BASE_URL="${baseUrl}"\nexport ANTHROPIC_API_KEY="${apiKey}"`,
          },
        ],
      };
    } else if (shell === 'cmd') {
      return {
        files: [
          {
            path: 'CMD',
            content: `set ANTHROPIC_BASE_URL=${baseUrl}\nset ANTHROPIC_API_KEY=${apiKey}`,
          },
        ],
      };
    } else {
      return {
        files: [
          {
            path: 'PowerShell',
            content: `$env:ANTHROPIC_BASE_URL="${baseUrl}"\n$env:ANTHROPIC_API_KEY="${apiKey}"`,
          },
        ],
      };
    }
  }

  // ========== CCS 导入相关 ==========
  const openCcsModal = useCallback(
    async (row: APIKeyResp) => {
      setCcsTarget(row);
      try {
        const resp = await apikeysApi.reveal(row.id);
        setCcsKeyValue(resp.key || null);
      } catch {
        toast('error', t('user_keys.reveal_failed'));
        setCcsTarget(null);
      }
    },
    [toast, t],
  );

  function executeCcsImport(
    apiKey: string,
    clientType: 'claude' | 'codex',
    platform: string,
  ) {
    let app: string;
    let endpoint: string;

    if (platform === 'openai') {
      if (clientType === 'claude') {
        app = 'claude';
        endpoint = baseUrl;
      } else {
        app = 'codex';
        endpoint = baseUrl;
      }
    } else {
      app = 'claude';
      endpoint = baseUrl;
    }

    const usageScript = `({
    request: {
      url: "{{baseUrl}}/v1/usage",
      method: "GET",
      headers: { "Authorization": "Bearer {{apiKey}}" }
    },
    extractor: function(response) {
      const remaining = response?.remaining ?? response?.quota?.remaining ?? response?.balance;
      const unit = response?.unit ?? response?.quota?.unit ?? "USD";
      return {
        isValid: response?.is_active ?? response?.isValid ?? true,
        remaining,
        unit
      };
    }
  })`;

    const siteName = document.title || 'AirGate';
    const params = new URLSearchParams({
      resource: 'provider',
      app,
      name: siteName,
      homepage: baseUrl,
      endpoint,
      apiKey,
      configFormat: 'json',
      usageEnabled: 'true',
      usageScript: btoa(usageScript),
      usageAutoInterval: '30',
    });

    const deeplink = `ccswitch://v1/import?${params.toString()}`;

    try {
      window.open(deeplink, '_self');
      setTimeout(() => {
        if (document.hasFocus()) {
          toast('error', t('user_keys.ccs_not_installed'));
        }
      }, 100);
    } catch {
      toast('error', t('user_keys.ccs_not_installed'));
    }
  }

  const columns: Column<APIKeyResp>[] = [
    { key: 'name', title: t('common.name') },
    {
      key: 'key_prefix',
      title: t('user_keys.title'),
      render: (row) => (
        <span
          className="inline-flex items-center gap-1.5 text-xs px-2 py-0.5 rounded-sm border border-glass-border bg-surface text-text-secondary font-mono"
        >
          <Key className="w-3 h-3 text-text-tertiary" />
          {row.key_prefix}...
        </span>
      ),
    },
    {
      key: 'group_id',
      title: t('user_keys.group'),
      render: (row) => groupMap.get(row.group_id)?.name || `#${row.group_id}`,
    },
    {
      key: 'quota',
      title: t('user_keys.quota_label'),
      render: (row) => (
        <span className="font-mono">
          {row.quota_usd > 0 ? (
            <>
              ${row.used_quota.toFixed(4)} / ${row.quota_usd.toFixed(4)}
            </>
          ) : (
            <span className="text-text-tertiary">{t('user_keys.quota_unlimited_hint')}</span>
          )}
        </span>
      ),
    },
    {
      key: 'expires_at',
      title: t('user_keys.expires_at'),
      render: (row) =>
        row.expires_at
          ? new Date(row.expires_at).toLocaleDateString('zh-CN')
          : t('user_keys.never_expire'),
    },
    {
      key: 'status',
      title: t('common.status'),
      render: (row) => {
        // 前端判断：过期时间已过则显示为 expired
        const isExpired = row.expires_at && new Date(row.expires_at) < new Date();
        const displayStatus = isExpired ? 'expired' : row.status;
        return <StatusBadge status={displayStatus} />;
      },
    },
    {
      key: 'actions',
      title: t('common.actions'),
      render: (row) => (
        <div className="flex gap-1 justify-center flex-wrap">
          <Button
            size="sm"
            variant="ghost"
            onClick={() => revealMutation.mutate(row.id)}
            icon={<Eye className="w-3.5 h-3.5" />}
            loading={revealMutation.isPending}
          >
            {t('api_keys.reveal')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => openUseKeyModal(row)}
            icon={<Terminal className="w-3.5 h-3.5" />}
          >
            {t('user_keys.use_key')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => openCcsModal(row)}
            icon={<Upload className="w-3.5 h-3.5" />}
          >
            {t('user_keys.import_ccs')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() =>
              toggleStatusMutation.mutate({
                id: row.id,
                status: row.status === 'active' ? 'disabled' : 'active',
              })
            }
            icon={
              row.status === 'active' ? (
                <Ban className="w-3.5 h-3.5" />
              ) : (
                <CheckCircle className="w-3.5 h-3.5" />
              )
            }
            className={
              row.status === 'active'
                ? 'text-warning hover:text-warning'
                : 'text-success hover:text-success'
            }
            loading={toggleStatusMutation.isPending}
          >
            {row.status === 'active' ? t('user_keys.disable') : t('user_keys.enable')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => openEdit(row)}
            icon={<Pencil className="w-3.5 h-3.5" />}
          >
            {t('common.edit')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => setDeleteTarget(row)}
            icon={<Trash2 className="w-3.5 h-3.5" />}
            className="text-danger hover:text-danger"
          >
            {t('common.delete')}
          </Button>
        </div>
      ),
    },
  ];

  const saving = createMutation.isPending || updateMutation.isPending;
  const useKeyPlatform = useKeyTarget ? getGroupPlatform(useKeyTarget.group_id) : '';
  const ccsPlatform = ccsTarget ? getGroupPlatform(ccsTarget.group_id) : '';
  // OpenAI 平台同时支持 Claude Code 和 Codex CLI
  const showClientTabs = useKeyPlatform === 'openai';

  return (
    <div className="p-6">
      <PageHeader
        title={t('user_keys.title')}
        actions={
          <Button onClick={openCreate} icon={<Plus className="w-4 h-4" />}>
            {t('user_keys.create')}
          </Button>
        }
      />

      <Table
        columns={columns}
        data={data?.list ?? []}
        loading={isLoading}
        rowKey={(row) => row.id as number}
        page={page}
        pageSize={20}
        total={data?.total ?? 0}
        onPageChange={setPage}
      />

      {/* 创建/编辑弹窗 */}
      <Modal
        open={modalOpen}
        onClose={closeModal}
        title={editingKey ? t('user_keys.edit') : t('user_keys.create')}
        footer={
          <>
            <Button variant="secondary" onClick={closeModal}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleSubmit} loading={saving}>
              {editingKey ? t('common.save') : t('common.create')}
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <Input
            label={t('common.name')}
            required
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            placeholder={t('user_keys.name_placeholder')}
          />
          <Select
            label={t('user_keys.group')}
            required
            value={form.group_id}
            onChange={(e) => setForm({ ...form, group_id: e.target.value })}
            options={groupOptions}
          />
          <Input
            label={t('user_keys.quota_label')}
            type="number"
            value={form.quota_usd}
            onChange={(e) => setForm({ ...form, quota_usd: e.target.value })}
            placeholder={t('user_keys.quota_unlimited_hint')}
            hint={t('user_keys.quota_hint')}
          />
          <DatePicker
            label={t('user_keys.expires_at')}
            value={form.expires_at}
            onChange={(v) => setForm({ ...form, expires_at: v })}
            hint={t('user_keys.expire_hint')}
          />
        </div>
      </Modal>

      {/* 新建密钥后显示完整密钥 */}
      <Modal
        open={!!createdKey}
        onClose={() => setCreatedKey(null)}
        title={t('user_keys.create_success')}
        footer={
          <Button onClick={() => setCreatedKey(null)}>{t('user_keys.key_created_warning')}</Button>
        }
      >
        <div className="space-y-4">
          <div className="flex items-start gap-2.5 rounded-md bg-danger-subtle border border-danger border-opacity-20 px-4 py-3">
            <AlertTriangle className="w-4 h-4 text-danger mt-0.5 shrink-0" />
            <p className="text-sm text-danger font-medium">
              {t('user_keys.key_created_warning')}
            </p>
          </div>
          <div
            className="border border-glass-border bg-bg-elevated shadow-sm rounded-lg p-3 break-all text-sm text-text font-mono"
          >
            {createdKey}
          </div>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              navigator.clipboard.writeText(createdKey || '');
              toast('success', t('user_keys.copy_key'));
            }}
            icon={<Copy className="w-3.5 h-3.5" />}
          >
            {t('user_keys.copy_key')}
          </Button>
        </div>
      </Modal>

      {/* 查看密钥弹窗 */}
      <Modal
        open={!!revealedKey}
        onClose={() => setRevealedKey(null)}
        title={t('api_keys.reveal')}
        footer={
          <Button onClick={() => setRevealedKey(null)}>{t('common.close')}</Button>
        }
      >
        <div className="space-y-4">
          <div
            className="rounded-md border border-glass-border bg-surface p-3 break-all text-sm text-text font-mono"
          >
            {revealedKey}
          </div>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              navigator.clipboard.writeText(revealedKey || '');
              toast('success', t('user_keys.copy_key'));
            }}
            icon={<Copy className="w-3.5 h-3.5" />}
          >
            {t('user_keys.copy_key')}
          </Button>
        </div>
      </Modal>

      {/* 使用 API 密钥配置弹窗 */}
      <Modal
        open={!!useKeyTarget}
        onClose={() => {
          setUseKeyTarget(null);
          setUseKeyValue(null);
        }}
        title={t('user_keys.use_key_title')}
        width="560px"
        footer={
          <Button
            onClick={() => {
              setUseKeyTarget(null);
              setUseKeyValue(null);
            }}
          >
            {t('common.close')}
          </Button>
        }
      >
        {useKeyValue ? (
          <div className="space-y-4">
            <p className="text-sm text-text-secondary">
              {t('user_keys.use_key_desc')}
            </p>

            {/* 客户端选择 Tab（OpenAI 平台时显示） */}
            {showClientTabs && (
              <div className="flex gap-1 p-0.5 rounded-md bg-bg-hover">
                <button
                  onClick={() => setUseKeyTab('claude')}
                  className={`flex-1 px-3 py-1.5 text-xs font-medium rounded transition-colors ${
                    useKeyTab === 'claude'
                      ? 'bg-bg-elevated text-text shadow-sm'
                      : 'text-text-tertiary hover:text-text-secondary'
                  }`}
                >
                  Claude Code
                </button>
                <button
                  onClick={() => setUseKeyTab('codex')}
                  className={`flex-1 px-3 py-1.5 text-xs font-medium rounded transition-colors ${
                    useKeyTab === 'codex'
                      ? 'bg-bg-elevated text-text shadow-sm'
                      : 'text-text-tertiary hover:text-text-secondary'
                  }`}
                >
                  Codex CLI
                </button>
              </div>
            )}

            {/* OS/Shell Tab */}
            <div className="flex gap-1 p-0.5 rounded-md bg-bg-hover">
              <button
                onClick={() => setUseKeyShell('unix')}
                className={`flex-1 px-3 py-1.5 text-xs font-medium rounded transition-colors ${
                  useKeyShell === 'unix'
                    ? 'bg-bg-elevated text-text shadow-sm'
                    : 'text-text-tertiary hover:text-text-secondary'
                }`}
              >
                macOS / Linux
              </button>
              <button
                onClick={() => setUseKeyShell('cmd')}
                className={`flex-1 px-3 py-1.5 text-xs font-medium rounded transition-colors ${
                  useKeyShell === 'cmd'
                    ? 'bg-bg-elevated text-text shadow-sm'
                    : 'text-text-tertiary hover:text-text-secondary'
                }`}
              >
                Windows CMD
              </button>
              <button
                onClick={() => setUseKeyShell('powershell')}
                className={`flex-1 px-3 py-1.5 text-xs font-medium rounded transition-colors ${
                  useKeyShell === 'powershell'
                    ? 'bg-bg-elevated text-text shadow-sm'
                    : 'text-text-tertiary hover:text-text-secondary'
                }`}
              >
                PowerShell
              </button>
            </div>

            {/* 配置代码块 */}
            {getUseKeyConfig(useKeyPlatform, useKeyTab, useKeyShell, useKeyValue).files.map(
              (file, idx) => (
                <div key={idx}>
                  {file.hint && (
                    <p className="text-xs text-warning mb-1.5 flex items-center gap-1">
                      <AlertTriangle className="w-3 h-3 shrink-0" />
                      {file.hint}
                    </p>
                  )}
                  <div className="rounded-md overflow-hidden border border-glass-border">
                    <div className="flex items-center justify-between px-3 py-1.5 bg-bg-hover border-b border-glass-border">
                      <span className="text-xs text-text-tertiary font-mono">{file.path}</span>
                      <button
                        onClick={() => {
                          navigator.clipboard.writeText(file.content);
                          toast('success', t('user_keys.copied'));
                        }}
                        className="flex items-center gap-1 px-2 py-0.5 text-xs rounded hover:bg-bg-elevated text-text-secondary transition-colors"
                      >
                        <Copy className="w-3 h-3" />
                        {t('user_keys.copy')}
                      </button>
                    </div>
                    <pre className="p-3 text-sm font-mono text-text bg-surface overflow-x-auto whitespace-pre-wrap">
                      {file.content}
                    </pre>
                  </div>
                </div>
              ),
            )}
          </div>
        ) : (
          <div className="flex items-center justify-center py-8 text-text-tertiary text-sm">
            {t('common.loading')}
          </div>
        )}
      </Modal>

      {/* CCS 导入弹窗 — 选择客户端 */}
      <Modal
        open={!!ccsTarget}
        onClose={() => {
          setCcsTarget(null);
          setCcsKeyValue(null);
        }}
        title={t('user_keys.ccs_select_client')}
        footer={
          <Button
            variant="secondary"
            onClick={() => {
              setCcsTarget(null);
              setCcsKeyValue(null);
            }}
          >
            {t('common.cancel')}
          </Button>
        }
      >
        {ccsKeyValue ? (
          <div className="space-y-3">
            <p className="text-sm text-text-secondary">
              {t('user_keys.ccs_select_desc')}
            </p>
            <div className="grid grid-cols-2 gap-3">
              {/* 始终显示 Claude Code */}
              <button
                onClick={() => {
                  executeCcsImport(ccsKeyValue, 'claude', ccsPlatform);
                  setCcsTarget(null);
                  setCcsKeyValue(null);
                }}
                className="flex flex-col items-center gap-2 p-4 rounded-lg border border-glass-border bg-surface hover:bg-bg-hover hover:border-text-tertiary transition-colors"
              >
                <div className="w-10 h-10 rounded-lg bg-info-subtle flex items-center justify-center">
                  <Terminal className="w-5 h-5 text-info" />
                </div>
                <span className="text-sm font-medium text-text">Claude Code</span>
                <span className="text-xs text-text-tertiary text-center">
                  {t('user_keys.ccs_claude_desc')}
                </span>
              </button>

              {/* OpenAI 平台额外显示 Codex CLI */}
              {ccsPlatform === 'openai' && (
                <button
                  onClick={() => {
                    executeCcsImport(ccsKeyValue, 'codex', ccsPlatform);
                    setCcsTarget(null);
                    setCcsKeyValue(null);
                  }}
                  className="flex flex-col items-center gap-2 p-4 rounded-lg border border-glass-border bg-surface hover:bg-bg-hover hover:border-text-tertiary transition-colors"
                >
                  <div className="w-10 h-10 rounded-lg bg-success-subtle flex items-center justify-center">
                    <Terminal className="w-5 h-5 text-success" />
                  </div>
                  <span className="text-sm font-medium text-text">Codex CLI</span>
                  <span className="text-xs text-text-tertiary text-center">
                    {t('user_keys.ccs_codex_desc')}
                  </span>
                </button>
              )}
            </div>
          </div>
        ) : (
          <div className="flex items-center justify-center py-8 text-text-tertiary text-sm">
            {t('common.loading')}
          </div>
        )}
      </Modal>

      {/* 删除确认 */}
      <ConfirmModal
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        title={t('user_keys.delete_key')}
        message={t('user_keys.delete_confirm', { name: deleteTarget?.name })}
        loading={deleteMutation.isPending}
        danger
      />
    </div>
  );
}
