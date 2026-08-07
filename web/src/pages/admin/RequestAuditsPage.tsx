import { useEffect, useMemo, useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Input } from '@heroui/react';
import {
  Braces,
  Check,
  ChevronRight,
  Clipboard,
  DatabaseZap,
  ShieldAlert,
} from 'lucide-react';

import { requestAuditsApi } from '../../shared/api/requestAudits';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { usePagination } from '../../shared/hooks/usePagination';
import { RefreshButton } from '../../shared/components/RefreshButton';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { formatDateTime } from '../../shared/utils/format';
import type {
  RequestAuditAttempt,
  RequestAuditListItem,
  RequestAuditPayload,
} from '../../shared/types';

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
    <div className="min-w-0 rounded-lg border border-border bg-surface-secondary/60 px-3 py-2">
      <div className="text-[10px] uppercase tracking-[0.12em] text-text-tertiary">{label}</div>
      <div className="mt-1 truncate font-mono text-xs font-semibold text-text" title={String(value)}>{value}</div>
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
      className={`group grid w-full grid-cols-[90px_minmax(150px,1.2fr)_minmax(120px,1fr)_70px_34px] items-center gap-3 border-b border-border px-3 py-3 text-left transition ${active ? 'bg-accent-soft/70 shadow-[inset_3px_0_0_var(--ag-accent)]' : 'hover:bg-surface-secondary/70'}`}
      type="button"
      onClick={onSelect}
    >
      <div>
        <div className="font-mono text-[11px] font-semibold text-text">{new Date(row.created_at).toLocaleTimeString('zh-CN', { hour12: false })}</div>
        <div className="mt-0.5 text-[10px] text-text-tertiary">{new Date(row.created_at).toLocaleDateString('zh-CN')}</div>
      </div>
      <div className="min-w-0">
        <div className="truncate font-mono text-xs font-semibold text-text" title={row.model}>{row.model}</div>
        <div className="mt-1 truncate font-mono text-[10px] text-text-tertiary" title={row.request_id}>{row.request_id || `审计 #${row.id}`}</div>
      </div>
      <div className="min-w-0">
        <div className="truncate text-xs text-text-secondary" title={row.user_email}>{row.user_email || `用户 #${row.user_id}`}</div>
        <div className="mt-1 truncate text-[10px] text-text-tertiary" title={row.routes.join(' → ')}>{row.routes.join(' → ') || '未触达上游'}</div>
      </div>
      <div>
        <span className={`inline-flex rounded-md border px-2 py-1 font-mono text-[11px] font-bold ${statusTone(row.status_code)}`}>
          {row.status_code || '—'}
        </span>
        <div className="mt-1 font-mono text-[10px] text-text-tertiary">{row.attempt_count} 次</div>
      </div>
      <ChevronRight className={`h-4 w-4 justify-self-end transition ${active ? 'text-accent' : 'text-text-tertiary group-hover:translate-x-0.5 group-hover:text-text'}`} />
    </button>
  );
}

