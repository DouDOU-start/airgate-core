import { useState, useRef, useEffect } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { AlertCircle } from 'lucide-react';

interface BadgeProps {
  children: string;
  variant?: 'default' | 'success' | 'warning' | 'danger' | 'info';
}

const variantStyles: Record<string, string> = {
  default: 'bg-bg-hover text-text-secondary border-glass-border',
  success: 'bg-success-subtle text-success border-success-subtle',
  warning: 'bg-warning-subtle text-warning border-warning-subtle',
  danger: 'bg-danger-subtle text-danger border-danger-subtle',
  info: 'bg-info-subtle text-info border-info-subtle',
};

export function Badge({ children, variant = 'default' }: BadgeProps) {
  return (
    <span
      className={`inline-flex items-center px-2.5 py-1 rounded-full text-[11px] font-semibold border ${variantStyles[variant]}`}
    >
      {children}
    </span>
  );
}

/** Outlined tag for version/code/metric display */
export function Tag({ children, className = '' }: { children: string; className?: string }) {
  return (
    <span className={`inline-flex items-center px-2.5 py-1 rounded-md text-[11px] font-semibold font-mono border border-glass-border text-text-secondary ${className}`}>
      {children}
    </span>
  );
}

const statusMap: Record<string, { variant: BadgeProps['variant']; label: string }> = {
  active: { variant: 'success', label: 'status.active' },
  enabled: { variant: 'success', label: 'status.enabled' },
  disabled: { variant: 'default', label: 'status.disabled' },
  error: { variant: 'danger', label: 'status.error' },
  expired: { variant: 'warning', label: 'status.expired' },
  suspended: { variant: 'warning', label: 'status.suspended' },
  pending: { variant: 'info', label: 'status.pending' },
  paid: { variant: 'success', label: 'status.paid' },
  failed: { variant: 'danger', label: 'status.failed' },
  installed: { variant: 'info', label: 'status.installed' },
};

/** 错误详情弹出框（Portal 渲染，避免被 overflow 裁切） */
function ErrorPopover({ anchor, content, onClose }: { anchor: DOMRect; content: string; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [onClose]);

  // 尝试解析 JSON 中的 message 字段
  let title = '';
  let message = content;
  const httpMatch = content.match(/^HTTP (\d+): (.*)$/s);
  if (httpMatch) {
    title = `HTTP ${httpMatch[1]}`;
    const jsonBody = httpMatch[2] ?? '';
    try {
      const parsed = JSON.parse(jsonBody);
      message = parsed?.error?.message || parsed?.message || jsonBody;
    } catch {
      message = jsonBody || content;
    }
  }

  const popoverWidth = 360;
  let left = anchor.left + anchor.width / 2 - popoverWidth / 2;
  if (left < 8) left = 8;
  if (left + popoverWidth > window.innerWidth - 8) left = window.innerWidth - 8 - popoverWidth;
  const top = anchor.bottom + 8;

  return createPortal(
    <div
      ref={ref}
      className="fixed z-[9999] animate-in fade-in slide-in-from-top-1 duration-150"
      style={{ top, left, width: popoverWidth }}
    >
      <div
        className="rounded-lg border shadow-lg overflow-hidden"
        style={{
          background: 'var(--ag-bg-card)',
          borderColor: 'var(--ag-danger)',
          boxShadow: '0 4px 24px rgba(0,0,0,0.25), 0 0 12px var(--ag-danger-glow, rgba(239,68,68,0.15))',
        }}
      >
        {/* 头部 */}
        <div
          className="flex items-center gap-2 px-3 py-2"
          style={{ background: 'var(--ag-danger-subtle)', borderBottom: '1px solid var(--ag-border)' }}
        >
          <AlertCircle className="w-4 h-4 shrink-0" style={{ color: 'var(--ag-danger)' }} />
          <span className="text-xs font-semibold" style={{ color: 'var(--ag-danger)' }}>
            {title || '错误详情'}
          </span>
        </div>
        {/* 内容 */}
        <div className="px-3 py-2.5">
          <p
            className="text-xs leading-relaxed break-words whitespace-pre-wrap"
            style={{ color: 'var(--ag-text-secondary)' }}
          >
            {message}
          </p>
        </div>
      </div>
    </div>,
    document.body,
  );
}

export function StatusBadge({ status, tooltip }: { status: string; tooltip?: string }) {
  const { t } = useTranslation();
  const config = statusMap[status] || { variant: 'default' as const, label: status };
  const [open, setOpen] = useState(false);
  const [rect, setRect] = useState<DOMRect | null>(null);
  const spanRef = useRef<HTMLSpanElement>(null);

  const handleClick = () => {
    if (!tooltip) return;
    if (open) {
      setOpen(false);
    } else {
      setRect(spanRef.current?.getBoundingClientRect() ?? null);
      setOpen(true);
    }
  };

  return (
    <>
      <span
        ref={spanRef}
        className={tooltip ? 'cursor-pointer' : undefined}
        onClick={handleClick}
      >
        <Badge variant={config.variant}>{t(config.label)}</Badge>
      </span>
      {open && rect && tooltip && (
        <ErrorPopover anchor={rect} content={tooltip} onClose={() => setOpen(false)} />
      )}
    </>
  );
}
