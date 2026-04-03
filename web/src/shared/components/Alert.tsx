import { type ReactNode } from 'react';
import { CheckCircle, XCircle, AlertTriangle, Info, X } from 'lucide-react';

type AlertVariant = 'success' | 'error' | 'warning' | 'info';

interface AlertProps {
  variant: AlertVariant;
  title?: string;
  children: ReactNode;
  onClose?: () => void;
  className?: string;
}

const config: Record<AlertVariant, { icon: ReactNode; border: string; bg: string; text: string }> = {
  success: {
    icon: <CheckCircle className="w-[18px] h-[18px]" />,
    border: 'border-success/20',
    bg: 'bg-success-subtle',
    text: 'text-success',
  },
  error: {
    icon: <XCircle className="w-[18px] h-[18px]" />,
    border: 'border-danger/20',
    bg: 'bg-danger-subtle',
    text: 'text-danger',
  },
  warning: {
    icon: <AlertTriangle className="w-[18px] h-[18px]" />,
    border: 'border-warning/20',
    bg: 'bg-warning-subtle',
    text: 'text-warning',
  },
  info: {
    icon: <Info className="w-[18px] h-[18px]" />,
    border: 'border-info/20',
    bg: 'bg-info-subtle',
    text: 'text-info',
  },
};

export function Alert({ variant, title, children, onClose, className = '' }: AlertProps) {
  const c = config[variant];

  return (
    <div className={`flex items-start gap-3 rounded-[10px] border ${c.border} ${c.bg} px-3.5 py-3 ${className}`}>
      <span className={`flex-shrink-0 mt-0.5 ${c.text}`}>{c.icon}</span>
      <div className="flex-1 min-w-0">
        {title && <p className={`text-[13px] font-semibold ${c.text}`}>{title}</p>}
        <div className="text-xs text-text-secondary">{children}</div>
      </div>
      {onClose && (
        <button
          onClick={onClose}
          className="flex-shrink-0 text-text-tertiary hover:text-text transition-colors"
        >
          <X className="w-4 h-4" />
        </button>
      )}
    </div>
  );
}
