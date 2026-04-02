import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Modal } from '../../../shared/components/Modal';
import { Table, type Column } from '../../../shared/components/Table';
import { StatusBadge } from '../../../shared/components/Badge';
import { usersApi } from '../../../shared/api/users';
import { formatDate } from '../../../shared/utils/format';
import type { UserResp, APIKeyResp } from '../../../shared/types';

interface UserApiKeysModalProps {
  open: boolean;
  user: UserResp;
  onClose: () => void;
}

export function UserApiKeysModal({ open, user, onClose }: UserApiKeysModalProps) {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);

  const { data, isLoading } = useQuery({
    queryKey: ['user-api-keys', user.id, page],
    queryFn: () => usersApi.apiKeys(user.id, { page, page_size: 10 }),
    enabled: open,
  });

  const columns: Column<APIKeyResp>[] = [
    { key: 'name', title: t('api_keys.title') },
    {
      key: 'key_prefix',
      title: t('api_keys.key_prefix'),
      render: (row) => <span className="font-mono text-text-secondary text-xs">{row.key_prefix}</span>,
    },
    {
      key: 'quota_usd',
      title: t('api_keys.quota_used'),
      render: (row) => (
        <span className="font-mono text-xs">
          ${row.used_quota.toFixed(2)} / {row.quota_usd > 0 ? `$${row.quota_usd.toFixed(2)}` : '∞'}
        </span>
      ),
    },
    {
      key: 'status',
      title: t('common.status'),
      render: (row) => <StatusBadge status={row.status} />,
    },
    {
      key: 'created_at',
      title: t('users.created_at'),
      render: (row) => (
        <span className="text-xs text-text-secondary">{formatDate(row.created_at)}</span>
      ),
    },
  ];

  return (
    <Modal open={open} onClose={onClose} title={`${t('users.api_keys')} - ${user.email}`} width="700px">
      {!isLoading && (!data?.list || data.list.length === 0) ? (
        <p className="text-sm text-text-tertiary text-center py-8">{t('users.no_api_keys')}</p>
      ) : (
        <Table<APIKeyResp>
          columns={columns}
          data={data?.list ?? []}
          loading={isLoading}
          rowKey={(row) => row.id}
          page={page}
          pageSize={10}
          total={data?.total ?? 0}
          onPageChange={setPage}
          autoHeight
        />
      )}
    </Modal>
  );
}
