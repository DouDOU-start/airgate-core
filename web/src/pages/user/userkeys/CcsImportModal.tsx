import { useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Modal, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { Terminal } from 'lucide-react';
import { useToast } from '../../../shared/ui';
import { apikeysApi } from '../../../shared/api/apikeys';
import type { APIKeyResp } from '../../../shared/types';

function executeCcsImport(
  baseUrl: string,
  apiKey: string,
  clientType: 'claude' | 'codex',
  toast: (type: 'success' | 'error', msg: string) => void,
  t: (key: string) => string,
) {
  // 渠道化后任意入口协议均可用，客户端类型只决定导入的 app 配置。
  const app: string = clientType;
  const endpoint: string = baseUrl;

  const usageScript = `({
    request: {
      url: "{{baseUrl}}/v1/usage",
      method: "GET",
      headers: { "Authorization": "Bearer {{apiKey}}" }
    },
    extractor: function(response) {
      const remaining = response?.remaining ?? response?.quota?.remaining ?? response?.balance;
      const unit = response?.unit ?? response?.quota?.unit ?? "USD";
      return {
        isValid: response?.is_active ?? response?.isValid ?? true,
        remaining,
        unit
      };
    }
  })`;

  const siteName = document.title || 'AirGate';
  const params = new URLSearchParams({
    resource: 'provider',
    app,
    name: siteName,
    homepage: baseUrl,
    endpoint,
    apiKey,
    configFormat: 'json',
    usageEnabled: 'true',
    usageScript: btoa(usageScript),
    usageAutoInterval: '30',
  });
  if (app === 'codex') {
    params.set('model', 'gpt-5.5');
    params.set('reviewModel', 'gpt-5.5');
    params.set('modelReasoningEffort', 'xhigh');
    params.set('disableResponseStorage', 'true');
    params.set('networkAccess', 'enabled');
    params.set('goals', 'true');
  }

  const deeplink = `ccswitch://v1/import?${params.toString()}`;

  let protocolHandled = false;
  const onBlur = () => {
    protocolHandled = true;
  };
  const onVisibilityChange = () => {
    if (document.visibilityState === 'hidden') {
      protocolHandled = true;
    }
  };
  window.addEventListener('blur', onBlur);
  document.addEventListener('visibilitychange', onVisibilityChange);

  try {
    const iframe = document.createElement('iframe');
    iframe.style.display = 'none';
    iframe.src = deeplink;
    document.body.appendChild(iframe);
    setTimeout(() => {
      iframe.remove();
      window.removeEventListener('blur', onBlur);
      document.removeEventListener('visibilitychange', onVisibilityChange);
      if (!protocolHandled && document.hasFocus()) {
        toast('error', t('user_keys.ccs_not_installed'));
      }
    }, 1500);
  } catch {
    window.removeEventListener('blur', onBlur);
    document.removeEventListener('visibilitychange', onVisibilityChange);
    toast('error', t('user_keys.ccs_not_installed'));
  }
}

export function useCcsImportModal() {
  const { toast } = useToast();
  const { t } = useTranslation();

  const [ccsTarget, setCcsTarget] = useState<APIKeyResp | null>(null);
  const [ccsKeyValue, setCcsKeyValue] = useState<string | null>(null);

  const openCcsModal = useCallback(
    async (row: APIKeyResp) => {
      setCcsTarget(row);
      try {
        const resp = await apikeysApi.reveal(row.id);
        setCcsKeyValue(resp.key || null);
      } catch {
        toast('error', t('user_keys.reveal_failed'));
        setCcsTarget(null);
      }
    },
    [toast, t],
  );

  const closeCcsModal = useCallback(() => {
    setCcsTarget(null);
    setCcsKeyValue(null);
  }, []);

  return {
    ccsTarget,
    ccsKeyValue,
    openCcsModal,
    closeCcsModal,
  };
}

export function CcsImportModal({
  open,
  ccsKeyValue,
  onClose,
}: {
  open: boolean;
  ccsKeyValue: string | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const baseUrl = window.location.origin;
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{t('user_keys.ccs_select_client')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
      {ccsKeyValue ? (
          <div className="space-y-3">
            <div className="grid grid-cols-2 gap-3">
              {/* Claude Code */}
              <Button
                variant="secondary"
                className="h-auto flex-col gap-2 p-4"
                onPress={() => {
                  executeCcsImport(baseUrl, ccsKeyValue, 'claude', toast, t);
                  onClose();
                }}
              >
                <div className="w-10 h-10 rounded-lg bg-info-subtle flex items-center justify-center">
                  <Terminal className="w-5 h-5 text-info" />
                </div>
                <span className="text-sm font-medium text-text">Claude Code</span>
                <span className="text-xs text-text-tertiary text-center">
                  {t('user_keys.ccs_claude_desc')}
                </span>
              </Button>

              {/* Codex CLI */}
              <Button
                variant="secondary"
                className="h-auto flex-col gap-2 p-4"
                onPress={() => {
                  executeCcsImport(baseUrl, ccsKeyValue, 'codex', toast, t);
                  onClose();
                }}
              >
                <div className="w-10 h-10 rounded-lg bg-success-subtle flex items-center justify-center">
                  <Terminal className="w-5 h-5 text-success" />
                </div>
                <span className="text-sm font-medium text-text">Codex CLI</span>
                <span className="text-xs text-text-tertiary text-center">
                  {t('user_keys.ccs_codex_desc')}
                </span>
              </Button>
            </div>
          </div>
      ) : (
        <div className="flex items-center justify-center py-8 text-text-tertiary text-sm">
          {t('common.loading')}
        </div>
      )}
            </Modal.Body>
            <Modal.Footer>
              <Button
                variant="secondary"
                onPress={onClose}
              >
                {t('common.cancel')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
