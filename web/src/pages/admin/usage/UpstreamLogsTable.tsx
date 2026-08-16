import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Chip, EmptyState, Tooltip } from '@heroui/react';
import { Inbox } from 'lucide-react';
import { upstreamLogsApi } from '../../../shared/api/upstreamLogs';
import { queryKeys } from '../../../shared/queryKeys';
import { usePagination } from '../../../shared/hooks/usePagination';
import { getTotalPages } from '../../../shared/utils/pagination';
import { formatDate, formatTime } from '../../../shared/utils/format';
import { TablePaginationFooter } from '../../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../../shared/components/TableLoadingRow';
import { CommonTable } from '../../../shared/components/CommonTable';
import type { UpstreamAttemptHop, UpstreamLogQuery, UpstreamLogResp } from '../../../shared/types';

/** 失败请求表（管理端使用记录页「失败」Tab 主体：用户转发失败 + 渠道测试失败）。 */
export function UpstreamLogsTable({
  filters,
}: {
  /** 与消费记录 Tab 共享的筛选（时间范围/模型/用户/Key） */
  filters: {
    start_date?: string;
    end_date?: string;
    model?: string;
    user_id?: number;
    api_key_id?: number;
  };
}) {
  const { t } = useTranslation();
  const { page, setPage, pageSize, setPageSize } = usePagination(20, 'admin.upstream-logs');

  // 共享筛选来自父组件，变更时重置本表分页——否则停在第 N 页改筛选会看到误导性空表。
  const filterKey = JSON.stringify(filters);
  useEffect(() => {
    setPage(1);
  }, [filterKey, setPage]);

  const queryParams: Partial<UpstreamLogQuery> = {
    page,
    page_size: pageSize,
    ...filters,
  };
  const { data, isLoading } = useQuery({
    queryKey: queryKeys.adminUpstreamLogs(queryParams),
    queryFn: () => upstreamLogsApi.list(queryParams),
    placeholderData: keepPreviousData,
  });

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);

  return (
    <div>
      <CommonTable
        ariaLabel={t('usage.records_tab_upstream')}
        // 固定布局：原因列文本极长，auto 布局会挤扁来源/结果等窄列（chip 竖排）
        contentStyle={{ tableLayout: 'fixed' }}
        mobileLayout="cards"
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
        minWidth={1280}
      >
        <CommonTable.Header>
          <CommonTable.Column id="created_at" style={{ width: 130 }}>{t('usage.time')}</CommonTable.Column>
          <CommonTable.Column id="status" style={{ width: 170 }}>{t('upstream_logs.outcome')}</CommonTable.Column>
          <CommonTable.Column id="model" style={{ width: 160 }}>{t('usage.model')}</CommonTable.Column>
          <CommonTable.Column id="user" style={{ width: 150 }}>{t('usage.user')}</CommonTable.Column>
          <CommonTable.Column id="channel" style={{ width: 150 }}>{t('usage.channel_or_account')}</CommonTable.Column>
          <CommonTable.Column id="client" style={{ width: 140 }}>{t('usage.client')}</CommonTable.Column>
          <CommonTable.Column id="message">{t('upstream_logs.message')}</CommonTable.Column>
          <CommonTable.Column id="duration" style={{ width: 80 }}>{t('usage.duration')}</CommonTable.Column>
        </CommonTable.Header>
        <CommonTable.Body>
          {isLoading ? (
            <TableLoadingRow colSpan={8} />
          ) : rows.length === 0 ? (
            <CommonTable.Row id="empty">
              <CommonTable.Cell colSpan={8}>
                <EmptyState>
                  <div className="flex flex-col items-center gap-2 py-4">
                    <Inbox className="h-5 w-5 text-text-tertiary" />
                    <div className="text-sm text-default-500">{t('upstream_logs.empty')}</div>
                  </div>
                </EmptyState>
              </CommonTable.Cell>
            </CommonTable.Row>
          ) : (
            rows.map((row) => <LogRow key={row.id} row={row} />)
          )}
        </CommonTable.Body>
      </CommonTable>
    </div>
  );
}

