import { useTranslation } from 'react-i18next';
import { Alert, Button, Modal, useOverlayState } from '@heroui/react';
import { AlertTriangle, Copy } from 'lucide-react';
import { useClipboard } from '../../../shared/hooks/useClipboard';

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
  const copy = useClipboard();
  const state = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  return (
    <Modal state={state}>
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{t('user_keys.create_success')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <Alert status="danger">
                  <Alert.Indicator>
                    <AlertTriangle className="h-4 w-4" />
                  </Alert.Indicator>
                  <Alert.Content>
                    <Alert.Description>{t('user_keys.key_created_warning')}</Alert.Description>
                  </Alert.Content>
                </Alert>
                <div className="break-all rounded-lg border border-glass-border bg-bg-elevated p-3 font-mono text-sm text-text shadow-sm">
                  {createdKey}
                </div>
                <Button
                  size="sm"
                  variant="secondary"
                  onPress={() => copy(createdKey || '', t('user_keys.copy_key'))}
                >
                  <Copy className="h-3.5 w-3.5" />
                  {t('user_keys.copy_key')}
                </Button>
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="primary" onPress={onClose}>
                {t('user_keys.key_saved_close')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
