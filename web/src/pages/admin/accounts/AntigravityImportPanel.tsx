import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Description,
  Label,
  Spinner,
  TextArea,
  TextField as HeroTextField,
} from '@heroui/react';
import { accountsApi } from '../../../shared/api/accounts';
import { useToast } from '../../../shared/ui';
import type { StartOAuthReq } from '../../../shared/types';
import { OAuthAuthPanel } from './OAuthAuthPanel';

type AntigravityImportTab = 'authorize' | 'refresh';

/** Antigravity 支持浏览器授权，也支持仅粘贴 RT 自动换票并发现项目 ID。 */
export function AntigravityImportPanel({
  startOptions,
  onSuccess,
}: {
  startOptions?: StartOAuthReq;
  onSuccess: () => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const [tab, setTab] = useState<AntigravityImportTab>('authorize');
  const [refreshToken, setRefreshToken] = useState('');
  const [saving, setSaving] = useState(false);
  const [importError, setImportError] = useState('');

  useEffect(() => {
    setImportError('');
  }, [tab]);

  const submitRefresh = async () => {
    if (!refreshToken.trim()) {
      toast('error', t('accounts.antigravity_rt_required'));
      return;
    }
    setSaving(true);
    setImportError('');
    try {
      const account = await accountsApi.antigravityImportRefresh({
        refresh_token: refreshToken.trim(),
        ...(startOptions ?? {}),
      });
      const successKey = startOptions?.account_id
        ? 'accounts.oauth_reauth_success'
        : 'accounts.oauth_success';
      toast('success', t(successKey, { name: account.name || account.id }));
      onSuccess();
    } catch (err) {
      setImportError(
        err instanceof Error && err.message
          ? err.message
          : t('accounts.oauth_failed'),
      );
    } finally {
      setSaving(false);
    }
  };

  const tabs: Array<{ id: AntigravityImportTab; label: string }> = [
    { id: 'authorize', label: t('accounts.antigravity_tab_authorize') },
    { id: 'refresh', label: t('accounts.antigravity_tab_refresh') },
  ];

  return (
    <div className="space-y-3">
      <div className="inline-flex flex-wrap rounded-lg border border-border bg-surface p-0.5 text-sm">
        {tabs.map((item) => (
          <button
            key={item.id}
            type="button"
            className={`rounded-md px-2.5 py-1.5 font-medium transition ${
              tab === item.id
                ? 'bg-primary text-text-inverse'
                : 'text-text-secondary hover:text-text'
            }`}
            onClick={() => setTab(item.id)}
          >
            {item.label}
          </button>
        ))}
      </div>

      {tab === 'authorize' ? (
        <OAuthAuthPanel
          platform="antigravity"
          startOptions={startOptions}
          onSuccess={onSuccess}
        />
      ) : null}

      {tab === 'refresh' ? (
        <div className="space-y-3">
          <Description className="text-sm text-default-500">
            {t('accounts.antigravity_rt_hint')}
          </Description>
          <HeroTextField fullWidth>
            <Label>{t('accounts.refresh_token')}</Label>
            <TextArea
              value={refreshToken}
              onChange={(event) => setRefreshToken(event.target.value)}
              placeholder={t('accounts.refresh_token_placeholder')}
              rows={4}
              className="font-mono text-xs"
            />
          </HeroTextField>
          <Button
            variant="primary"
            onPress={submitRefresh}
            isDisabled={saving || !refreshToken.trim()}
          >
            {saving ? <Spinner size="sm" /> : t('accounts.antigravity_import')}
          </Button>
        </div>
      ) : null}

      {importError ? (
        <div className="whitespace-pre-wrap break-words rounded-lg border border-danger/30 bg-danger/5 p-3 text-sm text-danger">
          {importError}
        </div>
      ) : null}
    </div>
  );
}
