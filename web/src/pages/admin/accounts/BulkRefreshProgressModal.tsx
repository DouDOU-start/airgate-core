import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Modal, useOverlayState } from '@heroui/react';
import { Check, X, Loader2, AlertTriangle } from 'lucide-react';
import { accountsApi } from '../../../shared/api/accounts';
import { getToken } from '../../../shared/api/client';

type ItemStatus = 'pending' | 'running' | 'success' | 'warning' | 'error';

interface ItemState {
  id: number;
  name: string;
  status: ItemStatus;
  error?: string;
  warning?: string;
}

/**
 * 批量刷新令牌进度弹窗。打开时立即发起 SSE 流式请求，实时展示每个账号的进度与结果。
 * 关闭时自动 abort 请求并通知父组件刷新列表。
 */
export function BulkRefreshProgressModal({
  open,
  accounts,
  onClose,
  onFinished,
}: {
  open: boolean;
  accounts: { id: number; name: string }[];
  onClose: () => void;
  onFinished: () => void;
}) {
  const { t } = useTranslation();

  const [items, setItems] = useState<ItemState[]>([]);
  const [done, setDone] = useState(0);
  const [total, setTotal] = useState(0);
  const [success, setSuccess] = useState(0);
  const [failed, setFailed] = useState(0);
  const [finished, setFinished] = useState(false);
  const [fatalError, setFatalError] = useState('');

  const abortRef = useRef<AbortController | null>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // 打开时启动流
  useEffect(() => {
    if (!open) return;

    // 初始化状态
    const initial: ItemState[] = accounts.map((a) => ({
      id: a.id,
      name: a.name,
      status: 'pending',
    }));
    setItems(initial);
    setDone(0);
    setTotal(accounts.length);
    setSuccess(0);
    setFailed(0);
    setFinished(false);
    setFatalError('');

    const controller = new AbortController();
    abortRef.current = controller;

    const run = async () => {
      try {
        const headers: Record<string, string> = { 'Content-Type': 'application/json' };
        const token = getToken();
        if (token) headers['Authorization'] = `Bearer ${token}`;

        const res = await fetch(accountsApi.bulkRefreshQuotaUrl(), {
          method: 'POST',
          headers,
          body: JSON.stringify({ account_ids: accounts.map((a) => a.id) }),
          signal: controller.signal,
        });

        if (!res.ok || !res.body) {
          setFatalError(`HTTP ${res.status}`);
          setFinished(true);
          return;
        }

        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        // 标记当前正在处理的 id（上一条 progress 的 id+1 对应的队列位置）
        let nextRunningIdx = 0;

        while (true) {
          const { done: streamDone, value } = await reader.read();
          if (streamDone) break;
          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split('\n');
          buffer = lines.pop() ?? '';

          for (const line of lines) {
            const trimmed = line.trim();
            if (!trimmed.startsWith('data: ')) continue;
            try {
              const evt = JSON.parse(trimmed.slice(6));
              if (evt.type === 'start') {
                setTotal(evt.total);
                // 把第一条标成 running
                if (evt.total > 0) {
                  setItems((prev) => {
                    const next = [...prev];
                    if (next[0]) next[0] = { ...next[0], status: 'running' };
                    return next;
                  });
                  nextRunningIdx = 1;
                }
              } else if (evt.type === 'progress') {
                setDone(evt.done);
                if (evt.success) setSuccess((s) => s + 1);
                else setFailed((f) => f + 1);

                setItems((prev) => {
                  const next = [...prev];
                  const idx = next.findIndex((it) => it.id === evt.id);
                  if (idx >= 0) {
                    // reauth_warning：本次靠存量 access_token JWT 解析降级拿到；
                    // 仍计作成功，但单独展示 warning 徽标引导重新授权。
                    const status: ItemStatus = evt.success
                      ? (evt.reauth_warning ? 'warning' : 'success')
                      : 'error';
                    next[idx] = {
                      ...next[idx]!,
                      status,
                      error: evt.error,
                      warning: evt.reauth_warning,
                    };
                  }
                  // 把下一个队列项标成 running
                  if (nextRunningIdx < next.length) {
                    const runner = next[nextRunningIdx]!;
                    if (runner.status === 'pending') {
                      next[nextRunningIdx] = { ...runner, status: 'running' };
                    }
                    nextRunningIdx++;
                  }
                  return next;
                });

                // 自动滚动到底部
                requestAnimationFrame(() => {
                  if (listRef.current) listRef.current.scrollTop = listRef.current.scrollHeight;
                });
              } else if (evt.type === 'complete') {
                setFinished(true);
              }
            } catch {
              // ignore non-JSON
            }
          }
        }
      } catch (err) {
        if ((err as Error).name === 'AbortError') return;
        setFatalError((err as Error).message);
        setFinished(true);
      }
    };

    run();

    return () => {
      controller.abort();
    };
  }, [open]);

  const handleClose = () => {
    abortRef.current?.abort();
    if (done > 0) onFinished();
    onClose();
  };

  const pct = total > 0 ? Math.round((done / total) * 100) : 0;
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) handleClose();
    },
  });

  return (
    <Modal state={modalState}>
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '520px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{t('accounts.bulk_refresh_title')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
        {/* 进度条 */}
        <div>
          <div className="flex items-center justify-between text-xs mb-1.5" style={{ color: 'var(--ag-text-secondary)' }}>
            <span>
              {t('accounts.bulk_refresh_progress', { done, total })}
            </span>
            <span className="font-mono">{pct}%</span>
          </div>
          <div className="h-1.5 rounded-full overflow-hidden" style={{ background: 'var(--ag-glass-border)' }}>
            <div
              className="h-full transition-all duration-200"
              style={{ width: `${pct}%`, background: 'var(--ag-primary)' }}
            />
          </div>
        </div>

        {/* 汇总 */}
        <div className="flex items-center gap-4 text-xs" style={{ color: 'var(--ag-text-secondary)' }}>
          <span className="inline-flex items-center gap-1">
            <Check className="w-3.5 h-3.5" style={{ color: 'var(--ag-success)' }} />
            {t('accounts.bulk_refresh_success_count', { count: success })}
          </span>
          <span className="inline-flex items-center gap-1">
            <X className="w-3.5 h-3.5" style={{ color: 'var(--ag-danger)' }} />
            {t('accounts.bulk_refresh_failed_count', { count: failed })}
          </span>
        </div>

        {/* 账号列表 */}
        <div
          ref={listRef}
          className="rounded-lg overflow-y-auto"
          style={{
            maxHeight: 260,
            border: '1px solid var(--ag-glass-border)',
            background: 'var(--ag-bg-surface)',
          }}
        >
          {items.map((item) => (
            <div
              key={item.id}
              className="flex items-center gap-2 px-3 py-2 text-xs"
              style={{ borderBottom: '1px solid var(--ag-border-subtle)' }}
            >
              <StatusIcon status={item.status} />
              <span className="flex-1 truncate" style={{ color: 'var(--ag-text)' }}>
                {item.name}
              </span>
              {item.error && (
                <span
                  className="truncate max-w-[180px]"
                  style={{ color: 'var(--ag-danger)' }}
                  title={item.error}
                >
                  {item.error}
                </span>
              )}
              {!item.error && item.warning && (
                <span
                  className="truncate max-w-[180px]"
                  style={{ color: 'var(--ag-warning)' }}
                  title={item.warning}
                >
                  {t('accounts.refresh_quota_reauth_warning_short', '需重新授权')}
                </span>
              )}
            </div>
          ))}
        </div>

        {fatalError && (
          <div className="text-xs" style={{ color: 'var(--ag-danger)' }}>
            {fatalError}
          </div>
        )}
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant={finished ? 'primary' : 'secondary'} onPress={handleClose}>
                {finished ? t('common.close') : t('common.cancel')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

function StatusIcon({ status }: { status: ItemStatus }) {
  if (status === 'running') {
    return <Loader2 className="w-3.5 h-3.5 animate-spin" style={{ color: 'var(--ag-primary)' }} />;
  }
  if (status === 'success') {
    return <Check className="w-3.5 h-3.5" style={{ color: 'var(--ag-success)' }} />;
  }
  if (status === 'warning') {
    return <AlertTriangle className="w-3.5 h-3.5" style={{ color: 'var(--ag-warning)' }} />;
  }
  if (status === 'error') {
    return <X className="w-3.5 h-3.5" style={{ color: 'var(--ag-danger)' }} />;
  }
  return <div className="w-3.5 h-3.5 rounded-full" style={{ background: 'var(--ag-glass-border)' }} />;
}
