import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import {
  Button, Card, EmptyState, Input, Label, Spinner, Tabs, TextField as HeroTextField,
} from '@heroui/react';
import { Gift, RefreshCw, Save, Search, Users, Wallet, X } from 'lucide-react';
import { inviteApi } from '../../shared/api/invite';
import { settingsApi } from '../../shared/api/settings';
import { queryKeys } from '../../shared/queryKeys';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { NativeSwitch } from '../../shared/components/NativeSwitch';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { CommonTable } from '../../shared/components/CommonTable';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { formatDateTime } from '../../shared/utils/format';

type TabKey = 'overview' | 'overrides' | 'invitees' | 'logs';

/* ==================== 总开关 + 全局比例 ==================== */

function OverviewTab() {
  const { t } = useTranslation();

  const { data, isLoading } = useQuery({
    queryKey: queryKeys.settings('invite'),
    queryFn: () => settingsApi.list('invite'),
  });

  const [enabled, setEnabled] = useState(false);
  const [ratePercent, setRatePercent] = useState('5');
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    if (!data || loaded) return;
    const map = Object.fromEntries(data.map((item) => [item.key, item.value]));
    setEnabled(map.invite_enabled === 'true');
    setRatePercent(map.invite_rebate_rate_percent || '5');
    setLoaded(true);
  }, [data, loaded]);

  const saveMutation = useCrudMutation<void, void>({
    mutationFn: () => settingsApi.update({
      settings: [
        { key: 'invite_enabled', value: String(enabled), group: 'invite' },
        { key: 'invite_rebate_rate_percent', value: ratePercent, group: 'invite' },
      ],
    }),
    successMessage: t('invite.admin_save_success'),
    queryKey: queryKeys.settings('invite'),
  });

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-10">
        <Spinner size="sm" />
      </div>
    );
  }

  return (
    <Card>
      <Card.Header>
        <Card.Title>{t('invite.admin_switch_title')}</Card.Title>
      </Card.Header>
      <Card.Content>
        <div className="space-y-5">
          <NativeSwitch
            isSelected={enabled}
            label={t('invite.admin_enabled')}
            onChange={setEnabled}
          />
          <div className="max-w-xs">
            <HeroTextField fullWidth>
              <Label>{t('invite.admin_rate_percent')}</Label>
              <Input
                max={100}
                min={0}
                step="0.1"
                type="number"
                value={ratePercent}
                onChange={(e) => setRatePercent(e.target.value)}
              />
            </HeroTextField>
          </div>
          <Button
            aria-busy={saveMutation.isPending}
            isDisabled={saveMutation.isPending}
            variant="primary"
            onPress={() => saveMutation.mutate()}
          >
            {saveMutation.isPending ? <Spinner size="sm" /> : <Save className="h-4 w-4" />}
            {t('invite.admin_save')}
          </Button>
        </div>
      </Card.Content>
    </Card>
  );
}

/* ==================== 专属比例覆盖 ==================== */