export default function RequestAuditsPage() {
  const { page, setPage, pageSize, setPageSize } = usePagination(15, 'admin-request-audits');
  const [keyword, setKeyword] = useState('');
  const [model, setModel] = useState('');
  const [statusCode, setStatusCode] = useState('');
  const [selectedID, setSelectedID] = useState<number | null>(null);
  const [attemptSeq, setAttemptSeq] = useState(1);
  const debouncedKeyword = useDebouncedValue(keyword, 300);
  const debouncedModel = useDebouncedValue(model, 300);

  const listQuery = useQuery({
    queryKey: ['request-audits', page, pageSize, debouncedKeyword, debouncedModel, statusCode],
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

  return (
    <div className="space-y-4">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="mb-1 flex items-center gap-2 font-mono text-[10px] uppercase tracking-[0.2em] text-accent">
            <DatabaseZap className="h-3.5 w-3.5" />
            网关请求取证
          </div>
          <h1 className="text-2xl font-semibold tracking-tight text-text">完整请求审计</h1>
          <p className="mt-1 text-sm text-text-secondary">对比客户端原始请求、实际调度账号或渠道，以及每次真实上游发包。</p>
        </div>
        <div className="flex items-center gap-2 rounded-lg border border-warning/25 bg-warning/10 px-3 py-2 text-xs text-warning">
          <ShieldAlert className="h-4 w-4" />
          页面包含解密后的凭证与提示词，仅限管理员排障使用
        </div>
      </header>

      <div className="grid gap-3 rounded-xl border border-border bg-surface p-3 shadow-sm md:grid-cols-[minmax(220px,1.4fr)_minmax(180px,1fr)_150px_auto]">
        <Input
          aria-label="搜索请求"
          placeholder="Request ID、用户/账号邮箱、渠道或模型"
          value={keyword}
          onChange={(event) => { setKeyword(event.target.value); setPage(1); }}
        />
        <Input
          aria-label="模型筛选"
          placeholder="模型名称"
          value={model}
          onChange={(event) => { setModel(event.target.value); setPage(1); }}
        />
        <select
          aria-label="状态码筛选"
          className="h-10 rounded-lg border border-border bg-surface px-3 text-sm text-text outline-none focus:border-accent"
          value={statusCode}
          onChange={(event) => { setStatusCode(event.target.value); setPage(1); }}
        >
          <option value="">全部状态</option>
          <option value="200">200 成功</option>
          <option value="400">400 请求错误</option>
          <option value="401">401 未授权</option>
          <option value="403">403 禁止访问</option>
          <option value="429">包含 429 尝试</option>
          <option value="499">499 用户中断</option>
          <option value="500">500 服务错误</option>
          <option value="503">503 上游不可用</option>
        </select>
        <RefreshButton ariaLabel="刷新请求审计" isRefreshing={listQuery.isFetching} onRefresh={() => listQuery.refetch()} />
      </div>

      <div className="grid min-h-[720px] overflow-hidden rounded-2xl border border-border bg-surface shadow-[0_22px_70px_rgba(15,23,42,0.08)] xl:grid-cols-[minmax(500px,0.9fr)_minmax(620px,1.1fr)]">
        <section className="min-w-0 border-b border-border xl:border-b-0 xl:border-r">
          <div className="grid grid-cols-[90px_minmax(150px,1.2fr)_minmax(120px,1fr)_70px_34px] gap-3 border-b border-border bg-surface-secondary/70 px-3 py-2 font-mono text-[10px] uppercase tracking-[0.12em] text-text-tertiary">
            <span>时间</span><span>请求</span><span>用户 / 路由</span><span>状态</span><span />
          </div>
          <div className="max-h-[665px] overflow-auto">
            {listQuery.isLoading ? (
              <div className="p-8 text-center text-sm text-text-tertiary">正在读取审计索引…</div>
            ) : rows.length === 0 ? (
              <div className="p-12 text-center text-sm text-text-tertiary">没有符合条件的审计记录</div>
            ) : rows.map((row) => (
              <AuditRow key={row.id} row={row} active={row.id === selectedID} onSelect={() => setSelectedID(row.id)} />
            ))}
          </div>
          <div className="border-t border-border p-3">
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

        <section className="min-w-0 bg-[radial-gradient(circle_at_top_right,rgba(34,211,238,0.06),transparent_34%)] p-4">
          {!selectedID ? (
            <div className="flex h-full min-h-[500px] items-center justify-center text-sm text-text-tertiary">从左侧选择一条请求</div>
          ) : detailQuery.isLoading ? (
            <div className="flex h-full min-h-[500px] items-center justify-center text-sm text-text-tertiary">正在解密请求快照…</div>
          ) : !detail ? (
            <div className="flex h-full min-h-[500px] items-center justify-center text-sm text-danger">详情读取失败</div>
          ) : (
            <div className="space-y-4">
              <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border pb-4">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className={`rounded-md border px-2 py-1 font-mono text-xs font-bold ${statusTone(detail.status_code)}`}>{detail.status_code || '处理中'}</span>
                    <span className="font-mono text-sm font-semibold text-text">{detail.method} {detail.path}</span>
                    {detail.stream ? <span className="rounded bg-cyan-500/10 px-2 py-1 text-[10px] font-semibold text-cyan-500">流式</span> : null}
                  </div>
                  <div className="mt-2 break-all font-mono text-[10px] text-text-tertiary">{detail.request_id}</div>
                </div>
                <div className="text-right text-xs text-text-tertiary">
                  <div>{formatDateTime(detail.created_at)}</div>
                  <div className="mt-1 font-mono">总耗时 {detail.duration_ms.toLocaleString()} ms</div>
                </div>
              </div>

              <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                <Metric label="用户" value={detail.user_email || `#${detail.user_id}`} />
                <Metric label="API Key" value={`#${detail.api_key_id}`} />
                <Metric label="客户端" value={detail.client || '普通客户端'} />
                <Metric label="来源 IP" value={detail.ip_address || detail.remote_addr || '—'} />
              </div>

              <div>
                <div className="mb-2 flex items-center justify-between">
                  <h2 className="text-sm font-semibold text-text">实际上游尝试</h2>
                  <span className="font-mono text-[10px] text-text-tertiary">{detail.attempts.length} 次真实触网</span>
                </div>
                <div className="flex gap-2 overflow-x-auto pb-1">
                  {detail.attempts.length === 0 ? (
                    <div className="rounded-lg border border-dashed border-border px-3 py-2 text-xs text-text-tertiary">请求在触网上游前结束</div>
                  ) : detail.attempts.map((attempt) => (
                    <button
                      key={attempt.id}
                      className={`min-w-[180px] rounded-lg border px-3 py-2 text-left transition ${attempt.seq === selectedAttempt?.seq ? 'border-accent bg-accent-soft shadow-sm' : 'border-border bg-surface hover:border-accent/40'}`}
                      type="button"
                      onClick={() => setAttemptSeq(attempt.seq)}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <span className="font-mono text-[10px] font-bold text-text-tertiary">尝试 {attempt.seq}</span>
                        <span className={`font-mono text-[10px] font-bold ${attempt.status_code === 429 ? 'text-warning' : attempt.status_code >= 400 ? 'text-danger' : 'text-success'}`}>{attempt.status_code || '错误'}</span>
                      </div>
                      <div className="mt-1 truncate text-xs font-semibold text-text" title={routeLabel(attempt)}>{routeLabel(attempt)}</div>
                      <div className="mt-1 flex items-center justify-between text-[10px] text-text-tertiary">
                        <span>{verdictLabel(attempt.verdict)}</span>
                        <span className="font-mono">{attempt.latency_ms} ms</span>
                      </div>
                    </button>
                  ))}
                </div>
              </div>

              {selectedAttempt ? (
                <div className="space-y-3 rounded-xl border border-border bg-surface-secondary/35 p-3">
                  <div className="grid gap-2 sm:grid-cols-4">
                    <Metric label="调度类型" value={selectedAttempt.route_kind === 'account' ? 'OAuth / 账号' : '渠道密钥'} />
                    <Metric label="账号或渠道" value={routeLabel(selectedAttempt)} />
                    <Metric label="首字耗时" value={selectedAttempt.first_token_ms ? `${selectedAttempt.first_token_ms} ms` : '—'} />
                    <Metric label="流完成" value={selectedAttempt.stream_completed ? '是' : '否'} />
                  </div>
                  <div className="rounded-lg border border-border bg-surface px-3 py-2">
                    <div className="text-[10px] uppercase tracking-[0.12em] text-text-tertiary">实际上游 URL</div>
                    <div className="mt-1 break-all font-mono text-[11px] text-text">{selectedAttempt.upstream_url.content || '—'}</div>
                    {selectedAttempt.reason ? <div className="mt-2 border-t border-border pt-2 text-xs text-danger">{selectedAttempt.reason}</div> : null}
                  </div>
                </div>
              ) : null}

              <div className="grid gap-3 2xl:grid-cols-2">
                <CodePanel title="客户端原始 Header" payload={detail.inbound_headers} />
                <CodePanel title={`上游 Header · Attempt ${selectedAttempt?.seq ?? '—'}`} payload={selectedAttempt?.headers} />
                <CodePanel title="客户端原始 Body" payload={detail.inbound_body} />
                <CodePanel title={`上游 Body · Attempt ${selectedAttempt?.seq ?? '—'}`} payload={selectedAttempt?.body} />
              </div>

              <div className="flex items-center gap-2 rounded-lg border border-cyan-500/20 bg-cyan-500/5 px-3 py-2 text-[11px] text-text-secondary">
                <ShieldAlert className="h-4 w-4 shrink-0 text-cyan-500" />
                base64 图片已替换为长度与 SHA-256 占位符；其他 Header 和 Body 字段按真实内容解密展示。
              </div>
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