function LogRow({ row }: { row: UpstreamLogResp }) {
  const { t } = useTranslation();
  const date = new Date(row.created_at);

  return (
    <CommonTable.Row id={String(row.id)}>
      <CommonTable.Cell>
        <div className="font-mono text-xs leading-tight" title={row.request_id ? `request_id: ${row.request_id}` : undefined}>
          <div className="text-text">{formatTime(date)}</div>
          <div className="text-text-tertiary">{formatDate(date)}</div>
        </div>
      </CommonTable.Cell>
      <CommonTable.Cell>
        <div className="flex items-center gap-1.5">
          <Chip color="danger" size="sm" variant="soft">
            {row.phase ? t(`upstream_logs.phase_${row.phase}`, row.phase) : t('upstream_logs.outcome_failure')}
          </Chip>
          {row.status_code > 0 && (
            <span className="font-mono text-xs text-text-tertiary">{row.status_code}</span>
          )}
          {row.billed && (
            <span className="font-mono text-[10px] text-warning" title={t('upstream_logs.billed_hint')}>$</span>
          )}
          {row.repeat_count > 1 && (
            <Chip
              color="warning"
              size="sm"
              title={t('upstream_logs.repeat_hint', { count: row.repeat_count })}
              variant="soft"
            >
              ×{row.repeat_count}
            </Chip>
          )}
        </div>
      </CommonTable.Cell>
      <CommonTable.Cell>
        <div className="min-w-0">
          <span className="block truncate font-mono text-xs text-text" title={row.model}>{row.model || '-'}</span>
          {row.endpoint && (
            <span className="block truncate font-mono text-[11px] text-text-tertiary" title={row.endpoint}>
              {row.endpoint}
            </span>
          )}
        </div>
      </CommonTable.Cell>
      <CommonTable.Cell>
        {row.source === 'channel_test' ? (
          <Chip color="accent" size="sm" variant="soft">
            {t('upstream_logs.source_channel_test')}
          </Chip>
        ) : (
          <span className="block truncate text-xs text-text-secondary" title={row.user_email}>
            {row.user_email || (row.user_id ? `#${row.user_id}` : '-')}
          </span>
        )}
      </CommonTable.Cell>
      <CommonTable.Cell>
        <ChannelCell row={row} />
      </CommonTable.Cell>
      <CommonTable.Cell>
        <ClientCell row={row} />
      </CommonTable.Cell>
      <CommonTable.Cell>
        <Tooltip delay={140} closeDelay={0}>
          <Tooltip.Trigger className="block w-full cursor-default text-left">
            <span className="block truncate text-xs text-text-secondary">{row.message || '-'}</span>
          </Tooltip.Trigger>
          <Tooltip.Content className="max-w-[26rem] whitespace-pre-wrap break-all border border-border bg-surface p-2.5 text-xs shadow-lg">
            <div className="space-y-1">
              <div className="text-text">{row.message}</div>
              {row.request_id && (
                <div className="font-mono text-[11px] text-text-tertiary">request_id: {row.request_id}</div>
              )}
            </div>
          </Tooltip.Content>
        </Tooltip>
      </CommonTable.Cell>
      <CommonTable.Cell>
        <span className="block text-center font-mono text-xs text-text-secondary">
          {row.duration_ms >= 1000 ? `${(row.duration_ms / 1000).toFixed(2)}s` : `${row.duration_ms}ms`}
        </span>
      </CommonTable.Cell>
    </CommonTable.Row>
  );
}

// ClientCell 客户端列：IP 主行 + UA 次行，hover 展示完整 IP / User-Agent（与成功表口径一致）。
function ClientCell({ row }: { row: UpstreamLogResp }) {
  const { t } = useTranslation();
  const ip = row.ip_address || '';
  const ua = row.user_agent || '';
  if (!ip && !ua) {
    return <span className="block text-xs text-text-tertiary">-</span>;
  }
  return (
    <Tooltip delay={140} closeDelay={0}>
      <Tooltip.Trigger className="block w-full cursor-default text-left">
        <div className="min-w-0">
          <span className="block truncate font-mono text-xs text-text-secondary">{ip || '-'}</span>
          {ua && <span className="block truncate text-[11px] leading-tight text-text-tertiary">{ua}</span>}
        </div>
      </Tooltip.Trigger>
      <Tooltip.Content className="max-w-[26rem] border border-border bg-surface p-2.5 text-xs shadow-lg">
        <div className="space-y-1">
          <div className="font-mono text-text">{t('usage.client_ip')}: {ip || '-'}</div>
          <div className="break-all font-mono text-text-secondary">{ua || '-'}</div>
        </div>
      </Tooltip.Content>
    </Tooltip>
  );
}

function hopRouteLabel(hop: UpstreamAttemptHop, t: ReturnType<typeof useTranslation>['t']) {
  if (hop.account_id || hop.account_name) {
    return hop.account_name || `#${hop.account_id}`;
  }
  const channelName = hop.channel_name || t('upstream_logs.channel_fallback', { id: hop.channel_id });
  const keyName = hop.channel_key_name || t('channels.key_unnamed');
  return `${channelName} · ${keyName}`;
}

// ChannelCell 路由列：按每一跳的实际类型展示渠道密钥或账号，多跳时用箭头串联并支持展开明细。
function ChannelCell({ row }: { row: UpstreamLogResp }) {
  const { t } = useTranslation();
  const chain = row.attempt_chain ?? [];
  const label = chain.length > 0
    ? chain.map((hop) => hopRouteLabel(hop, t)).join(' → ')
    : row.account_name
      || (row.account_id ? `#${row.account_id}` : '')
      || row.channel_name
      || (row.channel_id ? `#${row.channel_id}` : '-');

  if (chain.length === 0) {
    return <span className="block truncate text-xs text-text-secondary" title={label}>{label}</span>;
  }

  return (
    <Tooltip delay={140} closeDelay={0}>
      <Tooltip.Trigger className="block w-full cursor-default text-left">
        <span className="block truncate text-xs text-text-secondary">{label}</span>
      </Tooltip.Trigger>
      <Tooltip.Content className="max-w-[28rem] border border-border bg-surface p-2.5 text-xs shadow-lg">
        <div className="space-y-1.5">
          <div className="font-medium text-text">{t('upstream_logs.retry_chain')}</div>
          {chain.map((hop) => <HopLine key={hop.seq} hop={hop} />)}
        </div>
      </Tooltip.Content>
    </Tooltip>
  );
}

function HopLine({ hop }: { hop: UpstreamAttemptHop }) {
  const { t } = useTranslation();
  return (
    <div className="rounded-[var(--radius)] bg-bg-hover px-2 py-1 font-mono text-[11px] leading-relaxed">
      <span className="text-text">
        #{hop.seq} {hopRouteLabel(hop, t)}
        {!hop.account_id && !hop.account_name && hop.key_hint ? ` (${hop.key_hint})` : ''}
      </span>
      <span className="text-text-tertiary">
        {' '}· {hop.verdict}
        {hop.upstream_status ? ` · HTTP ${hop.upstream_status}` : ''}
        {typeof hop.latency_ms === 'number' && hop.latency_ms > 0 ? ` · ${hop.latency_ms}ms` : ''}
        {hop.auto_disabled ? ` · ${t('upstream_logs.auto_disabled')}` : ''}
      </span>
      {hop.reason && <div className="break-all text-text-secondary">{hop.reason}</div>}
    </div>
  );
}
