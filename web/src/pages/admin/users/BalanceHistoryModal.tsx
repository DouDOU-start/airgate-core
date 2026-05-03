import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Chip, EmptyState, Modal, Skeleton, Table as HeroTable, useOverlayState } from '@heroui/react';
import { useQuery } from '@tanstack/react-query';
import { usersApi } from '../../../shared/api/users';
import { getTotalPages } from '../../../shared/utils/pagination';
import { TablePaginationFooter } from '../../../shared/components/TablePaginationFooter';
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

  const actionColor = (action: string): 'success' | 'warning' | 'accent' => {
    switch (action) {
      case 'add': return 'success';
      case 'subtract': return 'warning';
      default: return 'accent';
    }
  };

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, 10);
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  return (
    <Modal state={modalState}>
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '750px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{`${t('users.balance_history')} - ${user.email}`}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="mb-4 rounded-md border border-glass-border bg-surface px-4 py-3">
                <p className="text-xs uppercaser text-text-tertiary">{t('users.current_balance')}</p>
                <p className="mt-1 font-mono text-lg font-bold">${user.balance.toFixed(2)}</p>
              </div>

              <HeroTable variant="primary">
                <HeroTable.ScrollContainer>
                  <HeroTable.Content aria-label={t('users.balance_history')}>
            <HeroTable.Header>
              <HeroTable.Column id="action" style={{ width: 96 }}>
                {t('users.action_type')}
              </HeroTable.Column>
              <HeroTable.Column id="amount">{t('users.amount')}</HeroTable.Column>
              <HeroTable.Column id="balance_change">
                {t('users.before_balance')} → {t('users.after_balance')}
              </HeroTable.Column>
              <HeroTable.Column id="remark">{t('users.remark')}</HeroTable.Column>
              <HeroTable.Column id="created_at">{t('users.created_at')}</HeroTable.Column>
            </HeroTable.Header>
            <HeroTable.Body>
              {isLoading ? (
                Array.from({ length: 5 }).map((_, index) => (
                  <HeroTable.Row id={`loading-${index}`} key={`loading-${index}`}>
                    {Array.from({ length: 5 }).map((__, cellIndex) => (
                      <HeroTable.Cell key={cellIndex}>
                        <Skeleton className="h-4 w-24" />
                      </HeroTable.Cell>
                    ))}
                  </HeroTable.Row>
                ))
              ) : rows.length === 0 ? (
                <HeroTable.Row id="empty">
                  <HeroTable.Cell colSpan={5}>
                    <EmptyState />
                  </HeroTable.Cell>
                </HeroTable.Row>
              ) : (
                rows.map((row: BalanceLogResp) => (
                  <HeroTable.Row id={String(row.id)} key={row.id}>
                    <HeroTable.Cell>
                      <Chip color={actionColor(row.action)} size="sm" variant="soft">
                        {actionLabel(row.action)}
                      </Chip>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className={`font-mono text-xs font-semibold ${row.action === 'add' ? 'text-success' : row.action === 'subtract' ? 'text-danger' : 'text-info'}`}>
                        {row.action === 'add' ? '+' : row.action === 'subtract' ? '-' : '='}{row.amount.toFixed(2)}
                      </span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="font-mono text-xs text-text-secondary">
                        ${row.before_balance.toFixed(2)} → ${row.after_balance.toFixed(2)}
                      </span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="text-xs text-text-tertiary">{row.remark || '-'}</span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="text-xs text-text-secondary">
                        {new Date(row.created_at).toLocaleString('zh-CN', {
                          day: '2-digit',
                          hour: '2-digit',
                          minute: '2-digit',
                          month: '2-digit',
                        })}
                      </span>
                    </HeroTable.Cell>
                  </HeroTable.Row>
                ))
              )}
            </HeroTable.Body>
          </HeroTable.Content>
                </HeroTable.ScrollContainer>
                <HeroTable.Footer>
                  <TablePaginationFooter
                    page={page}
                    setPage={setPage}
                    total={total}
                    totalPages={totalPages}
                  />
                </HeroTable.Footer>
              </HeroTable>
            </Modal.Body>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
