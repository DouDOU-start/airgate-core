import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Chip, EmptyState, Tooltip } from '@heroui/react';
import { Inbox } from 'lucide-react';
import { upstreamLogsApi } from '../../shared/api/upstreamLogs';
import { queryKeys } from '../../shared/queryKeys';
import { usePagination } from '../../shared/hooks/usePagination';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { CommonTable } from '../../shared/components/CommonTable';
import { formatDate, formatTime } from '../../shared/utils/format';
import type { UserUpstreamLogResp } from '../../shared/types';

// 有操作建议（CTA 文案）的失败阶段；缺失时只显示错误原因。
const CTA_PHASES = new Set([
  'precheck_balance', 'precheck_price', 'local_limit', 'queue_timeout',
  'upstream_exhausted', 'upstream_client_error', 'canceled', 'stream_aborted',
]);

/** 用户视角失败请求表（后端已脱敏：无渠道/重试链/IP，原因附操作建议）。 */
export function UserUpstreamLogsTable({
  filters,
}: {
  /** 与消费记录 Tab 共享的筛选（时间范围/模型/Key） */
  filters: {
    start_date?: string;
    end_date?: string;
    model?: string;
    api_key_id?: number;
  };
}) {
  const { t } = useTranslation();
  const { page, setPage, pageSize, setPageSize } = usePagination(20, 'user.upstream-logs');

  // 共享筛选来自父组件，变更时重置本表分页——否则停在第 N 页改筛选会看到误导性空表。
  const filterKey = JSON.stringify(filters);
  useEffect(() => {
    setPage(1);
  }, [filterKey, setPage]);

  const queryParams = { page, page_size: pageSize, ...filters };
  const { data, isLoading } = useQuery({
    queryKey: queryKeys.userUpstreamLogs(queryParams),
    queryFn: () => upstreamLogsApi.userList(queryParams),
    placeholderData: keepPreviousData,
  });

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);

  return (
    <CommonTable
      ariaLabel={t('usage.records_tab_upstream')}
      contentStyle={{ tableLayout: 'fixed' }}
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
      minWidth={860}
    >
      <CommonTable.Header>
        <CommonTable.Column id="created_at" style={{ width: 130 }}>{t('usage.time')}</CommonTable.Column>
        <CommonTable.Column id="status" style={{ width: 170 }}>{t('upstream_logs.outcome')}</CommonTable.Column>
        <CommonTable.Column id="model" style={{ width: 170 }}>{t('usage.model')}</CommonTable.Column>
        <CommonTable.Column id="message">{t('upstream_logs.message')}</CommonTable.Column>
        <CommonTable.Column id="duration" style={{ width: 80 }}>{t('usage.duration')}</CommonTable.Column>
      </CommonTable.Header>
      <CommonTable.Body>
        {isLoading ? (
          <TableLoadingRow colSpan={5} />
        ) : rows.length === 0 ? (
          <CommonTable.Row id="empty">
            <CommonTable.Cell colSpan={5}>
              <EmptyState>
                <div className="flex flex-col items-center gap-2 py-4">
                  <Inbox className="h-5 w-5 text-text-tertiary" />
                  <div className="text-sm text-default-500">{t('upstream_logs.user_empty')}</div>
                </div>
              </EmptyState>
            </CommonTable.Cell>
          </CommonTable.Row>
        ) : (
          rows.map((row) => <LogRow key={row.id} row={row} />)
        )}
      </CommonTable.Body>
    </CommonTable>
  );
}

function LogRow({ row }: { row: UserUpstreamLogResp }) {
  const { t } = useTranslation();
  const date = new Date(row.created_at);
  const cta = row.phase && CTA_PHASES.has(row.phase) ? t(`upstream_logs.cta_${row.phase}`) : '';

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
        <Tooltip delay={140} closeDelay={0}>
          <Tooltip.Trigger className="block w-full cursor-default text-left">
            <span className="block truncate text-xs text-text-secondary">{row.message || '-'}</span>
          </Tooltip.Trigger>
          <Tooltip.Content className="max-w-[26rem] whitespace-pre-wrap break-all border border-border bg-surface p-2.5 text-xs shadow-lg">
            <div className="space-y-1">
              <div className="text-text">{row.message}</div>
              {cta && <div className="font-medium text-warning">{cta}</div>}
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
