import { useEffect, useMemo, useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Button, ListBox, Select } from '@heroui/react';
import {
  Activity,
  ArrowRight,
  Braces,
  Check,
  ChevronRight,
  Clipboard,
  FileSearch,
  FilterX,
  KeyRound,
  ListFilter,
  Route,
  Search,
  Server,
  ShieldAlert,
  ShieldCheck,
  Trash2,
  UserRound,
  X,
} from 'lucide-react';

import { requestAuditsApi, type RequestAuditClearMode } from '../../shared/api/requestAudits';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';
import { RefreshButton } from '../../shared/components/RefreshButton';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { formatDateTime } from '../../shared/utils/format';
import type {
  RequestAuditAttempt,
  RequestAuditListItem,
  RequestAuditPayload,
} from '../../shared/types';

const CLEAR_MODE_OPTIONS: Array<{ id: RequestAuditClearMode; label: string; desc: string }> = [
  { id: 'older_than_24h', label: '仅 24 小时前', desc: '保留最近 24 小时的记录，删除更早的数据' },
  { id: 'all', label: '全部清空', desc: '删除全部请求审计记录，不可恢复' },
];

const STATUS_OPTIONS = [
  { id: '', label: '全部状态' },
  { id: '200', label: '200 成功' },
  { id: '400', label: '400 请求错误' },
  { id: '401', label: '401 未授权' },
  { id: '403', label: '403 禁止访问' },
  { id: '429', label: '包含 429 尝试' },
  { id: '499', label: '499 用户中断' },
  { id: '500', label: '500 服务错误' },
  { id: '503', label: '503 上游不可用' },
];

function statusOptionDotTone(status: string): string {
  if (status === '200') return 'bg-success';
  if (status === '429') return 'bg-warning';
  if (status) return 'bg-danger';
  return 'bg-text-tertiary';
}

function statusTone(status: number): string {
  if (status >= 200 && status < 300) return 'border-success/30 bg-success/10 text-success';
  if (status === 429) return 'border-warning/30 bg-warning/10 text-warning';
  if (status >= 400) return 'border-danger/30 bg-danger/10 text-danger';
  return 'border-border bg-surface-secondary text-text-tertiary';
}

function verdictLabel(verdict: string): string {
  const labels: Record<string, string> = {
    success: '成功',
    rateLimited: '429 限流',
    authFailed: '认证失败',
    transient: '上游暂时故障',
    networkError: '网络错误',
    clientError: '请求错误',
    streamAborted: '流中断',
  };
  return labels[verdict] ?? (verdict || '处理中');
}

function routeLabel(attempt: RequestAuditAttempt): string {
  if (attempt.route_kind === 'account') {
    return attempt.account_email || attempt.account_name || `账号 #${attempt.account_id}`;
  }
  const channel = attempt.channel_name || `渠道 #${attempt.channel_id}`;
  return attempt.channel_key_name ? `${channel} · ${attempt.channel_key_name}` : channel;
}

function prettyPayload(payload?: RequestAuditPayload): string {
  if (!payload) return '';
  if (payload.encoding === 'base64') return payload.content;
  try {
    return JSON.stringify(JSON.parse(payload.content), null, 2);
  } catch {
    return payload.content;
  }
}

