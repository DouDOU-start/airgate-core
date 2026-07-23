import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import {
  Button, Card, ComboBox, EmptyState, Input, Label, ListBox, Modal, Spinner, Tabs,
  TextArea, TextField as HeroTextField, useOverlayState,
} from '@heroui/react';
import { Gift, Pencil, Percent, Plus, Save, Search, Users, Wallet, X } from 'lucide-react';
import { inviteApi } from '../../shared/api/invite';
import { settingsApi } from '../../shared/api/settings';
import { usersApi } from '../../shared/api/users';
import { queryKeys } from '../../shared/queryKeys';
import type { InviteOverrideEntry } from '../../shared/types';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { NativeSwitch } from '../../shared/components/NativeSwitch';
import { DialogTriggerShim } from '../../shared/components/DialogTriggerShim';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { CommonTable } from '../../shared/components/CommonTable';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { formatDateTime } from '../../shared/utils/format';
import { RefreshButton } from '../../shared/components/RefreshButton';

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
  const [description, setDescription] = useState('');
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    if (!data || loaded) return;
    const map = Object.fromEntries(data.map((item) => [item.key, item.value]));
    setEnabled(map.invite_enabled === 'true');
    setRatePercent(map.invite_rebate_rate_percent || '5');
    setDescription(map.invite_description || '');
    setLoaded(true);
  }, [data, loaded]);

  const saveMutation = useCrudMutation<void, void>({
    mutationFn: () => settingsApi.update({
      settings: [
        { key: 'invite_enabled', value: String(enabled), group: 'invite' },
        { key: 'invite_rebate_rate_percent', value: ratePercent, group: 'invite' },
        { key: 'invite_description', value: description.trim(), group: 'invite' },
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
          <div className="max-w-xl">
            <HeroTextField fullWidth>
              <Label>{t('invite.admin_description')}</Label>
              <TextArea
                maxLength={120}
                placeholder={t('invite.admin_description_placeholder')}
                rows={2}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
              />
            </HeroTextField>
            <p className="mt-1 text-xs text-text-tertiary">{t('invite.admin_description_hint')}</p>
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

function OverrideModal({
  open,
  onClose,
  onSubmit,
  loading,
  editingEntry,
}: {
  open: boolean;
  onClose: () => void;
  onSubmit: (data: { userID: number; rate: number | null }) => void;
  loading: boolean;
  editingEntry?: InviteOverrideEntry | null;
}) {
  const { t } = useTranslation();
  const isEditing = !!editingEntry;
  const [targetUserID, setTargetUserID] = useState(editingEntry ? String(editingEntry.user_id) : '');
  const [targetUserKeyword, setTargetUserKeyword] = useState('');
  const debouncedTargetUserKeyword = useDebouncedValue(targetUserKeyword.trim(), 250);
  const [targetUserLabel, setTargetUserLabel] = useState('');
  const [targetRate, setTargetRate] = useState(
    editingEntry?.rate_percent != null ? String(editingEntry.rate_percent) : '',
  );

  // 专属比例目标用户：模糊搜索用户邮箱/用户名（同使用记录页的搜索模式）；编辑时目标用户固定，无需搜索
  const { data: targetUsersData } = useQuery({
    queryKey: queryKeys.adminUsersSearch(debouncedTargetUserKeyword),
    queryFn: () => usersApi.list({ page: 1, page_size: 20, keyword: debouncedTargetUserKeyword }),
    enabled: !isEditing && debouncedTargetUserKeyword.length > 0,
  });
  const targetUserOptions = (targetUsersData?.list ?? []).map((u) => ({
    id: String(u.id),
    label: u.username || u.email,
    description: u.username ? u.email : undefined,
    textValue: `${u.username || ''} ${u.email}`,
  }));

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  const handleSubmit = () => {
    if (!targetUserID.trim()) return;
    onSubmit({
      userID: Number(targetUserID),
      rate: targetRate.trim() === '' ? null : Number(targetRate),
    });
  };

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog className="ag-elevation-modal" style={{ maxWidth: '480px', width: 'min(100%, calc(100vw - 2rem))' }}>
            <Modal.Header>
              <Modal.Heading>{t(isEditing ? 'invite.admin_override_edit' : 'invite.admin_override_add')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                {isEditing ? (
                  <div className="flex flex-col gap-1.5">
                    <Label>{t('invite.admin_overrides_search_placeholder')}</Label>
                    <div className="rounded-lg border border-glass-border px-3 py-2 text-sm text-text">
                      {editingEntry?.email || editingEntry?.username || `#${editingEntry?.user_id}`}
                    </div>
                  </div>
                ) : (
                <div className="flex flex-col gap-1.5">
                  <Label>{t('invite.admin_overrides_search_placeholder')}</Label>
                  <ComboBox
                    aria-label={t('invite.admin_overrides_search_placeholder')}
                    allowsEmptyCollection
                    fullWidth
                    inputValue={targetUserKeyword}
                    items={targetUserOptions}
                    menuTrigger="focus"
                    selectedKey={targetUserID || null}
                    onInputChange={(value) => {
                      setTargetUserKeyword(value);
                      if (!value || (targetUserID && value !== targetUserLabel)) {
                        setTargetUserID('');
                        setTargetUserLabel('');
                      }
                    }}
                    onSelectionChange={(key) => {
                      const value = key == null ? '' : String(key);
                      setTargetUserID(value);
                      const option = targetUserOptions.find((item) => item.id === value);
                      const label = option?.label ? String(option.label) : '';
                      setTargetUserLabel(label);
                      setTargetUserKeyword(label);
                    }}
                  >
                    <ComboBox.InputGroup className="relative">
                      <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                      <Input className="pl-9" placeholder={t('invite.admin_overrides_search_placeholder')} />
                    </ComboBox.InputGroup>
                    <ComboBox.Popover>
                      <ListBox
                        items={targetUserOptions}
                        renderEmptyState={() => (
                          <div className="px-3 py-6 text-center text-xs text-text-tertiary">
                            {targetUserKeyword.trim() ? t('common.no_data') : t('invite.admin_overrides_search_placeholder')}
                          </div>
                        )}
                      >
                        {(item) => (
                          <ListBox.Item id={item.id} textValue={item.textValue}>
                            <div className="min-w-0">
                              <div className="truncate">{item.label}</div>
                              {item.description ? (
                                <div className="truncate text-xs text-text-tertiary">{item.description}</div>
                              ) : null}
                            </div>
                          </ListBox.Item>
                        )}
                      </ListBox>
                    </ComboBox.Popover>
                  </ComboBox>
                </div>
                )}
                <HeroTextField fullWidth>
                  <Label>{t('invite.admin_rate_percent')}</Label>
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
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>{t('common.cancel')}</Button>
              <Button
                aria-busy={loading}
                isDisabled={loading || !targetUserID.trim()}
                variant="primary"
                onPress={handleSubmit}
              >
                {loading ? <Spinner size="sm" /> : <Save className="h-4 w-4" />}
                {t('invite.admin_override_set')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

function OverridesTab() {
  const { t } = useTranslation();
  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.invite.overrides');
  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword, 300);
  const [modalMode, setModalMode] = useState<'add' | InviteOverrideEntry | null>(null);

  const { data, isFetching, isLoading, refetch } = useQuery({
    queryKey: queryKeys.inviteOverrides(page, pageSize, debouncedKeyword),
    queryFn: () => inviteApi.adminListOverrides({ page, page_size: pageSize, keyword: debouncedKeyword || undefined }),
    placeholderData: keepPreviousData,
  });

  const setMutation = useCrudMutation<{ user_id: number }, { userID: number; rate: number | null }>({
    mutationFn: ({ userID, rate }) => inviteApi.adminSetOverride(userID, rate),
    successMessage: t('invite.admin_override_success'),
    queryKey: queryKeys.inviteOverrides(),
    onSuccess: () => setModalMode(null),
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
        <RefreshButton
          ariaLabel={t('common.refresh', 'Refresh')}
          isRefreshing={isFetching}
          onRefresh={refetch}
        />
        <Button className="sm:ml-auto" variant="primary" onPress={() => setModalMode('add')}>
          <Plus className="h-4 w-4" />
          {t('invite.admin_override_add')}
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
        minWidth={590}
      >
        <CommonTable.Header>
          <CommonTable.Column id="email">{t('invite.invitee_email')}</CommonTable.Column>
          <CommonTable.Column id="invite_code">{t('invite.admin_invite_link')}</CommonTable.Column>
          <CommonTable.Column id="rate_percent">{t('invite.admin_rate_percent')}</CommonTable.Column>
          <CommonTable.Column id="invited_count">{t('invite.invited_count')}</CommonTable.Column>
          <CommonTable.Column id="actions" style={{ width: 130 }}>{t('common.actions')}</CommonTable.Column>
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
              <CommonTable.Row id={row.user_id} key={row.user_id}>
                <CommonTable.Cell>{row.email || row.username || '-'}</CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-xs">{row.invite_code}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  {row.rate_percent != null ? `${row.rate_percent.toFixed(1)}%` : '-'}
                </CommonTable.Cell>
                <CommonTable.Cell>{row.invited_count}</CommonTable.Cell>
                <CommonTable.Cell>
                  <div className="flex items-center gap-1.5">
                    <Button
                      isIconOnly
                      aria-label={t('common.edit')}
                      size="sm"
                      variant="ghost"
                      onPress={() => setModalMode(row)}
                    >
                      <Pencil className="h-3.5 w-3.5" />
                    </Button>
                    <Button
                      isIconOnly
                      aria-label={t('common.delete')}
                      size="sm"
                      variant="danger-soft"
                      onPress={() => setMutation.mutate({ userID: row.user_id, rate: null })}
                    >
                      <X className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                </CommonTable.Cell>
              </CommonTable.Row>
            ))
          )}
        </CommonTable.Body>
      </CommonTable>

      {modalMode && (
        <OverrideModal
          editingEntry={modalMode === 'add' ? null : modalMode}
          loading={setMutation.isPending}
          open={!!modalMode}
          onClose={() => setModalMode(null)}
          onSubmit={(input) => setMutation.mutate(input)}
        />
      )}
    </div>
  );
}

/* ==================== 全部邀请关系 ==================== */

function InviteesTab() {
  const { t } = useTranslation();
  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.invite.invitees');
  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword, 300);

  const { data, isFetching, isLoading, refetch } = useQuery({
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
        <RefreshButton
          ariaLabel={t('common.refresh', 'Refresh')}
          isRefreshing={isFetching}
          onRefresh={refetch}
        />
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

  const { data, isFetching, isLoading, refetch } = useQuery({
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
        <RefreshButton
          ariaLabel={t('common.refresh', 'Refresh')}
          isRefreshing={isFetching}
          onRefresh={refetch}
        />
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
  { key: 'overview', labelKey: 'invite.admin_switch_tab', icon: Gift },
  { key: 'invitees', labelKey: 'invite.admin_invitees_title', icon: Users },
  { key: 'logs', labelKey: 'invite.admin_logs_title', icon: Wallet },
  { key: 'overrides', labelKey: 'invite.admin_overrides_title', icon: Percent },
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
      {activeTab === 'invitees' && <InviteesTab />}
      {activeTab === 'logs' && <LogsTab />}
      {activeTab === 'overrides' && <OverridesTab />}
    </div>
  );
}
