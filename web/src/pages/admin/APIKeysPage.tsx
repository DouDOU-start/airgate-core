import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { AlertTriangle, Copy, Plus, Pencil, Trash2, Key, Layers, Eye, RefreshCw } from 'lucide-react';
import { Alert, AlertDialog, Button, EmptyState, Modal, Spinner, Table as HeroTable, useOverlayState } from '@heroui/react';
import {
  StatusChip,
} from '../../shared/ui';
import { apikeysApi } from '../../shared/api/apikeys';
import { groupsApi } from '../../shared/api/groups';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE, FETCH_ALL_PARAMS } from '../../shared/constants';
import { formatExpiry } from '../../shared/utils/format';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { useClipboard } from '../../shared/hooks/useClipboard';
import { CreateKeyModal } from './apikeys/CreateKeyModal';
import { EditKeyModal } from './apikeys/EditKeyModal';
import type { APIKeyResp, GroupResp } from '../../shared/types';

export default function APIKeysPage() {
  const { t } = useTranslation();
  const copy = useClipboard();

  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingKey, setEditingKey] = useState<APIKeyResp | null>(null);
  const [deletingKey, setDeletingKey] = useState<APIKeyResp | null>(null);
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [revealedKey, setRevealedKey] = useState<string | null>(null);

  const { data, isLoading, refetch } = useQuery({
    queryKey: queryKeys.apikeys(page, pageSize),
    queryFn: () => apikeysApi.list({ page, page_size: pageSize }),
    placeholderData: keepPreviousData,
  });

  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
  });

  const createMutation = useCrudMutation({
    mutationFn: apikeysApi.create,
    successMessage: t('api_keys.create_success'),
    queryKey: queryKeys.apikeys(),
    onSuccess: (resp) => {
      setShowCreateModal(false);
      if (resp.key) setCreatedKey(resp.key);
    },
  });

  const updateMutation = useCrudMutation({
    mutationFn: ({ id, data }: { id: number; data: Parameters<typeof apikeysApi.adminUpdate>[1] }) =>
      apikeysApi.adminUpdate(id, data),
    successMessage: t('api_keys.update_success'),
    queryKey: queryKeys.apikeys(),
    onSuccess: () => setEditingKey(null),
  });

  const deleteMutation = useCrudMutation({
    mutationFn: apikeysApi.delete,
    successMessage: t('api_keys.delete_success'),
    queryKey: queryKeys.apikeys(),
    onSuccess: () => setDeletingKey(null),
  });

  const revealMutation = useCrudMutation({
    mutationFn: apikeysApi.reveal,
    queryKey: queryKeys.apikeys(),
    onSuccess: (resp) => {
      if (resp.key) setRevealedKey(resp.key);
    },
  });

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);
  const createdKeyModalState = useOverlayState({
    isOpen: !!createdKey,
    onOpenChange: (open) => {
      if (!open) setCreatedKey(null);
    },
  });
  const revealedKeyModalState = useOverlayState({
    isOpen: !!revealedKey,
    onOpenChange: (open) => {
      if (!open) setRevealedKey(null);
    },
  });

  return (
    <div>
      <div className="flex justify-end mb-5">
        <div className="flex items-center gap-2 ml-auto">
          <Button
            isIconOnly
            aria-label={t('common.refresh', 'Refresh')}
            size="md"
            variant="ghost"
            onPress={() => refetch()}
          >
            <RefreshCw className="w-4 h-4" />
          </Button>
          <Button variant="primary" onPress={() => setShowCreateModal(true)}>
            <Plus className="w-4 h-4" />
            {t('api_keys.create')}
          </Button>
        </div>
      </div>

      <HeroTable variant="primary">
        <HeroTable.ScrollContainer>
          <HeroTable.Content aria-label={t('api_keys.title', 'API keys')}>
            <HeroTable.Header>
              <HeroTable.Column id="id" style={{ width: 72 }}>
                {t('common.id')}
              </HeroTable.Column>
              <HeroTable.Column id="name">{t('common.name')}</HeroTable.Column>
              <HeroTable.Column id="key_prefix">{t('api_keys.key_prefix')}</HeroTable.Column>
              <HeroTable.Column id="group_id">{t('api_keys.group')}</HeroTable.Column>
              <HeroTable.Column id="quota">{t('api_keys.quota_used')}</HeroTable.Column>
              <HeroTable.Column id="usage">{t('api_keys.usage')}</HeroTable.Column>
              <HeroTable.Column id="expires_at">{t('api_keys.expire_time')}</HeroTable.Column>
              <HeroTable.Column id="status">{t('common.status')}</HeroTable.Column>
              <HeroTable.Column id="actions">{t('common.actions')}</HeroTable.Column>
            </HeroTable.Header>
            <HeroTable.Body>
              {isLoading ? (
                <TableLoadingRow colSpan={9} />
              ) : rows.length === 0 ? (
                <HeroTable.Row id="empty">
                  <HeroTable.Cell colSpan={9}>
                    <EmptyState />
                  </HeroTable.Cell>
                </HeroTable.Row>
              ) : (
                rows.map((row: APIKeyResp) => {
                  const group = row.group_id == null
                    ? null
                    : groupsData?.list?.find((g: GroupResp) => g.id === row.group_id);

                  return (
                    <HeroTable.Row id={String(row.id)} key={row.id}>
                      <HeroTable.Cell>
                        <span className="font-mono">{row.id}</span>
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        <span className="inline-flex items-center gap-1.5">
                          <Key className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
                          <span style={{ color: 'var(--ag-text)' }} className="font-medium">{row.name}</span>
                        </span>
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        <code
                          className="text-xs px-2 py-0.5 rounded"
                          style={{
                            fontFamily: 'var(--ag-font-mono)',
                            background: 'var(--ag-bg-surface)',
                            color: 'var(--ag-text-secondary)',
                            border: '1px solid var(--ag-border-subtle)',
                          }}
                        >
                          {row.key_prefix}...
                        </code>
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        <span className="inline-flex items-center gap-1.5">
                          <Layers className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
                          {row.group_id == null ? t('api_keys.group_unbound') : group ? group.name : `#${row.group_id}`}
                        </span>
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        <span className="font-mono">
                          <span style={{ color: 'var(--ag-primary)' }}>${row.used_quota.toFixed(2)}</span>
                          <span style={{ color: 'var(--ag-text-tertiary)' }}> / </span>
                          <span>{row.quota_usd > 0 ? `$${row.quota_usd.toFixed(2)}` : t('common.unlimited')}</span>
                        </span>
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        <div className="font-mono text-xs space-y-0.5">
                          <div>
                            <span style={{ color: 'var(--ag-text-tertiary)' }}>{t('api_keys.today')}: </span>
                            <span style={{ color: 'var(--ag-primary)' }}>${row.today_cost.toFixed(4)}</span>
                          </div>
                          <div>
                            <span style={{ color: 'var(--ag-text-tertiary)' }}>{t('api_keys.thirty_days')}: </span>
                            <span style={{ color: 'var(--ag-text)' }}>${row.thirty_day_cost.toFixed(4)}</span>
                          </div>
                        </div>
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        <span className="font-mono">{formatExpiry(row.expires_at)}</span>
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        <StatusChip status={row.status} />
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        <div className="flex gap-1">
                          <Button
                            size="sm"
                            variant="ghost"
                            isDisabled={revealMutation.isPending}
                            onPress={() => revealMutation.mutate(row.id)}
                          >
                            {revealMutation.isPending ? <Spinner size="sm" /> : <Eye className="w-3.5 h-3.5" />}
                            {t('api_keys.reveal')}
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            onPress={() => setEditingKey(row)}
                          >
                            <Pencil className="w-3.5 h-3.5" />
                            {t('common.edit')}
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            style={{ color: 'var(--ag-danger)' }}
                            onPress={() => setDeletingKey(row)}
                          >
                            <Trash2 className="w-3.5 h-3.5" />
                            {t('common.delete')}
                          </Button>
                        </div>
                      </HeroTable.Cell>
                    </HeroTable.Row>
                  );
                })
              )}
            </HeroTable.Body>
          </HeroTable.Content>
        </HeroTable.ScrollContainer>
        <HeroTable.Footer>
          <TablePaginationFooter
            page={page}
            pageSize={pageSize}
            setPage={setPage}
            setPageSize={setPageSize}
            total={total}
            totalPages={totalPages}
          />
        </HeroTable.Footer>
      </HeroTable>

      <CreateKeyModal
        open={showCreateModal}
        groups={groupsData?.list ?? []}
        onClose={() => setShowCreateModal(false)}
        onSubmit={(data) => createMutation.mutate(data)}
        loading={createMutation.isPending}
      />

      <Modal state={createdKeyModalState}>
        <Modal.Backdrop>
          <Modal.Container placement="center" scroll="inside" size="md">
            <Modal.Dialog className="ag-elevation-modal">
              <Modal.Header>
                <Modal.Heading>{t('api_keys.key_created')}</Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body>
                <div className="space-y-4">
                  <Alert status="warning">
                    <Alert.Indicator>
                      <AlertTriangle className="h-4 w-4" />
                    </Alert.Indicator>
                    <Alert.Content>
                      <Alert.Description>{t('api_keys.key_created_warning')}</Alert.Description>
                    </Alert.Content>
                  </Alert>
                  <div className="flex items-center gap-2">
                    <code className="flex-1 break-all rounded-md border border-glass-border bg-surface px-3 py-2 font-mono text-sm text-text">
                      {createdKey ?? ''}
                    </code>
                    <Button size="sm" variant="secondary" onPress={() => copy(createdKey ?? '')}>
                      <Copy className="h-3.5 w-3.5" />
                      {t('common.copy')}
                    </Button>
                  </div>
                </div>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="primary" onPress={() => setCreatedKey(null)}>
                  {t('api_keys.key_saved_close')}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      <Modal state={revealedKeyModalState}>
        <Modal.Backdrop>
          <Modal.Container placement="center" scroll="inside" size="md">
            <Modal.Dialog className="ag-elevation-modal">
              <Modal.Header>
                <Modal.Heading>{t('api_keys.reveal')}</Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body>
                <div className="space-y-4">
                  <Alert status="warning">
                    <Alert.Indicator>
                      <AlertTriangle className="h-4 w-4" />
                    </Alert.Indicator>
                    <Alert.Content>
                      <Alert.Description>{t('api_keys.key_reveal_warning')}</Alert.Description>
                    </Alert.Content>
                  </Alert>
                  <div className="flex items-center gap-2">
                    <code className="flex-1 break-all rounded-md border border-glass-border bg-surface px-3 py-2 font-mono text-sm text-text">
                      {revealedKey ?? ''}
                    </code>
                    <Button size="sm" variant="secondary" onPress={() => copy(revealedKey ?? '')}>
                      <Copy className="h-3.5 w-3.5" />
                      {t('common.copy')}
                    </Button>
                  </div>
                </div>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="primary" onPress={() => setRevealedKey(null)}>
                  {t('common.close')}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      {editingKey && (
        <EditKeyModal
          open
          apiKey={editingKey}
          groups={groupsData?.list ?? []}
          onClose={() => setEditingKey(null)}
          onSubmit={(data) => updateMutation.mutate({ id: editingKey.id, data })}
          loading={updateMutation.isPending}
        />
      )}

      <AlertDialog
        isOpen={!!deletingKey}
        onOpenChange={(open) => {
          if (!open) setDeletingKey(null);
        }}
      >
        <AlertDialog.Backdrop>
          <AlertDialog.Container placement="center" size="sm">
            <AlertDialog.Dialog className="ag-elevation-modal">
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t('api_keys.delete_key')}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>{t('api_keys.delete_key_confirm', { name: deletingKey?.name })}</AlertDialog.Body>
              <AlertDialog.Footer>
                <Button variant="secondary" onPress={() => setDeletingKey(null)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  aria-busy={deleteMutation.isPending}
                  isDisabled={deleteMutation.isPending}
                  variant="danger"
                  onPress={() => deletingKey && deleteMutation.mutate(deletingKey.id)}
                >
                  {deleteMutation.isPending ? <Spinner size="sm" /> : null}
                  {t('common.confirm')}
                </Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>
    </div>
  );
}