function OverridesTab() {
  const { t } = useTranslation();
  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.invite.overrides');
  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword, 300);

  const [targetUserID, setTargetUserID] = useState('');
  const [targetRate, setTargetRate] = useState('');

  const { data, isLoading, refetch } = useQuery({
    queryKey: queryKeys.inviteOverrides(page, pageSize, debouncedKeyword),
    queryFn: () => inviteApi.adminListOverrides({ page, page_size: pageSize, keyword: debouncedKeyword || undefined }),
    placeholderData: keepPreviousData,
  });

  const setMutation = useCrudMutation<{ user_id: number }, { userID: number; rate: number | null }>({
    mutationFn: ({ userID, rate }) => inviteApi.adminSetOverride(userID, rate),
    successMessage: t('invite.admin_override_success'),
    queryKey: queryKeys.inviteOverrides(),
    onSuccess: () => {
      setTargetUserID('');
      setTargetRate('');
    },
  });

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);

  return (
    <div>
      <Card className="mb-5">
        <Card.Header>
          <Card.Title>{t('invite.admin_overrides_title')}</Card.Title>
        </Card.Header>
        <Card.Content>
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <div className="w-full sm:w-32">
              <HeroTextField fullWidth aria-label={t('invite.user_id')}>
                <Input
                  min={1}
                  placeholder={t('invite.user_id')}
                  type="number"
                  value={targetUserID}
                  onChange={(e) => setTargetUserID(e.target.value)}
                />
              </HeroTextField>
            </div>
            <div className="w-full sm:w-40">
              <HeroTextField fullWidth aria-label={t('invite.admin_rate_percent')}>
                <Input
                  max={100}
                  min={0}
                  placeholder={t('invite.admin_override_placeholder')}
                  step="0.1"
                  type="number"
                  value={targetRate}
                  onChange={(e) => setTargetRate(e.target.value)}
                />
              </HeroTextField>
            </div>
            <Button
              aria-busy={setMutation.isPending}
              isDisabled={setMutation.isPending || !targetUserID.trim()}
              variant="primary"
              onPress={() => setMutation.mutate({
                userID: Number(targetUserID),
                rate: targetRate.trim() === '' ? null : Number(targetRate),
              })}
            >
              {setMutation.isPending ? <Spinner size="sm" /> : <Save className="h-4 w-4" />}
              {t('invite.admin_override_set')}
            </Button>
          </div>
        </Card.Content>
      </Card>

      <div className="mb-3 flex flex-col sm:flex-row items-stretch sm:items-center gap-3 flex-wrap">
        <div className="w-full sm:w-64">
          <HeroTextField fullWidth aria-label={t('invite.admin_overrides_search_placeholder')}>
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
              <Input
                className="pl-9"
                placeholder={t('invite.admin_overrides_search_placeholder')}
                value={keyword}
                onChange={(e) => { setKeyword(e.target.value); setPage(1); }}
              />
            </div>
          </HeroTextField>
        </div>
        <Button isIconOnly aria-label={t('common.refresh', 'Refresh')} size="sm" variant="ghost" onPress={() => refetch()}>
          <RefreshCw className="h-4 w-4" />
        </Button>
      </div>

      <CommonTable
        ariaLabel={t('invite.admin_overrides_title')}
        footer={(
          <TablePaginationFooter
            page={page}
            pageSize={pageSize}
            setPage={setPage}
            setPageSize={setPageSize}
            total={total}
            totalPages={totalPages}
          />
        )}
        minWidth={640}
      >
        <CommonTable.Header>
          <CommonTable.Column id="user_id">{t('invite.user_id')}</CommonTable.Column>
          <CommonTable.Column id="email">{t('invite.invitee_email')}</CommonTable.Column>
          <CommonTable.Column id="invite_code">{t('invite.share_link')}</CommonTable.Column>
          <CommonTable.Column id="rate_percent">{t('invite.admin_rate_percent')}</CommonTable.Column>
          <CommonTable.Column id="invited_count">{t('invite.invited_count')}</CommonTable.Column>
          <CommonTable.Column id="actions" style={{ width: 100 }}>{t('common.actions')}</CommonTable.Column>
        </CommonTable.Header>
        <CommonTable.Body>
          {isLoading ? (
            <TableLoadingRow colSpan={6} />
          ) : rows.length === 0 ? (
            <CommonTable.Row id="empty">
              <CommonTable.Cell colSpan={6}>
                <EmptyState>
                  <div className="text-sm text-default-500">{t('common.no_data')}</div>
                </EmptyState>
              </CommonTable.Cell>
            </CommonTable.Row>
          ) : (
            rows.map((row) => (
              <CommonTable.Row id={row.user_id} key={row.user_id}>
                <CommonTable.Cell>{row.user_id}</CommonTable.Cell>
                <CommonTable.Cell>{row.email || row.username || '-'}</CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-xs">{row.invite_code}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  {row.rate_percent != null ? `${row.rate_percent.toFixed(1)}%` : '-'}
                </CommonTable.Cell>
                <CommonTable.Cell>{row.invited_count}</CommonTable.Cell>
                <CommonTable.Cell>
                  <Button
                    isIconOnly
                    aria-label={t('common.delete')}
                    size="sm"
                    variant="danger-soft"
                    onPress={() => setMutation.mutate({ userID: row.user_id, rate: null })}
                  >
                    <X className="h-3.5 w-3.5" />
                  </Button>
                </CommonTable.Cell>
              </CommonTable.Row>
            ))
          )}
        </CommonTable.Body>
      </CommonTable>
    </div>
  );
}