function CodePanel({ title, payload, empty = '无内容' }: {
  title: string;
  payload?: RequestAuditPayload;
  empty?: string;
}) {
  const [copied, setCopied] = useState(false);
  const content = useMemo(() => prettyPayload(payload), [payload]);

  const copy = async () => {
    if (!content) return;
    await navigator.clipboard.writeText(content);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1200);
  };

  return (
    <section className="min-w-0 overflow-hidden rounded-xl border border-border bg-[#0b1118] shadow-[0_18px_50px_rgba(0,0,0,0.16)]">
      <div className="flex h-10 items-center justify-between border-b border-white/10 px-3">
        <div className="flex min-w-0 items-center gap-2">
          <Braces className="h-3.5 w-3.5 shrink-0 text-cyan-400" />
          <span className="truncate font-mono text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-300">{title}</span>
          {payload ? <span className="font-mono text-[10px] text-slate-500">{payload.bytes.toLocaleString()} B</span> : null}
        </div>
        <button
          className="inline-flex h-7 items-center gap-1 rounded-md px-2 text-[11px] text-slate-400 transition hover:bg-white/10 hover:text-white disabled:opacity-40"
          disabled={!content}
          type="button"
          onClick={() => void copy()}
        >
          {copied ? <Check className="h-3 w-3 text-emerald-400" /> : <Clipboard className="h-3 w-3" />}
          {copied ? '已复制' : '复制'}
        </button>
      </div>
      <pre className="h-[300px] overflow-auto whitespace-pre-wrap break-all p-3 font-mono text-[11px] leading-5 text-slate-300">
        {content || <span className="text-slate-600">{empty}</span>}
      </pre>
    </section>
  );
}

function Metric({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="min-w-0 rounded-xl border border-border-subtle bg-surface-secondary/45 px-3 py-2.5">
      <div className="text-[10px] font-medium uppercase tracking-[0.14em] text-text-tertiary">{label}</div>
      <div className="mt-1.5 truncate font-mono text-xs font-semibold text-text" title={String(value)}>{value}</div>
    </div>
  );
}

function AuditRow({ row, active, onSelect }: {
  row: RequestAuditListItem;
  active: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      className={`group relative grid w-full grid-cols-[78px_minmax(0,1fr)_76px_20px] items-center gap-3 border-b border-border-subtle px-4 py-3.5 text-left transition duration-200 ${active ? 'bg-[linear-gradient(90deg,var(--ag-primary-subtle),transparent_78%)]' : 'hover:bg-surface-secondary/55'}`}
      type="button"
      onClick={onSelect}
    >
      <span className={`absolute inset-y-2 left-0 w-[3px] rounded-r-full bg-accent transition-opacity ${active ? 'opacity-100' : 'opacity-0'}`} />
      <div>
        <div className="font-mono text-[11px] font-semibold text-text">{new Date(row.created_at).toLocaleTimeString('zh-CN', { hour12: false })}</div>
        <div className="mt-0.5 text-[10px] text-text-tertiary">{new Date(row.created_at).toLocaleDateString('zh-CN')}</div>
      </div>
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-2">
          <div className="truncate font-mono text-xs font-semibold text-text" title={row.model}>{row.model}</div>
          <span className="shrink-0 rounded-full bg-surface-tertiary/70 px-1.5 py-0.5 font-mono text-[9px] text-text-tertiary">{row.attempt_count} 次</span>
        </div>
        <div className="mt-1 truncate font-mono text-[10px] text-text-tertiary" title={row.request_id}>{row.request_id || `审计 #${row.id}`}</div>
        <div className="mt-1.5 flex min-w-0 items-center gap-3 text-[10px] text-text-secondary">
          <span className="flex min-w-0 items-center gap-1">
            <UserRound className="h-3 w-3 shrink-0 text-text-tertiary" />
            <span className="truncate" title={row.user_email}>{row.user_email || `用户 #${row.user_id}`}</span>
          </span>
          <span className="flex min-w-0 items-center gap-1">
            <Route className="h-3 w-3 shrink-0 text-text-tertiary" />
            <span className="truncate" title={row.routes.join(' → ')}>{row.routes.join(' → ') || '未触达上游'}</span>
          </span>
        </div>
      </div>
      <div className="text-right">
        <span className={`inline-flex min-w-12 justify-center rounded-lg border px-2 py-1 font-mono text-[11px] font-bold ${statusTone(row.status_code)}`}>
          {row.status_code || '—'}
        </span>
      </div>
      <ChevronRight className={`h-4 w-4 justify-self-end transition ${active ? 'translate-x-0.5 text-accent' : 'text-text-tertiary group-hover:translate-x-0.5 group-hover:text-text'}`} />
    </button>
  );
}

