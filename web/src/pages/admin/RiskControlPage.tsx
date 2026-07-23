import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import {
  Button, Chip, EmptyState, Input, Label, ListBox, Select,
  TextField as HeroTextField,
} from '@heroui/react';
import {
  Activity, Eraser, KeyRound, Search, Settings2, Shield, Trash2, Unlock, Zap,
} from 'lucide-react';
import { riskControlApi, type ModerationLog } from '../../shared/api/riskControl';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { usePagination } from '../../shared/hooks/usePagination';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { queryKeys } from '../../shared/queryKeys';
import { CommonTable } from '../../shared/components/CommonTable';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';
import { RefreshButton } from '../../shared/components/RefreshButton';
import { StatCard } from '../../shared/components/StatCard';
import { getTotalPages } from '../../shared/utils/pagination';
import { ConfigModal } from './riskcontrol/ConfigModal';
import { LogDetailModal } from './riskcontrol/LogDetailModal';

const DEFAULT_PAGE_SIZE = 20;

type ResultFilter = '' | 'hit' | 'blocked' | 'pass' | 'error';

export default function RiskControlPage() {
  const { t } = useTranslation();

  const [showConfig, setShowConfig] = useState(false);
  const [detailLog, setDetailLog] = useState<ModerationLog | null>(null);
  const [unbanTarget, setUnbanTarget] = useState<ModerationLog | null>(null);
  const [confirmClearHashes, setConfirmClearHashes] = useState(false);
  const [clearLogsType, setClearLogsType] = useState<ResultFilter>('');
  const [confirmClearLogs, setConfirmClearLogs] = useState(false);
  const [resultFilter, setResultFilter] = useState<ResultFilter>('');
  const [search, setSearch] = useState('');
  const debouncedSearch = useDebouncedValue(search);
  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.risk_control');

  const { data: config } = useQuery({
    queryKey: queryKeys.riskControlConfig(),
    queryFn: () => riskControlApi.getConfig(),
  });

  const { data: status, isFetching: statusFetching, refetch: refetchStatus } = useQuery({
    queryKey: queryKeys.riskControlStatus(),
    queryFn: () => riskControlApi.getStatus(),
    refetchInterval: 10_000,
  });

  const listQuery = useMemo(
    () => ({
      page,
      page_size: pageSize,
      ...(resultFilter ? { result: resultFilter } : {}),
      ...(debouncedSearch ? { search: debouncedSearch } : {}),
    }),
    [page, pageSize, resultFilter, debouncedSearch],
  );

  const {
    data: logsData,
    isFetching: logsFetching,
    isLoading: logsLoading,
    refetch: refetchLogs,
  } = useQuery({
    queryKey: queryKeys.riskControlLogs(listQuery),
    queryFn: () => riskControlApi.listLogs(listQuery),
    placeholderData: keepPreviousData,
  });

  const unbanMutation = useCrudMutation({
    mutationFn: (userId: number) => riskControlApi.unbanUser(userId),
    successMessage: t('risk_control.unban_success'),
    queryKey: queryKeys.riskControlLogs(listQuery),
    onSuccess: () => setUnbanTarget(null),
  });

  const clearLogsMutation = useCrudMutation({
    mutationFn: (result: string) => riskControlApi.clearLogs(result || undefined),
    successMessage: t('risk_control.logs_cleared'),
    queryKey: queryKeys.riskControlLogs(listQuery),
    extraQueryKeys: [queryKeys.riskControlStatus()],
    onSuccess: () => setConfirmClearLogs(false),
  });

  const clearHashesMutation = useCrudMutation({
    mutationFn: () => riskControlApi.clearHashes(),
    successMessage: t('risk_control.hashes_cleared'),
    queryKey: queryKeys.riskControlStatus(),
    onSuccess: () => setConfirmClearHashes(false),
  });

  const rows = logsData?.list ?? [];
  const total = logsData?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);

  const resultOptions: { id: ResultFilter; label: string }[] = [
    { id: '', label: t('risk_control.result_all') },
    { id: 'hit', label: t('risk_control.result_hit') },
    { id: 'blocked', label: t('risk_control.result_blocked') },
    { id: 'pass', label: t('risk_control.result_pass') },
    { id: 'error', label: t('risk_control.result_error') },
  ];

  const actionChipColor = (log: ModerationLog) => {
    if (log.action === 'allow') return log.flagged ? 'warning' : 'default';
    if (log.action === 'error') return 'warning';
    return 'danger';
  };

  return (
    <div>
      {/* 统计卡 */}
      <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard
          accentColor="var(--ag-primary)"
          icon={<Shield className="h-5 w-5" />}
          title={t('risk_control.stat_checked')}
          value={(status?.pre_block_checked ?? 0).toLocaleString()}
        />
        <StatCard
          accentColor="var(--danger)"
          icon={<Zap className="h-5 w-5" />}
          title={t('risk_control.stat_blocked')}
          value={(status?.pre_block_blocked ?? 0).toLocaleString()}
        />
        <StatCard
          accentColor="var(--ag-info)"
          icon={<KeyRound className="h-5 w-5" />}
          title={t('risk_control.stat_keys_available')}
          value={`${status?.api_key_available_count ?? 0} / ${config?.api_key_count ?? 0}`}
        />
        <StatCard
          accentColor="var(--ag-warning)"
          icon={<Activity className="h-5 w-5" />}
          title={t('risk_control.stat_avg_latency')}
          value={`${status?.pre_block_avg_latency_ms ?? 0} ms`}
        />
      </div>

      {/* 工具栏 */}
      <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-3 mb-5 flex-wrap">
        <div className="w-full sm:w-80">
          <HeroTextField fullWidth aria-label={t('common.search')}>
            <div className="relative">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-default-400 pointer-events-none" />
              <Input
                className="pl-9"
                placeholder={t('risk_control.search_placeholder')}
                value={search}
                onChange={(e) => { setSearch(e.target.value); setPage(1); }}
              />
            </div>
          </HeroTextField>
        </div>
        <div className="w-full sm:w-48">
          <Select
            fullWidth
            selectedKey={resultFilter}
            onSelectionChange={(key) => {
              setResultFilter(key == null ? '' : (String(key) as ResultFilter));
              setPage(1);
            }}
          >
            <Label className="sr-only">{t('risk_control.result_filter')}</Label>
            <Select.Trigger>
              <Select.Value>{resultOptions.find((o) => o.id === resultFilter)?.label}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={resultOptions}>
                {(item) => <ListBox.Item id={item.id} textValue={item.label}>{item.label}</ListBox.Item>}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>
        <Chip color={status?.mode && status.mode !== 'off' ? 'success' : 'default'} size="sm">
          {t(`risk_control.mode_${status?.mode || 'off'}`)}
        </Chip>
        <div className="flex items-center gap-2 sm:ml-auto">
          <Button
            size="sm"
            variant="ghost"
            onPress={() => { setClearLogsType(''); setConfirmClearLogs(true); }}
          >
            <Trash2 className="w-3.5 h-3.5" />
            {t('risk_control.clear_logs')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onPress={() => setConfirmClearHashes(true)}
          >
            <Eraser className="w-3.5 h-3.5" />
            {t('risk_control.clear_hashes')}
          </Button>
          <RefreshButton
            ariaLabel={t('common.refresh')}
            isRefreshing={statusFetching || logsFetching}
            onRefresh={() => { refetchStatus(); refetchLogs(); }}
          />
          <Button variant="primary" onPress={() => setShowConfig(true)}>
            <Settings2 className="w-4 h-4" />
            {t('risk_control.open_config')}
          </Button>
        </div>
      </div>

      {/* 审核日志表 */}
      <CommonTable ariaLabel={t('risk_control.title')} minWidth={980}>
        <CommonTable.Header>
          <CommonTable.Column id="time" style={{ width: 150 }}>{t('risk_control.col_time')}</CommonTable.Column>
          <CommonTable.Column id="user" style={{ width: 190 }}>{t('risk_control.col_user')}</CommonTable.Column>
          <CommonTable.Column id="model" style={{ width: 140 }}>{t('risk_control.col_model')}</CommonTable.Column>
          <CommonTable.Column id="action" style={{ width: 110 }}>{t('risk_control.col_action')}</CommonTable.Column>
          <CommonTable.Column id="category" style={{ width: 160 }}>{t('risk_control.col_category')}</CommonTable.Column>
          <CommonTable.Column id="excerpt">{t('risk_control.col_excerpt')}</CommonTable.Column>
          <CommonTable.Column id="actions" style={{ width: 120 }}>{t('common.actions')}</CommonTable.Column>
        </CommonTable.Header>
        <CommonTable.Body>
          {logsLoading ? (
            <TableLoadingRow colSpan={7} />
          ) : rows.length === 0 ? (
            <CommonTable.Row id="empty">
              <CommonTable.Cell colSpan={7}>
                <EmptyState>
                  <div className="text-sm text-default-500">{t('risk_control.empty_hint')}</div>
                </EmptyState>
              </CommonTable.Cell>
            </CommonTable.Row>
          ) : (
            rows.map((log) => (
              <CommonTable.Row key={log.id} id={String(log.id)}>
                <CommonTable.Cell>
                  <span className="text-xs text-default-500">{new Date(log.created_at).toLocaleString()}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <div className="flex items-center gap-1.5 min-w-0">
                    <span className="truncate text-sm">{log.user_email || `#${log.user_id}`}</span>
                    {log.user_status === 'disabled' ? (
                      <Chip color="danger" size="sm">{t('risk_control.banned')}</Chip>
                    ) : null}
                  </div>
                </CommonTable.Cell>
                <CommonTable.Cell><span className="text-sm">{log.model || '-'}</span></CommonTable.Cell>
                <CommonTable.Cell>
                  <Chip color={actionChipColor(log)} size="sm">
                    {t(`risk_control.action_${log.action}`, log.action)}
                  </Chip>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="text-sm">
                    {log.matched_keyword
                      ? log.matched_keyword
                      : log.highest_category
                        ? `${log.highest_category} / ${log.highest_score.toFixed(3)}`
                        : '-'}
                  </span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="text-xs text-default-500 line-clamp-2 break-all">{log.input_excerpt || '-'}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <div className="flex items-center gap-1">
                    <Button size="sm" variant="ghost" onPress={() => setDetailLog(log)}>
                      {t('risk_control.detail')}
                    </Button>
                    {log.user_status === 'disabled' ? (
                      <Button
                        isIconOnly
                        aria-label={t('risk_control.unban')}
                        size="sm"
                        variant="ghost"
                        onPress={() => setUnbanTarget(log)}
                      >
                        <Unlock className="w-3.5 h-3.5" />
                      </Button>
                    ) : null}
                  </div>
                </CommonTable.Cell>
              </CommonTable.Row>
            ))
          )}
        </CommonTable.Body>
      </CommonTable>

      <TablePaginationFooter
        page={page}
        pageSize={pageSize}
        setPage={setPage}
        setPageSize={setPageSize}
        total={total}
        totalPages={totalPages}
      />

      <ConfigModal config={config ?? null} open={showConfig} onClose={() => setShowConfig(false)} />
      <LogDetailModal log={detailLog} open={!!detailLog} onClose={() => setDetailLog(null)} />

      <ConfirmDialog
        confirmVariant="primary"
        description={t('risk_control.unban_confirm', { email: unbanTarget?.user_email || `#${unbanTarget?.user_id}` })}
        loading={unbanMutation.isPending}
        open={!!unbanTarget}
        status="warning"
        title={t('risk_control.unban')}
        onConfirm={() => unbanTarget && unbanMutation.mutate(unbanTarget.user_id)}
        onOpenChange={(nextOpen) => { if (!nextOpen) setUnbanTarget(null); }}
      />
      <ConfirmDialog
        description={
          <div className="space-y-3">
            <div>{t('risk_control.clear_logs_confirm')}</div>
            <Select
              fullWidth
              selectedKey={clearLogsType}
              onSelectionChange={(key) => setClearLogsType(key == null ? '' : (String(key) as ResultFilter))}
            >
              <Label className="sr-only">{t('risk_control.clear_logs_type')}</Label>
              <Select.Trigger>
                <Select.Value>{resultOptions.find((o) => o.id === clearLogsType)?.label}</Select.Value>
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover>
                <ListBox items={resultOptions}>
                  {(item) => <ListBox.Item id={item.id} textValue={item.label}>{item.label}</ListBox.Item>}
                </ListBox>
              </Select.Popover>
            </Select>
          </div>
        }
        loading={clearLogsMutation.isPending}
        open={confirmClearLogs}
        title={t('risk_control.clear_logs')}
        onConfirm={() => clearLogsMutation.mutate(clearLogsType)}
        onOpenChange={(nextOpen) => { if (!nextOpen) setConfirmClearLogs(false); }}
      />
      <ConfirmDialog
        description={t('risk_control.clear_hashes_confirm')}
        loading={clearHashesMutation.isPending}
        open={confirmClearHashes}
        title={t('risk_control.clear_hashes')}
        onConfirm={() => clearHashesMutation.mutate(undefined)}
        onOpenChange={(nextOpen) => { if (!nextOpen) setConfirmClearHashes(false); }}
      />
    </div>
  );
}