/* ==================== 全部邀请关系 ==================== */

function InviteesTab() {
  const { t } = useTranslation();
  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.invite.invitees');
  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword, 300);

  const { data, isLoading, refetch } = useQuery({
    queryKey: queryKeys.inviteAdminInvitees(page, pageSize, debouncedKeyword),
    queryFn: () => inviteApi.adminListInvitees({ page, page_size: pageSize, keyword: debouncedKeyword || undefined }),
    placeholderData: keepPreviousData,
  });

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);

  return (
    <div>
      <div className="mb-3 flex flex-col sm:flex-row items-stretch sm:items-center gap-3 flex-wrap">
        <div className="w-full sm:w-64">
          <HeroTextField fullWidth aria-label={t('invite.admin_overrides_search_placeholder')}>
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
              <Input
                className="pl-9"
                placeholder={t('invite.admin_overrides_search_placeholder')}
                value={keyword}
                onChange={(e) => { setKeyword(e.target.value); setPage(1); }}
              />
            </div>
          </HeroTextField>
        </div>
        <Button isIconOnly aria-label={t('common.refresh', 'Refresh')} size="sm" variant="ghost" onPress={() => refetch()}>
          <RefreshCw className="h-4 w-4" />
        </Button>
      </div>

      <CommonTable
        ariaLabel={t('invite.admin_invitees_title')}
        footer={(
          <TablePaginationFooter
            page={page}
            pageSize={pageSize}
            setPage={setPage}
            setPageSize={setPageSize}
            total={total}
            totalPages={totalPages}
          />
        )}
        minWidth={720}
      >
        <CommonTable.Header>
          <CommonTable.Column id="inviter">{t('invite.inviter_email')}</CommonTable.Column>
          <CommonTable.Column id="invitee">{t('invite.invitee')}</CommonTable.Column>
          <CommonTable.Column id="created_at">{t('invite.bound_at')}</CommonTable.Column>
          <CommonTable.Column id="total_rebate">{t('invite.total_rebate')}</CommonTable.Column>
        </CommonTable.Header>
        <CommonTable.Body>
          {isLoading ? (
            <TableLoadingRow colSpan={4} />
          ) : rows.length === 0 ? (
            <CommonTable.Row id="empty">
              <CommonTable.Cell colSpan={4}>
                <EmptyState>
                  <div className="text-sm text-default-500">{t('common.no_data')}</div>
                </EmptyState>
              </CommonTable.Cell>
            </CommonTable.Row>
          ) : (
            rows.map((row) => (
              <CommonTable.Row id={`${row.inviter_id}-${row.invitee_id}`} key={`${row.inviter_id}-${row.invitee_id}`}>
                <CommonTable.Cell>{row.inviter_email || row.inviter_username || `#${row.inviter_id}`}</CommonTable.Cell>
                <CommonTable.Cell>{row.invitee_email || row.invitee_username || `#${row.invitee_id}`}</CommonTable.Cell>
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
  );
}

/* ==================== 全部返利流水 ==================== */

function LogsTab() {
  const { t } = useTranslation();
  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.invite.logs');
  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword, 300);

  const { data, isLoading, refetch } = useQuery({
    queryKey: queryKeys.inviteAdminLogs(page, pageSize, debouncedKeyword),
    queryFn: () => inviteApi.adminListLogs({ page, page_size: pageSize, keyword: debouncedKeyword || undefined }),
    placeholderData: keepPreviousData,
  });

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);

  return (
    <div>
      <div className="mb-3 flex flex-col sm:flex-row items-stretch sm:items-center gap-3 flex-wrap">
        <div className="w-full sm:w-64">
          <HeroTextField fullWidth aria-label={t('invite.admin_overrides_search_placeholder')}>
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
              <Input
                className="pl-9"
                placeholder={t('invite.admin_overrides_search_placeholder')}
                value={keyword}
                onChange={(e) => { setKeyword(e.target.value); setPage(1); }}
              />
            </div>
          </HeroTextField>
        </div>
        <Button isIconOnly aria-label={t('common.refresh', 'Refresh')} size="sm" variant="ghost" onPress={() => refetch()}>
          <RefreshCw className="h-4 w-4" />
        </Button>
      </div>

      <CommonTable
        ariaLabel={t('invite.admin_logs_title')}
        footer={(
          <TablePaginationFooter
            page={page}
            pageSize={pageSize}
            setPage={setPage}
            setPageSize={setPageSize}
            total={total}
            totalPages={totalPages}
          />
        )}
        minWidth={720}
      >
        <CommonTable.Header>
          <CommonTable.Column id="created_at">{t('invite.log_time')}</CommonTable.Column>
          <CommonTable.Column id="user">{t('invite.inviter_email')}</CommonTable.Column>
          <CommonTable.Column id="action">{t('invite.log_action')}</CommonTable.Column>
          <CommonTable.Column id="amount">{t('invite.log_amount')}</CommonTable.Column>
          <CommonTable.Column id="source">{t('invite.log_source')}</CommonTable.Column>
        </CommonTable.Header>
        <CommonTable.Body>
          {isLoading ? (
            <TableLoadingRow colSpan={5} />
          ) : rows.length === 0 ? (
            <CommonTable.Row id="empty">
              <CommonTable.Cell colSpan={5}>
                <EmptyState>
                  <div className="text-sm text-default-500">{t('common.no_data')}</div>
                </EmptyState>
              </CommonTable.Cell>
            </CommonTable.Row>
          ) : (
            rows.map((row) => (
              <CommonTable.Row id={row.id} key={row.id}>
                <CommonTable.Cell>{formatDateTime(row.created_at)}</CommonTable.Cell>
                <CommonTable.Cell>{row.user_email || `#${row.user_id}`}</CommonTable.Cell>
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
  );
}

/* ==================== 主页面 ==================== */

const TABS: { key: TabKey; labelKey: string; icon: typeof Gift }[] = [
  { key: 'overview', labelKey: 'invite.admin_switch_title', icon: Gift },
  { key: 'overrides', labelKey: 'invite.admin_overrides_title', icon: Wallet },
  { key: 'invitees', labelKey: 'invite.admin_invitees_title', icon: Users },
  { key: 'logs', labelKey: 'invite.admin_logs_title', icon: Wallet },
];

export default function InviteRebatePage() {
  const { t } = useTranslation();
  const [activeTab, setActiveTab] = useState<TabKey>('overview');

  return (
    <div>
      <div className="mb-5 w-full overflow-x-auto hide-scrollbar pb-1">
        <Tabs
          className="ag-page-tabs whitespace-nowrap"
          selectedKey={activeTab}
          onSelectionChange={(key) => setActiveTab(key as TabKey)}
        >
          <Tabs.List>
            {TABS.map((tab, index) => {
              const Icon = tab.icon;
              return (
                <Tabs.Tab key={tab.key} id={tab.key}>
                  {index > 0 ? <Tabs.Separator /> : null}
                  <Tabs.Indicator />
                  <Icon className="w-4 h-4" />
                  <span>{t(tab.labelKey)}</span>
                </Tabs.Tab>
              );
            })}
          </Tabs.List>
        </Tabs>
      </div>

      {activeTab === 'overview' && <OverviewTab />}
      {activeTab === 'overrides' && <OverridesTab />}
      {activeTab === 'invitees' && <InviteesTab />}
      {activeTab === 'logs' && <LogsTab />}
    </div>
  );
}