function ListLoadingState() {
  return (
    <div aria-label="正在读取审计索引" className="divide-y divide-border-subtle">
      {Array.from({ length: 7 }, (_, index) => (
        <div key={index} className="grid grid-cols-[78px_minmax(0,1fr)_76px] gap-3 px-4 py-4">
          <div className="space-y-2">
            <div className="h-3 w-12 animate-pulse rounded-full bg-surface-tertiary" />
            <div className="h-2.5 w-16 animate-pulse rounded-full bg-surface-secondary" />
          </div>
          <div className="space-y-2">
            <div className="h-3 w-1/3 animate-pulse rounded-full bg-surface-tertiary" />
            <div className="h-2.5 w-2/3 animate-pulse rounded-full bg-surface-secondary" />
            <div className="h-2.5 w-1/2 animate-pulse rounded-full bg-surface-secondary" />
          </div>
          <div className="h-7 w-12 animate-pulse justify-self-end rounded-lg bg-surface-tertiary" />
        </div>
      ))}
    </div>
  );
}

function DetailEmptyState() {
  return (
    <div className="relative flex h-full min-h-[520px] items-center justify-center overflow-hidden px-6 py-14">
      <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_50%_42%,var(--ag-primary-subtle),transparent_34%)] opacity-70" />
      <div className="relative max-w-xl text-center">
        <div className="mx-auto mb-8 flex items-center justify-center">
          <div className="flex h-14 w-14 items-center justify-center rounded-2xl border border-border bg-surface shadow-sm">
            <FileSearch className="h-6 w-6 text-accent" />
          </div>
          <div className="relative mx-2 h-px w-14 bg-border sm:mx-3 sm:w-20">
            <span className="absolute left-1/2 top-1/2 h-2 w-2 -translate-x-1/2 -translate-y-1/2 rounded-full bg-accent shadow-[0_0_0_4px_var(--ag-primary-subtle)]" />
          </div>
          <div className="flex h-14 w-14 items-center justify-center rounded-2xl border border-border bg-surface shadow-sm">
            <Route className="h-6 w-6 text-secondary" />
          </div>
          <div className="relative mx-2 h-px w-14 bg-border sm:mx-3 sm:w-20">
            <ArrowRight className="absolute right-0 top-1/2 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
          </div>
          <div className="flex h-14 w-14 items-center justify-center rounded-2xl border border-border bg-surface shadow-sm">
            <Server className="h-6 w-6 text-success" />
          </div>
        </div>
        <h2 className="text-lg font-semibold tracking-tight text-text">选择一条请求，查看完整调用链路</h2>
        <p className="mx-auto mt-2 max-w-md text-sm leading-6 text-text-secondary">
          这里会并排展示客户端原始报文、网关调度路径和每次上游发包，方便快速定位认证、限流与重试问题。
        </p>
        <div className="mx-auto mt-6 inline-flex items-center gap-2 rounded-full border border-border-subtle bg-surface-secondary/60 px-3 py-1.5 text-[11px] text-text-tertiary">
          <ShieldCheck className="h-3.5 w-3.5 text-accent" />
          敏感内容仅在当前管理员会话内解密展示
        </div>
      </div>
    </div>
  );
}

