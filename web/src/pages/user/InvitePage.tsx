import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, EmptyState, Input, Spinner } from '@heroui/react';
import { AlertTriangle, Copy, Gift, Users, Wallet } from 'lucide-react';
import { inviteApi } from '../../shared/api/invite';
import { queryKeys } from '../../shared/queryKeys';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { usePagination } from '../../shared/hooks/usePagination';
import { useClipboard } from '../../shared/hooks/useClipboard';
import { useToast } from '../../shared/ui';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { CommonTable } from '../../shared/components/CommonTable';
import { formatDateTime } from '../../shared/utils/format';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import type { InviteTransferResp } from '../../shared/types';

export default function InvitePage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const copy = useClipboard();

  const { data: me, isLoading: meLoading } = useQuery({
    queryKey: queryKeys.inviteMe(),
    queryFn: () => inviteApi.getMe(),
  });

  const [bindCode, setBindCode] = useState('');
  const bindMutation = useCrudMutation<{ bound: boolean }, string>({
    mutationFn: (code) => inviteApi.bind(code),
    queryKey: queryKeys.inviteMe(),
    onSuccess: () => {
      setBindCode('');
      toast('success', t('invite.bind_success'));
    },
  });

  const transferMutation = useCrudMutation<InviteTransferResp, void>({
    mutationFn: () => inviteApi.transfer(),
    queryKey: queryKeys.inviteMe(),
    onSuccess: (result) => {
      toast('success', t('invite.transfer_success', { amount: result.transferred.toFixed(2) }));
      queryClient.invalidateQueries({ queryKey: queryKeys.userMe() });
      queryClient.invalidateQueries({ queryKey: queryKeys.myBalanceHistory() });
    },
  });

  // 我邀请的人（分页）
  const inviteesPagination = usePagination(DEFAULT_PAGE_SIZE, 'user.invite.invitees');
  const { data: inviteesData, isLoading: inviteesLoading } = useQuery({
    queryKey: queryKeys.inviteMyInvitees({ page: inviteesPagination.page, page_size: inviteesPagination.pageSize }),
    queryFn: () => inviteApi.listMyInvitees({ page: inviteesPagination.page, page_size: inviteesPagination.pageSize }),
    enabled: !!me?.enabled,
    placeholderData: keepPreviousData,
  });

  // 返利流水（分页）
  const logsPagination = usePagination(DEFAULT_PAGE_SIZE, 'user.invite.logs');
  const { data: logsData, isLoading: logsLoading } = useQuery({
    queryKey: queryKeys.inviteMyLogs({ page: logsPagination.page, page_size: logsPagination.pageSize }),
    queryFn: () => inviteApi.listMyLogs({ page: logsPagination.page, page_size: logsPagination.pageSize }),
    enabled: !!me?.enabled,
    placeholderData: keepPreviousData,
  });

  const shareLink = me?.invite_code
    ? `${window.location.origin}/?invite_code=${encodeURIComponent(me.invite_code)}`
    : '';

  const inviteeRows = inviteesData?.list ?? [];
  const inviteeTotal = inviteesData?.total ?? 0;
  const inviteeTotalPages = getTotalPages(inviteeTotal, inviteesPagination.pageSize);

  const logRows = logsData?.list ?? [];
  const logTotal = logsData?.total ?? 0;
  const logTotalPages = getTotalPages(logTotal, logsPagination.pageSize);

  if (meLoading) {
    return (
      <div className="flex items-center justify-center py-16">
        <Spinner size="sm" />
      </div>
    );
  }

  if (!me?.enabled) {
    return (
      <div className="mx-auto w-full max-w-5xl">
        <Alert status="warning">
          <Alert.Indicator>
            <AlertTriangle className="h-4 w-4" />
          </Alert.Indicator>
          <Alert.Content>
            <Alert.Title>{t('invite.disabled_title')}</Alert.Title>
            <Alert.Description>{t('invite.disabled_desc')}</Alert.Description>
          </Alert.Content>
        </Alert>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-5xl space-y-6">
      {/* 我的邀请信息 */}
      <Card>
        <Card.Header>
          <Card.Title className="flex items-center gap-2">
            <Gift className="h-4 w-4 text-primary" />
            {t('nav.invite')}
          </Card.Title>
        </Card.Header>
        <Card.Content>
          <div className="space-y-5">
            <p className="text-sm text-text-tertiary">
              {t('invite.rate_hint', { rate: (me?.effective_rate_percent ?? 0).toFixed(1) })}
            </p>

            {/* 邀请链接 */}
            <div>
              <p className="mb-2 text-sm font-medium text-text">{t('invite.share_link')}</p>
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                <div className="w-full sm:max-w-md">
                  <Input readOnly className="font-mono text-xs" value={shareLink} />
                </div>
                <Button
                  size="sm"
                  variant="secondary"
                  onPress={() => copy(shareLink)}
                >
                  <Copy className="h-3.5 w-3.5" />
                  {t('common.copy')}
                </Button>
              </div>
            </div>

            {/* 统计概览 */}
            <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
              <div className="rounded-[var(--radius)] border border-border p-3">
                <p className="text-xs text-text-tertiary">{t('invite.invited_count')}</p>
                <p className="mt-1 font-mono text-lg font-semibold text-text">{me?.invited_count ?? 0}</p>
              </div>
              <div className="rounded-[var(--radius)] border border-border p-3">
                <p className="text-xs text-text-tertiary">{t('invite.rebate_balance')}</p>
                <p className="mt-1 font-mono text-lg font-semibold text-text">${(me?.rebate_balance ?? 0).toFixed(2)}</p>
              </div>
              <div className="rounded-[var(--radius)] border border-border p-3">
                <p className="text-xs text-text-tertiary">{t('invite.rebate_total')}</p>
                <p className="mt-1 font-mono text-lg font-semibold text-text">${(me?.rebate_total ?? 0).toFixed(2)}</p>
              </div>
              <div className="flex items-center rounded-[var(--radius)] border border-border p-3">
                <Button
                  aria-busy={transferMutation.isPending}
                  className="w-full"
                  isDisabled={transferMutation.isPending || (me?.rebate_balance ?? 0) <= 0}
                  size="sm"
                  variant="primary"
                  onPress={() => transferMutation.mutate()}
                >
                  {transferMutation.isPending ? <Spinner size="sm" /> : <Wallet className="h-3.5 w-3.5" />}
                  {t('invite.transfer_submit')}
                </Button>
              </div>
            </div>

            {/* 补绑邀请码：尚未绑定邀请人时展示 */}
            {!me?.inviter_id ? (
              <div className="border-t border-border pt-4">
                <p className="mb-2 text-sm font-medium text-text">{t('invite.bind_title')}</p>
                <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                  <div className="w-full sm:max-w-xs">
                    <Input
                      aria-label={t('invite.bind_placeholder')}
                      className="font-mono"
                      maxLength={32}
                      placeholder={t('invite.bind_placeholder')}
                      value={bindCode}
                      onChange={(e) => setBindCode(e.target.value)}
                    />
                  </div>
                  <Button
                    aria-busy={bindMutation.isPending}
                    isDisabled={bindMutation.isPending || !bindCode.trim()}
                    size="sm"
                    variant="secondary"
                    onPress={() => bindMutation.mutate(bindCode.trim())}
                  >
                    {bindMutation.isPending ? <Spinner size="sm" /> : <Gift className="h-3.5 w-3.5" />}
                    {t('invite.bind_submit')}
                  </Button>
                </div>
              </div>
            ) : null}
          </div>
        </Card.Content>
      </Card>

      {/* 我邀请的人 */}
      <div>
        <div className="mb-3 flex items-center gap-2">
          <Users className="h-4 w-4 text-text-tertiary" />
          <h3 className="text-base font-semibold text-text">{t('invite.invitees_title')}</h3>
        </div>
        <CommonTable
          ariaLabel={t('invite.invitees_title')}
          footer={(
            <TablePaginationFooter
              page={inviteesPagination.page}
              pageSize={inviteesPagination.pageSize}
              setPage={inviteesPagination.setPage}
              setPageSize={inviteesPagination.setPageSize}
              total={inviteeTotal}
              totalPages={inviteeTotalPages}
            />
          )}
          minWidth={640}
        >
          <CommonTable.Header>
            <CommonTable.Column id="invitee_email">{t('invite.invitee_email')}</CommonTable.Column>
            <CommonTable.Column id="created_at">{t('invite.bound_at')}</CommonTable.Column>
            <CommonTable.Column id="total_rebate">{t('invite.total_rebate')}</CommonTable.Column>
          </CommonTable.Header>
          <CommonTable.Body>
            {inviteesLoading ? (
              <TableLoadingRow colSpan={3} />
            ) : inviteeRows.length === 0 ? (
              <CommonTable.Row id="empty">
                <CommonTable.Cell colSpan={3}>
                  <EmptyState>
                    <div className="text-sm text-default-500">{t('common.no_data')}</div>
                  </EmptyState>
                </CommonTable.Cell>
              </CommonTable.Row>
            ) : (
              inviteeRows.map((row) => (
                <CommonTable.Row id={row.invitee_id} key={row.invitee_id}>
                  <CommonTable.Cell>{row.invitee_email || row.invitee_username || row.invitee_id}</CommonTable.Cell>
                  <CommonTable.Cell>{formatDateTime(row.created_at)}</CommonTable.Cell>
                  <CommonTable.Cell>
                    <span className="font-mono">${row.total_rebate.toFixed(2)}</span>
                  </CommonTable.Cell>
                </CommonTable.Row>
              ))
            )}
          </CommonTable.Body>
        </CommonTable>
      </div>

      {/* 返利流水 */}
      <div>
        <div className="mb-3 flex items-center gap-2">
          <Wallet className="h-4 w-4 text-text-tertiary" />
          <h3 className="text-base font-semibold text-text">{t('invite.logs_title')}</h3>
        </div>
        <CommonTable
          ariaLabel={t('invite.logs_title')}
          footer={(
            <TablePaginationFooter
              page={logsPagination.page}
              pageSize={logsPagination.pageSize}
              setPage={logsPagination.setPage}
              setPageSize={logsPagination.setPageSize}
              total={logTotal}
              totalPages={logTotalPages}
            />
          )}
          minWidth={640}
        >
          <CommonTable.Header>
            <CommonTable.Column id="created_at">{t('invite.log_time')}</CommonTable.Column>
            <CommonTable.Column id="action">{t('invite.log_action')}</CommonTable.Column>
            <CommonTable.Column id="amount">{t('invite.log_amount')}</CommonTable.Column>
            <CommonTable.Column id="source">{t('invite.log_source')}</CommonTable.Column>
          </CommonTable.Header>
          <CommonTable.Body>
            {logsLoading ? (
              <TableLoadingRow colSpan={4} />
            ) : logRows.length === 0 ? (
              <CommonTable.Row id="empty">
                <CommonTable.Cell colSpan={4}>
                  <EmptyState>
                    <div className="text-sm text-default-500">{t('common.no_data')}</div>
                  </EmptyState>
                </CommonTable.Cell>
              </CommonTable.Row>
            ) : (
              logRows.map((row) => (
                <CommonTable.Row id={row.id} key={row.id}>
                  <CommonTable.Cell>{formatDateTime(row.created_at)}</CommonTable.Cell>
                  <CommonTable.Cell>{t(`invite.action_${row.action}`)}</CommonTable.Cell>
                  <CommonTable.Cell>
                    <span className="font-mono">${row.amount.toFixed(2)}</span>
                  </CommonTable.Cell>
                  <CommonTable.Cell>{row.source_user_email || '-'}</CommonTable.Cell>
                </CommonTable.Row>
              ))
            )}
          </CommonTable.Body>
        </CommonTable>
      </div>
    </div>
  );
}
