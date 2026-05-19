import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { EmptyState, Modal, Skeleton, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import {
  StatusChip,
} from '../../../shared/ui';
import { usersApi } from '../../../shared/api/users';
import { formatDate } from '../../../shared/utils/format';
import { getTotalPages } from '../../../shared/utils/pagination';
import { CommonTable } from '../../../shared/components/CommonTable';
import { TablePaginationFooter } from '../../../shared/components/TablePaginationFooter';
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
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '700px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{`${t('users.api_keys')} - ${user.email}`}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <CommonTable
                ariaLabel={t('users.api_keys')}
                footer={(
                  <TablePaginationFooter
                    page={page}
                    setPage={setPage}
                    total={total}
                    totalPages={totalPages}
                  />
                )}
              >
            <CommonTable.Header>
              <CommonTable.Column id="name" isRowHeader>{t('api_keys.title')}</CommonTable.Column>
              <CommonTable.Column id="key_prefix">{t('api_keys.key_prefix')}</CommonTable.Column>
              <CommonTable.Column id="quota_usd">{t('api_keys.quota_used')}</CommonTable.Column>
              <CommonTable.Column id="status">{t('common.status')}</CommonTable.Column>
              <CommonTable.Column id="created_at">{t('users.created_at')}</CommonTable.Column>
            </CommonTable.Header>
            <CommonTable.Body>
              {isLoading ? (
                Array.from({ length: 5 }).map((_, index) => (
                  <CommonTable.Row id={`loading-${index}`} key={`loading-${index}`}>
                    {Array.from({ length: 5 }).map((__, cellIndex) => (
                      <CommonTable.Cell key={cellIndex}>
                        <Skeleton className="h-4 w-24" />
                      </CommonTable.Cell>
                    ))}
                  </CommonTable.Row>
                ))
              ) : rows.length === 0 ? (
                <CommonTable.Row id="empty">
                  <CommonTable.Cell colSpan={5}>
                    <EmptyState>
                      <div className="text-sm text-default-500">{t('common.no_data')}</div>
                    </EmptyState>
                  </CommonTable.Cell>
                </CommonTable.Row>
              ) : (
                rows.map((row: APIKeyResp) => (
                  <CommonTable.Row id={String(row.id)} key={row.id}>
                    <CommonTable.Cell>{row.name}</CommonTable.Cell>
                    <CommonTable.Cell>
                      <span className="font-mono text-xs text-text-secondary">{row.key_prefix}</span>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <span className="font-mono text-xs">
                        ${row.used_quota.toFixed(2)} / {row.quota_usd > 0 ? `$${row.quota_usd.toFixed(2)}` : '∞'}
                      </span>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <StatusChip status={row.status} />
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <span className="text-xs text-text-secondary">{formatDate(row.created_at)}</span>
                    </CommonTable.Cell>
                  </CommonTable.Row>
                ))
              )}
            </CommonTable.Body>
              </CommonTable>
            </Modal.Body>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
