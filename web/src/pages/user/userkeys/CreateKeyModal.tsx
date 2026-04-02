import { useTranslation } from 'react-i18next';
import { AlertTriangle, Copy } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { useToast } from '../../../shared/components/Toast';

export function CreateKeyModal({
  open,
  createdKey,
  onClose,
}: {
  open: boolean;
  createdKey: string | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('user_keys.create_success')}
      footer={
        <Button onClick={onClose}>{t('user_keys.key_saved_close')}</Button>
      }
    >
      <div className="space-y-4">
        <div className="flex items-start gap-2.5 rounded-md bg-danger-subtle border border-danger border-opacity-20 px-4 py-3">
          <AlertTriangle className="w-4 h-4 text-danger mt-0.5 shrink-0" />
          <p className="text-sm text-danger font-medium">
            {t('user_keys.key_created_warning')}
          </p>
        </div>
        <div
          className="border border-glass-border bg-bg-elevated shadow-sm rounded-lg p-3 break-all text-sm text-text font-mono"
        >
          {createdKey}
        </div>
        <Button
          variant="secondary"
          size="sm"
          onClick={() => {
            navigator.clipboard.writeText(createdKey || '');
            toast('success', t('user_keys.copy_key'));
          }}
          icon={<Copy className="w-3.5 h-3.5" />}
        >
          {t('user_keys.copy_key')}
        </Button>
      </div>
    </Modal>
  );
}
