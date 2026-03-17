import { type ReactNode, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { X } from 'lucide-react';
import { Button } from './Button';

interface ModalProps {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  footer?: ReactNode;
  width?: string;
}

export function Modal({ open, onClose, title, children, footer, width = '480px' }: ModalProps) {
  useEffect(() => {
    if (!open) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', handler);
    document.body.style.overflow = 'hidden';
    return () => {
      document.removeEventListener('keydown', handler);
      document.body.style.overflow = '';
    };
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4" style={{ animation: 'ag-fade-in 0.15s ease-out' }}>
      {/* 遮罩层：去掉 backdrop-blur 提升性能 */}
      <div className="fixed inset-0 bg-black/20" onClick={onClose} />
      <div
        className="ag-glass-modal ag-elevation-modal relative rounded-2xl max-h-[85vh] flex flex-col"
        style={{ width, maxWidth: '90vw', animation: 'ag-scale-in 0.2s cubic-bezier(0.16, 1, 0.3, 1)' }}
      >
        <div className="flex items-center justify-between px-6 py-4 border-b" style={{ borderColor: 'var(--ag-border-subtle)' }}>
          <h3 className="text-sm font-semibold text-text">{title}</h3>
          <button
            onClick={onClose}
            className="flex items-center justify-center w-7 h-7 rounded-lg text-text-tertiary hover:text-text hover:bg-bg-hover transition-colors"
          >
            <X className="w-4 h-4" />
          </button>
        </div>
        <div className="px-6 py-5 overflow-y-auto flex-1">{children}</div>
        {footer && (
          <div className="flex items-center justify-end gap-3 px-6 py-4 border-t" style={{ borderColor: 'var(--ag-border-subtle)' }}>
            {footer}
          </div>
        )}
      </div>
    </div>
  );
}

interface ConfirmModalProps {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: string;
  message: string;
  loading?: boolean;
  danger?: boolean;
}

export function ConfirmModal({ open, onClose, onConfirm, title, message, loading, danger }: ConfirmModalProps) {
  const { t } = useTranslation();

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>{t('common.cancel')}</Button>
          <Button variant={danger ? 'danger' : 'primary'} onClick={onConfirm} loading={loading}>{t('common.confirm')}</Button>
        </>
      }
    >
      <p className="text-sm text-text-secondary">{message}</p>
    </Modal>
  );
}
