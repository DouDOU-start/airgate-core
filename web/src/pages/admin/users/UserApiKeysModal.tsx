import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { EmptyState, Modal, Skeleton, Table as HeroTable, useOverlayState } from '@heroui/react';
import {
  StatusChip,
} from '../../../shared/ui';
import { usersApi } from '../../../shared/api/users';
import { formatDate } from '../../../shared/utils/format';
import { getTotalPages } from '../../../shared/utils/pagination';
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
              <HeroTable variant="primary">
                <HeroTable.ScrollContainer>
                  <HeroTable.Content aria-label={t('users.api_keys')}>
            <HeroTable.Header>
              <HeroTable.Column id="name">{t('api_keys.title')}</HeroTable.Column>
              <HeroTable.Column id="key_prefix">{t('api_keys.key_prefix')}</HeroTable.Column>
              <HeroTable.Column id="quota_usd">{t('api_keys.quota_used')}</HeroTable.Column>
              <HeroTable.Column id="status">{t('common.status')}</HeroTable.Column>
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
                rows.map((row: APIKeyResp) => (
                  <HeroTable.Row id={String(row.id)} key={row.id}>
                    <HeroTable.Cell>{row.name}</HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="font-mono text-xs text-text-secondary">{row.key_prefix}</span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="font-mono text-xs">
                        ${row.used_quota.toFixed(2)} / {row.quota_usd > 0 ? `$${row.quota_usd.toFixed(2)}` : '∞'}
                      </span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <StatusChip status={row.status} />
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="text-xs text-text-secondary">{formatDate(row.created_at)}</span>
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
