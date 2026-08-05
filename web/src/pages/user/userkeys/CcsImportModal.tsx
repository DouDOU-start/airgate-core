import { useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Modal, Spinner, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { ArrowUpRight, Braces, Sparkles, SquareTerminal } from 'lucide-react';
import { useToast } from '../../../shared/ui';
import { useSiteSettings } from '../../../app/providers/SiteSettingsProvider';
import { apikeysApi } from '../../../shared/api/apikeys';
import type { APIKeyResp } from '../../../shared/types';

type CcsClientType = 'claude' | 'codex' | 'grokbuild';

function normalizeBaseUrl(baseUrl: string): string {
  return baseUrl.trim().replace(/\/+$/, '');
}

function toGatewayRoot(baseUrl: string): string {
  return normalizeBaseUrl(baseUrl).replace(/\/v1$/i, '');
}

function toResponsesBaseUrl(baseUrl: string): string {
  return `${toGatewayRoot(baseUrl)}/v1`;
}

function executeCcsImport(
  baseUrl: string,
  apiKey: string,
  siteName: string,
  clientType: CcsClientType,
  toast: (type: 'success' | 'error', msg: string) => void,
  t: (key: string) => string,
) {
  // 渠道化后任意入口协议均可用，客户端类型只决定导入的 app 配置。
  const app: string = clientType;
  const gatewayRoot = toGatewayRoot(baseUrl);
  const endpoint = app === 'grokbuild' ? toResponsesBaseUrl(baseUrl) : normalizeBaseUrl(baseUrl);

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

  const params = new URLSearchParams({
    resource: 'provider',
    app,
    name: siteName,
    homepage: gatewayRoot,
    endpoint,
    apiKey,
    configFormat: 'json',
    usageEnabled: 'true',
    usageScript: btoa(usageScript),
    usageBaseUrl: gatewayRoot,
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
  if (app === 'grokbuild') {
    // CC-Switch 的 Grok Build 深链使用原生 Responses 配置。
    params.set('model', 'grok-4.5');
    params.set('icon', 'grok');
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
  const site = useSiteSettings();
  const siteName = site.site_name || 'AirGate';
  const baseUrl = site.api_base_url || window.location.origin;
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });
  const clientOptions = [
    {
      type: 'claude' as const,
      name: 'Claude Code',
      description: t('user_keys.ccs_claude_desc'),
      icon: Braces,
    },
    {
      type: 'codex' as const,
      name: 'Codex CLI',
      description: t('user_keys.ccs_codex_desc'),
      icon: SquareTerminal,
    },
    {
      type: 'grokbuild' as const,
      name: 'Grok Build',
      description: t('user_keys.ccs_grok_desc'),
      icon: Sparkles,
    },
  ];

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="lg">
          <Modal.Dialog className="ag-elevation-modal ag-ccs-import-modal">
            <Modal.Header>
              <Modal.Heading>{t('user_keys.ccs_select_client')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body className="ag-ccs-import-modal__body">
              {ccsKeyValue ? (
                <div className="ag-ccs-import-modal__content">
                  <p className="ag-ccs-import-modal__description">
                    {t('user_keys.ccs_select_client_desc')}
                  </p>
                  <div className="ag-ccs-client-grid">
                    {clientOptions.map((client) => {
                      const Icon = client.icon;
                      return (
                        <Button
                          key={client.type}
                          variant="secondary"
                          className="ag-ccs-client-card"
                          data-client={client.type}
                          aria-label={`${client.name}：${client.description}`}
                          onPress={() => {
                            executeCcsImport(baseUrl, ccsKeyValue, siteName, client.type, toast, t);
                            onClose();
                          }}
                        >
                          <span className="ag-ccs-client-card__icon" aria-hidden="true">
                            <Icon />
                          </span>
                          <span className="ag-ccs-client-card__copy">
                            <strong>{client.name}</strong>
                            <span>{client.description}</span>
                          </span>
                          <ArrowUpRight className="ag-ccs-client-card__arrow" aria-hidden="true" />
                        </Button>
                      );
                    })}
                  </div>
                </div>
              ) : (
                <div className="ag-ccs-import-modal__loading">
                  <Spinner size="sm" />
                  <span>{t('common.loading')}</span>
                </div>
              )}
            </Modal.Body>
            <Modal.Footer className="ag-ccs-import-modal__footer">
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
