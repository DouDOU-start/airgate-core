import { useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Terminal } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { useToast } from '../../../shared/components/Toast';
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

  try {
    window.open(deeplink, '_self');
    setTimeout(() => {
      if (document.hasFocus()) {
        toast('error', t('user_keys.ccs_not_installed'));
      }
    }, 100);
  } catch {
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

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('user_keys.ccs_select_client')}
      footer={
        <Button
          variant="secondary"
          onClick={onClose}
        >
          {t('common.cancel')}
        </Button>
      }
    >
      {ccsKeyValue ? (
        ccsPlatform ? (
          <div className="space-y-3">
            <p className="text-sm text-text-secondary">
              {t('user_keys.ccs_select_desc')}
            </p>
            <div className="grid grid-cols-2 gap-3">
              {/* 始终显示 Claude Code */}
              <button
                onClick={() => {
                  executeCcsImport(baseUrl, ccsKeyValue, 'claude', ccsPlatform, toast, t);
                  onClose();
                }}
                className="flex flex-col items-center gap-2 p-4 rounded-lg border border-glass-border bg-surface hover:bg-bg-hover hover:border-text-tertiary transition-colors"
              >
                <div className="w-10 h-10 rounded-lg bg-info-subtle flex items-center justify-center">
                  <Terminal className="w-5 h-5 text-info" />
                </div>
                <span className="text-sm font-medium text-text">Claude Code</span>
                <span className="text-xs text-text-tertiary text-center">
                  {t('user_keys.ccs_claude_desc')}
                </span>
              </button>

              {/* OpenAI 平台额外显示 Codex CLI */}
              {ccsPlatform === 'openai' && (
                <button
                  onClick={() => {
                    executeCcsImport(baseUrl, ccsKeyValue, 'codex', ccsPlatform, toast, t);
                    onClose();
                  }}
                  className="flex flex-col items-center gap-2 p-4 rounded-lg border border-glass-border bg-surface hover:bg-bg-hover hover:border-text-tertiary transition-colors"
                >
                  <div className="w-10 h-10 rounded-lg bg-success-subtle flex items-center justify-center">
                    <Terminal className="w-5 h-5 text-success" />
                  </div>
                  <span className="text-sm font-medium text-text">Codex CLI</span>
                  <span className="text-xs text-text-tertiary text-center">
                    {t('user_keys.ccs_codex_desc')}
                  </span>
                </button>
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
    </Modal>
  );
}
