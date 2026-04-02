import { useTranslation } from 'react-i18next';
import { AlertTriangle, Copy } from 'lucide-react';
import { Modal } from './Modal';
import { Button } from './Button';
import { useToast } from './Toast';

interface KeyRevealModalProps {
  open: boolean;
  keyValue: string;
  title: string;
  warningText?: string;
  closeText?: string;
  onClose: () => void;
}

export function KeyRevealModal({
  open,
  keyValue,
  title,
  warningText,
  closeText,
  onClose,
}: KeyRevealModalProps) {
  const { t } = useTranslation();
  const { toast } = useToast();

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(keyValue);
      toast('success', t('common.copied'));
    } catch {
      toast('error', t('common.copy_failed'));
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      footer={
        <Button onClick={onClose}>{closeText ?? t('common.close')}</Button>
      }
    >
      <div className="space-y-4">
        {warningText && (
          <div
            className="rounded-md p-4 flex items-start gap-3"
            style={{
              background: 'var(--ag-warning-subtle)',
              border: '1px solid var(--ag-warning)',
            }}
          >
            <AlertTriangle className="w-4 h-4 flex-shrink-0 mt-0.5" style={{ color: 'var(--ag-warning)' }} />
            <p className="text-sm font-medium" style={{ color: 'var(--ag-warning)' }}>
              {warningText}
            </p>
          </div>
        )}
        <div className="flex items-center gap-2">
          <code
            className="flex-1 px-3 py-2 rounded-md text-sm break-all"
            style={{
              fontFamily: 'var(--ag-font-mono)',
              background: 'var(--ag-bg-surface)',
              color: 'var(--ag-text)',
              border: '1px solid var(--ag-glass-border)',
            }}
          >
            {keyValue}
          </code>
          <Button
            size="sm"
            variant="secondary"
            icon={<Copy className="w-3.5 h-3.5" />}
            onClick={handleCopy}
          >
            {t('common.copy')}
          </Button>
        </div>
      </div>
    </Modal>
  );
}
