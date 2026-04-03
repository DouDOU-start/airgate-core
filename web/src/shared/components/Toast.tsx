import { useState, createContext, useContext, useCallback, type ReactNode } from 'react';
import { CheckCircle, XCircle, AlertTriangle, Info, X } from 'lucide-react';

type ToastType = 'success' | 'error' | 'warning' | 'info';

interface ToastMessage {
  id: number;
  type: ToastType;
  message: string;
  title?: string;
}

interface ToastContextType {
  toast: (type: ToastType, message: string, title?: string) => void;
}

const ToastContext = createContext<ToastContextType>({ toast: () => {} });

let nextId = 0;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [messages, setMessages] = useState<ToastMessage[]>([]);

  const toast = useCallback((type: ToastType, message: string, title?: string) => {
    const id = nextId++;
    setMessages((prev) => [...prev, { id, type, message, title }]);
    setTimeout(() => {
      setMessages((prev) => prev.filter((m) => m.id !== id));
    }, 4000);
  }, []);

  return (
    <ToastContext.Provider value={{ toast }}>
      {children}
      <div className="fixed top-5 right-5 z-[100] flex flex-col gap-2.5 pointer-events-none">
        {messages.map((msg) => (
          <ToastItem
            key={msg.id}
            {...msg}
            onClose={() => setMessages((prev) => prev.filter((m) => m.id !== msg.id))}
          />
        ))}
      </div>
    </ToastContext.Provider>
  );
}

const typeConfig = {
  success: { icon: CheckCircle, border: 'border-success/20', color: 'text-success' },
  error: { icon: XCircle, border: 'border-danger/20', color: 'text-danger' },
  warning: { icon: AlertTriangle, border: 'border-warning/20', color: 'text-warning' },
  info: { icon: Info, border: 'border-primary/20', color: 'text-primary' },
};

function ToastItem({ type, message, title, onClose }: ToastMessage & { onClose: () => void }) {
  const config = typeConfig[type];
  const Icon = config.icon;

  return (
    <div
      className={`pointer-events-auto flex items-start gap-3 px-3.5 py-3 rounded-xl border ${config.border} bg-bg-elevated shadow-md min-w-[280px] max-w-[400px]`}
      style={{ animation: 'ag-slide-down 0.25s cubic-bezier(0.16, 1, 0.3, 1)' }}
    >
      <Icon className={`w-[18px] h-[18px] flex-shrink-0 mt-0.5 ${config.color}`} />
      <div className="flex-1 min-w-0">
        {title && <p className={`text-sm font-semibold ${config.color}`}>{title}</p>}
        <span className="text-[13px] text-text-secondary">{message}</span>
      </div>
      <button
        onClick={onClose}
        className="flex-shrink-0 text-text-tertiary hover:text-text transition-colors mt-0.5"
      >
        <X className="w-4 h-4" />
      </button>
    </div>
  );
}

export function useToast() {
  return useContext(ToastContext);
}