export default function RequestAuditsPage() {
  const { page, setPage, pageSize, setPageSize } = usePagination(15, 'admin-request-audits');
  const [keyword, setKeyword] = useState('');
  const [model, setModel] = useState('');
  const [statusCode, setStatusCode] = useState('');
  const [selectedID, setSelectedID] = useState<number | null>(null);
  const [attemptSeq, setAttemptSeq] = useState(1);
  const [confirmClear, setConfirmClear] = useState(false);
  const [clearMode, setClearMode] = useState<RequestAuditClearMode>('older_than_24h');
  const debouncedKeyword = useDebouncedValue(keyword, 300);
  const debouncedModel = useDebouncedValue(model, 300);

  const listQueryKey = ['request-audits', page, pageSize, debouncedKeyword, debouncedModel, statusCode] as const;
  const listQuery = useQuery({
    queryKey: listQueryKey,
    queryFn: () => requestAuditsApi.list({
      page,
      page_size: pageSize,
      keyword: debouncedKeyword || undefined,
      model: debouncedModel || undefined,
      status_code: statusCode ? Number(statusCode) : undefined,
    }),
    placeholderData: keepPreviousData,
    refetchInterval: 15_000,
    meta: { globalLoading: false },
  });

  const clearMutation = useCrudMutation({
    mutationFn: (mode: RequestAuditClearMode) => requestAuditsApi.clear(mode),
    successMessage: '请求审计已清空',
    queryKey: listQueryKey,
    onSuccess: () => {
      setConfirmClear(false);
      setSelectedID(null);
      setPage(1);
    },
  });

  const rows = listQuery.data?.list ?? [];
  useEffect(() => {
    if (rows.length === 0) {
      setSelectedID(null);
      return;
    }
    if (selectedID == null || !rows.some((row) => row.id === selectedID)) {
      setSelectedID(rows[0]?.id ?? null);
    }
  }, [rows, selectedID]);

  const detailQuery = useQuery({
    queryKey: ['request-audit-detail', selectedID],
    queryFn: () => requestAuditsApi.get(selectedID as number),
    enabled: selectedID != null,
    refetchOnWindowFocus: false,
    meta: { globalLoading: false },
  });
  const detail = detailQuery.data;

  useEffect(() => {
    setAttemptSeq(detail?.attempts[0]?.seq ?? 1);
  }, [detail?.id, detail?.attempts]);

  const selectedAttempt = detail?.attempts.find((attempt) => attempt.seq === attemptSeq)
    ?? detail?.attempts[0];
  const total = listQuery.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const hasFilters = Boolean(keyword || model || statusCode);
  const selectedStatusLabel = STATUS_OPTIONS.find((item) => item.id === statusCode)?.label ?? '全部状态';

  const clearFilters = () => {
    setKeyword('');
    setModel('');
    setStatusCode('');
    setPage(1);
  };

  return (
    <div className="space-y-3">
      <section aria-label="请求审计筛选" className="overflow-hidden rounded-2xl border border-border bg-surface shadow-sm">
        <div className="grid md:grid-cols-2 xl:grid-cols-[minmax(320px,1.45fr)_minmax(220px,0.85fr)_190px_64px]">
          <div className="group relative flex h-14 min-w-0 items-center gap-3 border-b border-border-subtle px-4 transition focus-within:bg-accent-soft/35 md:col-span-2 xl:col-span-1 xl:border-b-0 xl:border-r">
            <Search className="h-4 w-4 shrink-0 text-text-tertiary transition group-focus-within:text-accent" />
            <div className="min-w-0 flex-1">
              <label className="block text-[9px] font-semibold uppercase tracking-[0.16em] text-text-tertiary" htmlFor="audit-search">全文检索</label>
              <input
                id="audit-search"
                className="mt-0.5 w-full bg-transparent text-sm text-text outline-none placeholder:text-field-placeholder"
                placeholder="Request ID、用户邮箱、账号或渠道"
                value={keyword}
                onChange={(event) => { setKeyword(event.target.value); setPage(1); }}
              />
            </div>
            {keyword ? (
              <button aria-label="清除搜索内容" className="rounded-md p-1 text-text-tertiary transition hover:bg-surface-secondary hover:text-text" type="button" onClick={() => { setKeyword(''); setPage(1); }}>
                <X className="h-3.5 w-3.5" />
              </button>
            ) : null}
          </div>

          <div className="group relative flex h-14 min-w-0 items-center gap-3 border-b border-border-subtle px-4 transition focus-within:bg-accent-soft/35 md:border-r xl:border-b-0">
            <KeyRound className="h-4 w-4 shrink-0 text-text-tertiary transition group-focus-within:text-accent" />
            <div className="min-w-0 flex-1">
              <label className="block text-[9px] font-semibold uppercase tracking-[0.16em] text-text-tertiary" htmlFor="audit-model">模型</label>
              <input
                id="audit-model"
                className="mt-0.5 w-full bg-transparent text-sm text-text outline-none placeholder:text-field-placeholder"
                placeholder="例如 claude-sonnet-4"
                value={model}
                onChange={(event) => { setModel(event.target.value); setPage(1); }}
              />
            </div>
            {model ? (
              <button aria-label="清除模型筛选" className="rounded-md p-1 text-text-tertiary transition hover:bg-surface-secondary hover:text-text" type="button" onClick={() => { setModel(''); setPage(1); }}>
                <X className="h-3.5 w-3.5" />
              </button>
            ) : null}
          </div>

          <div className="relative flex h-14 items-center gap-3 border-b border-border-subtle px-4 md:border-r xl:border-b-0">
            <ListFilter className="h-4 w-4 shrink-0 text-text-tertiary" />
            <div className="min-w-0 flex-1">
              <span className="block text-[9px] font-semibold uppercase tracking-[0.16em] text-text-tertiary">响应状态</span>
              <Select
                aria-label="响应状态"
                className="ag-audit-status-select mt-0.5"
                fullWidth
                selectedKey={statusCode}
                onSelectionChange={(key) => {
                  setStatusCode(key == null ? '' : String(key));
                  setPage(1);
                }}
              >
                <Select.Trigger>
                  <Select.Value>{selectedStatusLabel}</Select.Value>
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover className="ag-audit-status-popover">
                  <ListBox items={STATUS_OPTIONS}>
                    {(item) => (
                      <ListBox.Item id={item.id} textValue={item.label}>
                        <span className="flex items-center gap-2">
                          <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${statusOptionDotTone(item.id)}`} />
                          <span>{item.label}</span>
                        </span>
                      </ListBox.Item>
                    )}
                  </ListBox>
                </Select.Popover>
              </Select>
            </div>
          </div>

          <div className="flex h-14 items-center justify-center gap-1 px-2">
            <Button
              aria-label="清空请求审计"
              className="min-w-0 px-2 text-text-tertiary hover:text-danger"
              isDisabled={total === 0 || clearMutation.isPending}
              size="sm"
              variant="ghost"
              onPress={() => {
                setClearMode('older_than_24h');
                setConfirmClear(true);
              }}
            >
              <Trash2 className="h-4 w-4" />
            </Button>
            <RefreshButton ariaLabel="刷新请求审计" isRefreshing={listQuery.isFetching} onRefresh={() => listQuery.refetch()} />
          </div>
        </div>

        <div className="flex min-h-8 flex-wrap items-center justify-between gap-2 border-t border-border-subtle bg-surface-secondary/35 px-4 py-1.5 text-[10px] text-text-tertiary">
          <span className="inline-flex items-center gap-2">
            <span className="relative flex h-2 w-2">
              <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-success opacity-40 motion-reduce:animate-none" />
              <span className="relative inline-flex h-2 w-2 rounded-full bg-success" />
            </span>
            每 15 秒自动刷新
          </span>
          <div className="flex items-center gap-3">
            <span>当前 {rows.length} 条 · 共 {total} 条</span>
            {hasFilters ? (
              <button className="inline-flex items-center gap-1 text-accent transition hover:text-accent-hover" type="button" onClick={clearFilters}>
                <FilterX className="h-3 w-3" />
                清除筛选
              </button>
            ) : null}
          </div>
        </div>
      </section>

      <div className="grid min-h-[700px] overflow-hidden rounded-2xl border border-border bg-surface shadow-lg xl:h-[calc(100vh-220px)] xl:min-h-[650px] xl:grid-cols-[minmax(520px,0.88fr)_minmax(0,1.12fr)]">
        <section className="flex min-w-0 flex-col border-b border-border xl:min-h-0 xl:border-b-0 xl:border-r">
          <div className="flex h-12 shrink-0 items-center justify-between border-b border-border-subtle bg-surface-secondary/45 px-4">
            <div className="flex items-center gap-2">
              <Activity className="h-4 w-4 text-accent" />
              <h2 className="text-sm font-semibold text-text">审计记录</h2>
            </div>
            <span className="rounded-full border border-border-subtle bg-surface px-2 py-1 font-mono text-[10px] text-text-tertiary">{total.toLocaleString()} 条</span>
          </div>
          <div className="grid shrink-0 grid-cols-[78px_minmax(0,1fr)_76px_20px] gap-3 border-b border-border-subtle px-4 py-2 font-mono text-[9px] uppercase tracking-[0.14em] text-text-tertiary">
            <span>时间</span><span>请求 / 路由</span><span className="text-right">结果</span><span />
          </div>
          <div className="min-h-[430px] flex-1 overflow-y-auto">
            {listQuery.isLoading ? (
              <ListLoadingState />
            ) : listQuery.isError ? (
              <div className="flex h-full min-h-[430px] flex-col items-center justify-center px-8 text-center">
                <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-danger/10 text-danger"><ShieldAlert className="h-5 w-5" /></div>
                <h3 className="mt-4 text-sm font-semibold text-text">审计记录加载失败</h3>
                <p className="mt-1 text-xs text-text-tertiary">请检查网络连接后重新刷新</p>
              </div>
            ) : rows.length === 0 ? (
              <div className="flex h-full min-h-[430px] flex-col items-center justify-center px-8 text-center">
                <div className="relative flex h-16 w-16 items-center justify-center rounded-2xl border border-border-subtle bg-surface-secondary/55 text-text-tertiary">
                  <FileSearch className="h-7 w-7" />
                  <span className="absolute -right-1 -top-1 h-3 w-3 rounded-full border-2 border-surface bg-accent" />
                </div>
                <h3 className="mt-5 text-sm font-semibold text-text">{hasFilters ? '没有匹配的审计记录' : '暂时还没有审计记录'}</h3>
                <p className="mt-1 max-w-xs text-xs leading-5 text-text-tertiary">
                  {hasFilters ? '可以减少筛选条件，或确认 Request ID、模型名称是否正确。' : '新的网关请求完成后，会自动出现在这里。'}
                </p>
                {hasFilters ? (
                  <button className="mt-4 inline-flex items-center gap-1.5 rounded-lg border border-border bg-surface px-3 py-2 text-xs font-medium text-text-secondary transition hover:border-accent/40 hover:text-accent" type="button" onClick={clearFilters}>
                    <FilterX className="h-3.5 w-3.5" />
                    清除全部筛选
                  </button>
                ) : null}
              </div>
            ) : rows.map((row) => (
              <AuditRow key={row.id} row={row} active={row.id === selectedID} onSelect={() => setSelectedID(row.id)} />
            ))}
          </div>
          <div className="shrink-0 border-t border-border-subtle bg-surface-secondary/25 p-3">
            <TablePaginationFooter
              page={page}
              pageSize={pageSize}
              setPage={setPage}
              setPageSize={setPageSize}
              total={total}
              totalPages={totalPages}
            />
          </div>
        </section>

        <section className="min-w-0 overflow-y-auto bg-[radial-gradient(circle_at_top_right,var(--ag-primary-subtle),transparent_30%)] xl:min-h-0">
          {!selectedID ? (
            <DetailEmptyState />
          ) : detailQuery.isLoading ? (
            <div className="flex h-full min-h-[520px] flex-col items-center justify-center text-sm text-text-tertiary">
              <div className="mb-4 flex h-12 w-12 items-center justify-center rounded-2xl border border-border bg-surface shadow-sm">
                <Activity className="h-5 w-5 animate-pulse text-accent" />
              </div>
              正在解密请求快照…
            </div>
          ) : !detail ? (
            <div className="flex h-full min-h-[520px] flex-col items-center justify-center text-sm text-danger">
              <div className="mb-4 flex h-12 w-12 items-center justify-center rounded-2xl bg-danger/10"><ShieldAlert className="h-5 w-5" /></div>
              详情读取失败
            </div>
          ) : (
            <div className="space-y-4 p-4 2xl:p-5">
              <div className="rounded-2xl border border-border bg-surface/85 p-4 shadow-sm backdrop-blur-sm">
                <div className="flex flex-wrap items-start justify-between gap-4">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className={`rounded-lg border px-2.5 py-1 font-mono text-xs font-bold ${statusTone(detail.status_code)}`}>{detail.status_code || '处理中'}</span>
                      <span className="font-mono text-sm font-semibold text-text">{detail.method} {detail.path}</span>
                      {detail.stream ? <span className="rounded-full bg-info-subtle px-2 py-1 text-[10px] font-semibold text-info">流式响应</span> : null}
                    </div>
                    <div className="mt-3 flex min-w-0 items-center gap-2 text-text-tertiary">
                      <KeyRound className="h-3.5 w-3.5 shrink-0" />
                      <span className="break-all font-mono text-[10px]">{detail.request_id}</span>
                    </div>
                  </div>
                  <div className="shrink-0 text-right text-xs text-text-tertiary">
                    <div>{formatDateTime(detail.created_at)}</div>
                    <div className="mt-1.5 inline-flex items-center gap-1.5 rounded-full bg-surface-secondary px-2 py-1 font-mono text-[10px] text-text-secondary">
                      总耗时 {detail.duration_ms.toLocaleString()} ms
                    </div>
                  </div>
                </div>

                <div className="mt-4 grid grid-cols-2 gap-2 sm:grid-cols-4">
                  <Metric label="用户" value={detail.user_email || `#${detail.user_id}`} />
                  <Metric label="API Key" value={`#${detail.api_key_id}`} />
                  <Metric label="客户端" value={detail.client || '普通客户端'} />
                  <Metric label="来源 IP" value={detail.ip_address || detail.remote_addr || '—'} />
                </div>
              </div>

              <section className="rounded-2xl border border-border bg-surface/75 p-4">
                <div className="mb-3 flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Route className="h-4 w-4 text-secondary" />
                    <h2 className="text-sm font-semibold text-text">实际上游尝试</h2>
                  </div>
                  <span className="font-mono text-[10px] text-text-tertiary">{detail.attempts.length} 次真实触网</span>
                </div>
                <div className="flex gap-2 overflow-x-auto pb-1">
                  {detail.attempts.length === 0 ? (
                    <div className="flex w-full items-center gap-2 rounded-xl border border-dashed border-border px-3 py-3 text-xs text-text-tertiary">
                      <ShieldCheck className="h-4 w-4" />
                      请求在触网上游前结束
                    </div>
                  ) : detail.attempts.map((attempt) => (
                    <button
                      key={attempt.id}
                      className={`group min-w-[190px] rounded-xl border px-3 py-2.5 text-left transition ${attempt.seq === selectedAttempt?.seq ? 'border-accent bg-accent-soft shadow-sm' : 'border-border-subtle bg-surface-secondary/35 hover:border-accent/40 hover:bg-surface-secondary/65'}`}
                      type="button"
                      onClick={() => setAttemptSeq(attempt.seq)}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <span className="font-mono text-[10px] font-bold uppercase tracking-[0.1em] text-text-tertiary">尝试 {attempt.seq}</span>
                        <span className={`rounded-md px-1.5 py-0.5 font-mono text-[10px] font-bold ${attempt.status_code === 429 ? 'bg-warning/10 text-warning' : attempt.status_code >= 400 ? 'bg-danger/10 text-danger' : 'bg-success/10 text-success'}`}>{attempt.status_code || '错误'}</span>
                      </div>
                      <div className="mt-2 truncate text-xs font-semibold text-text" title={routeLabel(attempt)}>{routeLabel(attempt)}</div>
                      <div className="mt-1.5 flex items-center justify-between text-[10px] text-text-tertiary">
                        <span>{verdictLabel(attempt.verdict)}</span>
                        <span className="font-mono">{attempt.latency_ms} ms</span>
                      </div>
                    </button>
                  ))}
                </div>
              </section>

              {selectedAttempt ? (
                <div className="space-y-3 rounded-2xl border border-border bg-surface-secondary/35 p-4">
                  <div className="grid gap-2 sm:grid-cols-4">
                    <Metric label="调度类型" value={selectedAttempt.route_kind === 'account' ? 'OAuth / 账号' : '渠道密钥'} />
                    <Metric label="账号或渠道" value={routeLabel(selectedAttempt)} />
                    <Metric label="首字耗时" value={selectedAttempt.first_token_ms ? `${selectedAttempt.first_token_ms} ms` : '—'} />
                    <Metric label="流完成" value={selectedAttempt.stream_completed ? '是' : '否'} />
                  </div>
                  <div className="rounded-xl border border-border-subtle bg-surface px-3 py-3">
                    <div className="flex items-center gap-2 text-[10px] font-medium uppercase tracking-[0.12em] text-text-tertiary">
                      <Server className="h-3.5 w-3.5" />
                      实际上游 URL
                    </div>
                    <div className="mt-2 break-all font-mono text-[11px] leading-5 text-text">{selectedAttempt.upstream_url.content || '—'}</div>
                    {selectedAttempt.reason ? <div className="mt-3 border-t border-border-subtle pt-3 text-xs text-danger">{selectedAttempt.reason}</div> : null}
                  </div>
                </div>
              ) : null}

              <div className="grid gap-3 2xl:grid-cols-2">
                <CodePanel title="客户端原始 Header" payload={detail.inbound_headers} />
                <CodePanel title={`上游 Header · Attempt ${selectedAttempt?.seq ?? '—'}`} payload={selectedAttempt?.headers} />
                <CodePanel title="客户端原始 Body" payload={detail.inbound_body} />
                <CodePanel title={`上游 Body · Attempt ${selectedAttempt?.seq ?? '—'}`} payload={selectedAttempt?.body} />
              </div>

              <div className="flex items-center gap-2 rounded-xl border border-info/20 bg-info-subtle px-3 py-2.5 text-[11px] text-text-secondary">
                <ShieldAlert className="h-4 w-4 shrink-0 text-info" />
                base64 图片已替换为长度与 SHA-256 占位符；其他 Header 和 Body 字段按真实内容解密展示。
              </div>
            </div>
          )}
        </section>
      </div>

      <ConfirmDialog
        description={(
          <div className="space-y-3">
            <p className="text-sm text-text-secondary">
              当前共 {total.toLocaleString()} 条记录。删除后不可恢复，请选择清理范围：
            </p>
            <div className="space-y-2">
              {CLEAR_MODE_OPTIONS.map((option) => {
                const active = clearMode === option.id;
                return (
                  <button
                    key={option.id}
                    className={`w-full rounded-xl border px-3 py-2.5 text-left transition ${
                      active
                        ? 'border-danger/40 bg-danger/5 shadow-sm'
                        : 'border-border bg-surface hover:border-border-strong'
                    }`}
                    type="button"
                    onClick={() => setClearMode(option.id)}
                  >
                    <div className="flex items-center gap-2">
                      <span className={`h-2 w-2 rounded-full ${active ? 'bg-danger' : 'bg-text-tertiary/50'}`} />
                      <span className="text-sm font-medium text-text">{option.label}</span>
                    </div>
                    <p className="mt-1 pl-4 text-xs text-text-tertiary">{option.desc}</p>
                  </button>
                );
              })}
            </div>
          </div>
        )}
        loading={clearMutation.isPending}
        open={confirmClear}
        title="清空请求审计"
        onConfirm={() => clearMutation.mutate(clearMode)}
        onOpenChange={(nextOpen) => { if (!nextOpen) setConfirmClear(false); }}
      />
    </div>
  );
}
