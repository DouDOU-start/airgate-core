import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Modal } from '../../../shared/components/Modal';
import { Table, type Column } from '../../../shared/components/Table';
import { Badge } from '../../../shared/components/Badge';
import { usersApi } from '../../../shared/api/users';
import type { UserResp, BalanceLogResp } from '../../../shared/types';

interface BalanceHistoryModalProps {
  open: boolean;
  user: UserResp;
  onClose: () => void;
}

export function BalanceHistoryModal({ open, user, onClose }: BalanceHistoryModalProps) {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);

  const { data, isLoading } = useQuery({
    queryKey: ['user-balance-history', user.id, page],
    queryFn: () => usersApi.balanceHistory(user.id, { page, page_size: 10 }),
    enabled: open,
  });

  const actionLabel = (action: string) => {
    switch (action) {
      case 'add': return t('users.action_add');
      case 'subtract': return t('users.action_subtract');
      case 'set': return t('users.action_set');
      default: return action;
    }
  };

  const actionVariant = (action: string): 'success' | 'warning' | 'info' => {
    switch (action) {
      case 'add': return 'success';
      case 'subtract': return 'warning';
      default: return 'info';
    }
  };

  const columns: Column<BalanceLogResp>[] = [
    {
      key: 'action',
      title: t('users.action_type'),
      width: '80px',
      render: (row) => <Badge variant={actionVariant(row.action)}>{actionLabel(row.action)}</Badge>,
    },
    {
      key: 'amount',
      title: t('users.amount'),
      render: (row) => (
        <span className={`font-mono text-xs font-semibold ${row.action === 'add' ? 'text-success' : row.action === 'subtract' ? 'text-danger' : 'text-info'}`}>
          {row.action === 'add' ? '+' : row.action === 'subtract' ? '-' : '='}{row.amount.toFixed(2)}
        </span>
      ),
    },
    {
      key: 'balance_change',
      title: `${t('users.before_balance')} → ${t('users.after_balance')}`,
      render: (row) => (
        <span className="font-mono text-xs text-text-secondary">
          ${row.before_balance.toFixed(2)} → ${row.after_balance.toFixed(2)}
        </span>
      ),
    },
    {
      key: 'remark',
      title: t('users.remark'),
      render: (row) => <span className="text-xs text-text-tertiary">{row.remark || '-'}</span>,
    },
    {
      key: 'created_at',
      title: t('users.created_at'),
      render: (row) => (
        <span className="text-xs text-text-secondary">
          {new Date(row.created_at).toLocaleString('zh-CN', {
            month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
          })}
        </span>
      ),
    },
  ];

  return (
    <Modal open={open} onClose={onClose} title={`${t('users.balance_history')} - ${user.email}`} width="750px">
      <div className="rounded-md bg-surface border border-glass-border px-4 py-3 mb-4">
        <p className="text-xs text-text-tertiary uppercase tracking-wider">{t('users.current_balance')}</p>
        <p className="text-lg font-bold mt-1 font-mono">${user.balance.toFixed(2)}</p>
      </div>

      {!isLoading && (!data?.list || data.list.length === 0) ? (
        <p className="text-sm text-text-tertiary text-center py-8">{t('users.no_balance_history')}</p>
      ) : (
        <Table<BalanceLogResp>
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
