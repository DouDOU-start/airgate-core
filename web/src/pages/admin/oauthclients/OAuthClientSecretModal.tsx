import { useTranslation } from 'react-i18next';
import { Alert, Button, Modal, useOverlayState } from '@heroui/react';
import { Copy } from 'lucide-react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { useClipboard } from '../../../shared/hooks/useClipboard';
import type { OAuthClientSecretResp } from '../../../shared/types';

// 创建/重置后一次性展示 client_id + client_secret（关闭后无法再次查看）。
export function OAuthClientSecretModal({
  credential,
  onClose,
}: {
  credential: OAuthClientSecretResp | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const copy = useClipboard();

  const modalState = useOverlayState({
    isOpen: !!credential,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  const renderField = (label: string, value: string) => (
    <div>
      <div className="text-xs font-medium text-text-tertiary mb-1">{label}</div>
      <div className="flex items-center gap-2">
        <code className="flex-1 rounded-md bg-surface-secondary px-3 py-2 text-xs break-all select-all">
          {value}
        </code>
        <Button
          isIconOnly
          aria-label={t('common.copy')}
          size="sm"
          variant="ghost"
          onPress={() => copy(value)}
        >
          <Copy className="w-4 h-4" />
        </Button>
      </div>
    </div>
  );

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" size="md">
          <Modal.Dialog className="ag-elevation-modal" style={{ maxWidth: '560px' }}>
            <Modal.Header>
              <Modal.Heading>{t('oauth_clients.secret_title')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <Alert status="warning">{t('oauth_clients.secret_warning')}</Alert>
                {credential ? renderField('Client ID', credential.client_id) : null}
                {credential ? renderField('Client Secret', credential.client_secret) : null}
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="primary" onPress={onClose}>
                {t('oauth_clients.secret_saved')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
