import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Modal, Spinner, useOverlayState } from '@heroui/react';
import { AlertTriangle, RotateCcw } from 'lucide-react';
import { pluginsApi } from '../../../shared/api/plugins';
import { ConfirmDialog } from '../../../shared/components/ConfirmDialog';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { useToast } from '../../../shared/ui';
import type { AccountResp } from '../../../shared/types';
import { AccountSummaryCard } from './AccountSummaryCard';

export const CODEX_FINGERPRINT_CAPABILITY = 'codex_fingerprint.v1';
export const CODEX_ENHANCE_PLUGIN_ID = 'airgate-codex-enhance';

export interface AccountFingerprintView {
  account_id: number;
  user_agent: string;
  client_user_agent: string;
  client_version: string;
  originator: string;
  installation_id: string;
  window_id: string;
  session_id: string;
  tls_hello: string;
}

const FINGERPRINT_FIELDS: Array<{ key: keyof AccountFingerprintView; labelKey: string }> = [
  { key: 'user_agent', labelKey: 'accounts.fingerprint_user_agent' },
  { key: 'client_user_agent', labelKey: 'accounts.fingerprint_client_user_agent' },
  { key: 'client_version', labelKey: 'accounts.fingerprint_client_version' },
  { key: 'originator', labelKey: 'accounts.fingerprint_originator' },
  { key: 'installation_id', labelKey: 'accounts.fingerprint_installation_id' },
  { key: 'window_id', labelKey: 'accounts.fingerprint_window_id' },
  { key: 'session_id', labelKey: 'accounts.fingerprint_session_id' },
  { key: 'tls_hello', labelKey: 'accounts.fingerprint_tls_hello' },
];

/** Codex 账号原生出站指纹：只读查看，整份重置。 */
export function AccountFingerprintModal({
  account,
  pluginId = CODEX_ENHANCE_PLUGIN_ID,
  onClose,
}: {
  account: AccountResp | null;
  pluginId?: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const open = !!account;
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (next) => {
      if (!next) onClose();
    },
  });
  const [loading, setLoading] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [confirmReset, setConfirmReset] = useState(false);
  const [fingerprint, setFingerprint] = useState<AccountFingerprintView | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!account) {
      setFingerprint(null);
      setError('');
      setConfirmReset(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError('');
    setFingerprint(null);
    void pluginsApi
      .action<AccountFingerprintView>(pluginId, 'fingerprints/get', { account_id: account.id })
      .then((data) => {
        if (!cancelled) setFingerprint(data);
      })
      .catch((err: Error) => {
        if (!cancelled) setError(err.message || t('accounts.fingerprint_load_failed'));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [account?.id, pluginId, t]);

  async function handleReset() {
    if (!account) return;
    setResetting(true);
    try {
      const next = await pluginsApi.action<AccountFingerprintView>(pluginId, 'fingerprints/reset', {
        account_id: account.id,
      });
      setFingerprint(next);
      setConfirmReset(false);
      toast('success', t('accounts.fingerprint_reset_success'));
    } catch (err) {
      toast('error', err instanceof Error ? err.message : t('accounts.fingerprint_reset_failed'));
    } finally {
      setResetting(false);
    }
  }

  return (
    <>
      <Modal state={modalState}>
        <DialogTriggerShim />
        <Modal.Backdrop>
          <Modal.Container placement="center" size="lg" scroll="inside">
            <Modal.Dialog className="ag-elevation-modal ag-account-fingerprint-modal">
              <Modal.Header>
                <Modal.Heading>{t('accounts.fingerprint_title')}</Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body className="ag-account-fingerprint-modal__body">
                {account ? (
                  <AccountSummaryCard
                    account={account}
                    context={t('accounts.fingerprint_hint')}
                  />
                ) : null}

                {loading ? (
                  <div className="ag-account-fingerprint-state">
                    <Spinner />
                    <span>{t('common.loading')}</span>
                  </div>
                ) : null}

                {!loading && error ? (
                  <div className="ag-account-fingerprint-state" data-status="error">
                    <AlertTriangle className="h-5 w-5" aria-hidden="true" />
                    <span>{error}</span>
                  </div>
                ) : null}

                {!loading && !error && fingerprint ? (
                  <dl className="ag-account-fingerprint-fields">
                    {FINGERPRINT_FIELDS.map((field) => (
                      <div key={field.key} className="ag-account-fingerprint-field">
                        <dt>{t(field.labelKey)}</dt>
                        <dd>
                          <code>{String(fingerprint[field.key] ?? '')}</code>
                        </dd>
                      </div>
                    ))}
                  </dl>
                ) : null}
              </Modal.Body>
              <Modal.Footer>
                <Button
                  variant="secondary"
                  isDisabled={loading || resetting || !fingerprint}
                  onPress={() => setConfirmReset(true)}
                >
                  <RotateCcw className="h-3.5 w-3.5" aria-hidden="true" />
                  {t('accounts.fingerprint_reset')}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      <ConfirmDialog
        open={confirmReset}
        onOpenChange={setConfirmReset}
        title={t('accounts.fingerprint_reset')}
        description={t('accounts.fingerprint_reset_confirm')}
        loading={resetting}
        status="warning"
        confirmVariant="danger"
        onConfirm={() => {
          void handleReset();
        }}
      />
    </>
  );
}

export function accountSupportsFingerprint(platform?: string) {
  return (platform || '').toLowerCase() === 'codex';
}

export function fingerprintPluginId(plugins: Array<{ id: string; running: boolean; capabilities: string[] }>) {
  const match = plugins.find((plugin) => (
    plugin.running && plugin.capabilities.includes(CODEX_FINGERPRINT_CAPABILITY)
  ));
  return match?.id ?? '';
}

export function fingerprintFeatureEnabled(values?: Record<string, unknown>) {
  const value = values?.fingerprint_enabled;
  return value === true || value === 'true' || value === 1 || value === '1' || value === 'on';
}
