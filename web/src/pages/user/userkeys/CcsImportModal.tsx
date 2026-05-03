import { useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Modal, useOverlayState } from '@heroui/react';
import { Terminal } from 'lucide-react';
import { useToast } from '../../../shared/ui';
import { apikeysApi } from '../../../shared/api/apikeys';
import type { APIKeyResp, GroupResp } from '../../../shared/types';

function executeCcsImport(
  baseUrl: string,
  apiKey: string,
  clientType: 'claude' | 'codex',
  platform: string,
  toast: (type: 'success' | 'error', msg: string) => void,
  t: (key: string) => string,
) {
  let app: string;
  let endpoint: string;

  if (platform === 'openai') {
    if (clientType === 'claude') {
      app = 'claude';
      endpoint = baseUrl;
    } else {
      app = 'codex';
      endpoint = baseUrl;
    }
  } else {
    app = 'claude';
    endpoint = baseUrl;
  }

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

export function useCcsImportModal(groupMap: Map<number, GroupResp>) {
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

  const getGroupPlatform = (groupId: number | null) =>
    groupId == null ? '' : groupMap.get(groupId)?.platform || '';

  const ccsPlatform = ccsTarget ? getGroupPlatform(ccsTarget.group_id) : '';

  return {
    ccsTarget,
    ccsKeyValue,
    ccsPlatform,
    openCcsModal,
    closeCcsModal,
  };
}

export function CcsImportModal({
  open,
  ccsKeyValue,
  ccsPlatform,
  onClose,
}: {
  open: boolean;
  ccsKeyValue: string | null;
  ccsPlatform: string;
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
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{t('user_keys.ccs_select_client')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
      {ccsKeyValue ? (
        ccsPlatform ? (
          <div className="space-y-3">
            <p className="text-sm text-text-secondary">
              {t('user_keys.ccs_select_desc')}
            </p>
            <div className="grid grid-cols-2 gap-3">
              {/* 始终显示 Claude Code */}
              <Button
                variant="secondary"
                className="h-auto flex-col gap-2 p-4"
                onPress={() => {
                  executeCcsImport(baseUrl, ccsKeyValue, 'claude', ccsPlatform, toast, t);
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

              {/* OpenAI 平台额外显示 Codex CLI */}
              {ccsPlatform === 'openai' && (
                <Button
                  variant="secondary"
                  className="h-auto flex-col gap-2 p-4"
                  onPress={() => {
                    executeCcsImport(baseUrl, ccsKeyValue, 'codex', ccsPlatform, toast, t);
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
              )}
            </div>
          </div>
        ) : (
          <div className="rounded-md border border-glass-border bg-surface p-4 text-sm text-text-secondary">
            {t('user_keys.group_unbound_hint')}
          </div>
        )
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
