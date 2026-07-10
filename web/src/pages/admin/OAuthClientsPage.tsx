import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Plus, Pencil, Trash2, RefreshCw, KeyRound } from 'lucide-react';
import { Button, Chip, EmptyState } from '@heroui/react';
import { oauthApi } from '../../shared/api/oauth';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { useClipboard } from '../../shared/hooks/useClipboard';
import { queryKeys } from '../../shared/queryKeys';
import { CommonTable } from '../../shared/components/CommonTable';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { OAuthClientFormModal } from './oauthclients/OAuthClientFormModal';
import { OAuthClientSecretModal } from './oauthclients/OAuthClientSecretModal';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';
import type {
  OAuthClientResp,
  OAuthClientSecretResp,
  UpdateOAuthClientReq,
} from '../../shared/types';

// 应用图标：http(s) 开头按图片渲染，否则按 emoji/文本渲染
function AppIcon({ icon }: { icon: string }) {
  if (!icon) return null;
  if (icon.startsWith('http://') || icon.startsWith('https://')) {
    return <img alt="" className="h-5 w-5 rounded" src={icon} />;
  }
  return <span className="text-base leading-none">{icon}</span>;
}

export default function OAuthClientsPage() {
  const { t } = useTranslation();
  const copy = useClipboard();

  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingItem, setEditingItem] = useState<OAuthClientResp | null>(null);
  const [deletingItem, setDeletingItem] = useState<OAuthClientResp | null>(null);
  const [resettingItem, setResettingItem] = useState<OAuthClientResp | null>(null);
  const [credential, setCredential] = useState<OAuthClientSecretResp | null>(null);

  const { data, isLoading, refetch } = useQuery({
    queryKey: queryKeys.oauthClients(),
    queryFn: () => oauthApi.list(),
  });

  const createMutation = useCrudMutation<OAuthClientSecretResp, UpdateOAuthClientReq>({
    mutationFn: (payload) => oauthApi.create(payload),
    successMessage: t('oauth_clients.create_success'),
    queryKey: queryKeys.oauthClients(),
    onSuccess: (resp) => {
      setShowCreateModal(false);
      setCredential(resp);
    },
  });

  const updateMutation = useCrudMutation<unknown, { id: number; data: UpdateOAuthClientReq }>({
    mutationFn: ({ id, data: payload }) => oauthApi.update(id, payload),
    successMessage: t('oauth_clients.update_success'),
    queryKey: queryKeys.oauthClients(),
    onSuccess: () => setEditingItem(null),
  });

  const deleteMutation = useCrudMutation<unknown, number>({
    mutationFn: (id) => oauthApi.delete(id),
    successMessage: t('oauth_clients.delete_success'),
    queryKey: queryKeys.oauthClients(),
    onSuccess: () => setDeletingItem(null),
  });

  const resetSecretMutation = useCrudMutation<OAuthClientSecretResp, number>({
    mutationFn: (id) => oauthApi.resetSecret(id),
    successMessage: t('oauth_clients.reset_secret_success'),
    queryKey: queryKeys.oauthClients(),
    onSuccess: (resp) => {
      setResettingItem(null);
      setCredential(resp);
    },
  });

  const rows = data ?? [];

  return (
    <div>
      {/* 工具栏 */}
      <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-3 mb-5 flex-wrap">
        <div className="flex items-center gap-2 sm:ml-auto">
          <Button
            isIconOnly
            aria-label={t('common.refresh', 'Refresh')}
            size="sm"
            variant="ghost"
            onPress={() => refetch()}
          >
            <RefreshCw className="w-4 h-4" />
          </Button>
          <Button variant="primary" onPress={() => setShowCreateModal(true)}>
            <Plus className="w-4 h-4" />
            {t('oauth_clients.create')}
          </Button>
        </div>
      </div>

      {/* 表格 */}
      <CommonTable ariaLabel={t('oauth_clients.title', 'OAuth Clients')} minWidth={880}>
        <CommonTable.Header>
          <CommonTable.Column id="name" style={{ width: 220 }}>{t('oauth_clients.col_name')}</CommonTable.Column>
          <CommonTable.Column id="client_id" style={{ width: 240 }}>Client ID</CommonTable.Column>
          <CommonTable.Column id="flags" style={{ width: 200 }}>{t('oauth_clients.col_flags')}</CommonTable.Column>
          <CommonTable.Column id="status" style={{ width: 90 }}>{t('oauth_clients.col_status')}</CommonTable.Column>
          <CommonTable.Column id="actions" style={{ width: 130 }}>{t('common.actions')}</CommonTable.Column>
        </CommonTable.Header>
        <CommonTable.Body>
          {isLoading ? (
            <TableLoadingRow colSpan={5} />
          ) : rows.length === 0 ? (
            <CommonTable.Row id="empty">
              <CommonTable.Cell colSpan={5}>
                <EmptyState>
                  <div className="text-sm text-default-500">{t('oauth_clients.empty_hint')}</div>
                </EmptyState>
              </CommonTable.Cell>
            </CommonTable.Row>
          ) : (
            rows.map((row) => (
              <CommonTable.Row id={String(row.id)} key={row.id}>
                <CommonTable.Cell>
                  <div className="flex items-center gap-2">
                    <AppIcon icon={row.icon} />
                    <div className="min-w-0">
                      <div className="truncate font-medium" style={{ color: 'var(--ag-text)' }} title={row.name}>
                        {row.name}
                      </div>
                      {row.description ? (
                        <div className="truncate text-xs text-text-tertiary" title={row.description}>
                          {row.description}
                        </div>
                      ) : null}
                    </div>
                  </div>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <button
                    className="cursor-pointer rounded bg-surface-secondary px-2 py-0.5 font-mono text-xs"
                    title={t('common.copy')}
                    type="button"
                    onClick={() => copy(row.client_id)}
                  >
                    {row.client_id}
                  </button>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <div className="flex flex-wrap items-center gap-1">
                    {row.first_party ? (
                      <Chip color="accent" size="sm" variant="soft">{t('oauth_clients.flag_first_party')}</Chip>
                    ) : null}
                    {row.show_in_nav ? (
                      <Chip color="default" size="sm" variant="soft">{t('oauth_clients.flag_show_in_nav')}</Chip>
                    ) : null}
                  </div>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <Chip color={row.enabled ? 'success' : 'default'} size="sm" variant="soft">
                    {row.enabled ? t('oauth_clients.status_enabled') : t('oauth_clients.status_disabled')}
                  </Chip>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <div className="ag-table-row-actions flex items-center justify-center gap-0.5">
                    <Button
                      isIconOnly
                      size="sm"
                      variant="secondary"
                      aria-label={t('oauth_clients.reset_secret')}
                      onPress={() => setResettingItem(row)}
                    >
                      <KeyRound className="w-3.5 h-3.5" />
                    </Button>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="secondary"
                      aria-label={t('common.edit')}
                      onPress={() => setEditingItem(row)}
                    >
                      <Pencil className="w-3.5 h-3.5" />
                    </Button>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="danger-soft"
                      className="text-danger"
                      aria-label={t('common.delete')}
                      onPress={() => setDeletingItem(row)}
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </Button>
                  </div>
                </CommonTable.Cell>
              </CommonTable.Row>
            ))
          )}
        </CommonTable.Body>
      </CommonTable>

      {/* 创建弹窗 */}
      <OAuthClientFormModal
        open={showCreateModal}
        title={t('oauth_clients.create')}
        onClose={() => setShowCreateModal(false)}
        onSubmit={(payload) => createMutation.mutate(payload)}
        loading={createMutation.isPending}
      />

      {/* 编辑弹窗 */}
      {editingItem && (
        <OAuthClientFormModal
          open
          title={t('oauth_clients.edit')}
          client={editingItem}
          onClose={() => setEditingItem(null)}
          onSubmit={(payload) => updateMutation.mutate({ id: editingItem.id, data: payload })}
          loading={updateMutation.isPending}
        />
      )}

      {/* 凭证一次性展示 */}
      <OAuthClientSecretModal credential={credential} onClose={() => setCredential(null)} />

      {/* 重置 secret 确认 */}
      <ConfirmDialog
        open={!!resettingItem}
        onOpenChange={(open) => {
          if (!open) setResettingItem(null);
        }}
        title={t('oauth_clients.reset_secret')}
        description={t('oauth_clients.reset_secret_confirm', { name: resettingItem?.name })}
        status="warning"
        confirmVariant="primary"
        loading={resetSecretMutation.isPending}
        onConfirm={() => resettingItem && resetSecretMutation.mutate(resettingItem.id)}
      />

      {/* 删除确认 */}
      <ConfirmDialog
        open={!!deletingItem}
        onOpenChange={(open) => {
          if (!open) setDeletingItem(null);
        }}
        title={t('oauth_clients.delete_title')}
        description={t('oauth_clients.delete_confirm', { name: deletingItem?.name })}
        loading={deleteMutation.isPending}
        onConfirm={() => deletingItem && deleteMutation.mutate(deletingItem.id)}
      />
    </div>
  );
}
