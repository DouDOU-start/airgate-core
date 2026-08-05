import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Description,
  Input,
  Label,
  Spinner,
  TextArea,
  TextField as HeroTextField,
} from '@heroui/react';
import { Copy, ExternalLink } from 'lucide-react';
import { accountsApi } from '../../../shared/api/accounts';
import { useToast } from '../../../shared/ui';
import type { OAuthSessionResp, StartOAuthReq } from '../../../shared/types';

/** Codex OAuth 导入方式：浏览器授权 / RT / Session（已移除设备码）。 */
export type CodexImportTab = 'authorize' | 'refresh' | 'session';

export function CodexImportPanel({
  startOptions,
  onSuccess,
}: {
  startOptions?: StartOAuthReq;
  onSuccess: () => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();

  const [tab, setTab] = useState<CodexImportTab>('authorize');
  const [saving, setSaving] = useState(false);

  // browser authorize
  const [session, setSession] = useState<OAuthSessionResp | null>(null);
  const [starting, setStarting] = useState(false);
  const [pasteCode, setPasteCode] = useState('');
  const [completing, setCompleting] = useState(false);

  // refresh
  const [refreshToken, setRefreshToken] = useState('');
  const [clientId, setClientId] = useState('');

  // session
  const [sessionRaw, setSessionRaw] = useState('');

  // 导入/授权失败原文（多行上游 JSON 在面板内 pre-wrap 展示）
  const [importError, setImportError] = useState('');

  useEffect(() => {
    setSession(null);
    setPasteCode('');
    setImportError('');
  }, [tab]);

  const baseOpts = (): StartOAuthReq => ({ ...(startOptions ?? {}) });
  const isReauth = !!(startOptions?.account_id && startOptions.account_id > 0);
  const successKey = isReauth ? 'accounts.oauth_reauth_success' : 'accounts.oauth_success';

  const showImportError = (err: unknown, fallback: string) => {
    const msg = err instanceof Error && err.message ? err.message : fallback;
    setImportError(msg);
  };

  const onSessionDone = (next: OAuthSessionResp) => {
    if (next.status === 'completed') {
      setImportError('');
      toast(
        'success',
        t(successKey, { name: next.account_name || next.account_id }),
      );
      onSuccess();
    } else if (next.status === 'failed') {
      showImportError(new Error(next.error || ''), t('accounts.oauth_failed'));
    }
  };

  const copyText = async (text?: string) => {
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      toast('success', t('accounts.oauth_copied'));
    } catch {
      toast('error', t('accounts.oauth_copy_failed'));
    }
  };

  // ── 浏览器授权 ──
  const startBrowser = async () => {
    setStarting(true);
    setSession(null);
    setPasteCode('');
    setImportError('');
    try {
      const s = await accountsApi.oauthStart('codex', { ...baseOpts(), mode: 'browser' });
      setSession(s);
      onSessionDone(s);
    } catch (err) {
      showImportError(err, t('accounts.oauth_failed'));
    } finally {
      setStarting(false);
    }
  };

  const completeBrowser = async () => {
    if (!session?.id || !pasteCode.trim()) return;
    setCompleting(true);
    setImportError('');
    try {
      const next = await accountsApi.oauthComplete(session.id, pasteCode.trim());
      setSession(next);
      onSessionDone(next);
    } catch (err) {
      showImportError(err, t('accounts.oauth_failed'));
    } finally {
      setCompleting(false);
    }
  };

  // ── RT ──
  const submitRefresh = async () => {
    if (!refreshToken.trim()) {
      toast('error', t('accounts.codex_rt_required'));
      return;
    }
    setSaving(true);
    setImportError('');
    try {
      const acc = await accountsApi.codexImportRefresh({
        refresh_token: refreshToken.trim(),
        client_id: clientId.trim() || undefined,
        ...baseOpts(),
      });
      setImportError('');
      toast('success', t(successKey, { name: acc.name || acc.id }));
      onSuccess();
    } catch (err) {
      showImportError(err, t('accounts.oauth_failed'));
    } finally {
      setSaving(false);
    }
  };

  // ── Session ──
  const submitSession = async () => {
    if (!sessionRaw.trim()) {
      toast('error', t('accounts.codex_session_required'));
      return;
    }
    setSaving(true);
    setImportError('');
    try {
      const acc = await accountsApi.codexImportSession({
        session: sessionRaw.trim(),
        ...baseOpts(),
      });
      setImportError('');
      toast('success', t(successKey, { name: acc.name || acc.id }));
      onSuccess();
    } catch (err) {
      showImportError(err, t('accounts.oauth_failed'));
    } finally {
      setSaving(false);
    }
  };

  const tabs: Array<{ id: CodexImportTab; label: string }> = [
    { id: 'authorize', label: t('accounts.codex_tab_authorize') },
    { id: 'refresh', label: t('accounts.codex_tab_refresh') },
    { id: 'session', label: t('accounts.codex_tab_session') },
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
        <div className="space-y-3">
          <Description className="text-sm text-default-500">
            {t('accounts.codex_authorize_hint')}
          </Description>
          <Button variant="primary" size="sm" onPress={startBrowser} isDisabled={starting}>
            {starting ? <Spinner size="sm" /> : null}
            {session?.authorize_url
              ? t('accounts.oauth_regenerate')
              : t('accounts.oauth_start')}
          </Button>
          {session?.authorize_url ? (
            <div className="rounded-lg border border-primary/30 bg-primary/5 p-3 space-y-3">
              <div className="flex flex-wrap gap-2">
                <Button
                  variant="primary"
                  size="sm"
                  onPress={() =>
                    window.open(session.authorize_url, '_blank', 'noopener,noreferrer')
                  }
                >
                  <ExternalLink className="w-3.5 h-3.5" />
                  {t('accounts.oauth_open_link')}
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  onPress={() => copyText(session.authorize_url)}
                >
                  <Copy className="w-3.5 h-3.5" />
                  {t('accounts.oauth_copy_link')}
                </Button>
              </div>
              <div className="break-all rounded bg-default-100 p-2 font-mono text-xs select-all">
                {session.authorize_url}
              </div>
              {session.status === 'pending' ? (
                <div className="space-y-2">
                  <HeroTextField fullWidth>
                    <Label>{t('accounts.oauth_paste_code')}</Label>
                    <TextArea
                      value={pasteCode}
                      onChange={(e) => setPasteCode(e.target.value)}
                      placeholder={t('accounts.oauth_paste_code_hint')}
                      rows={3}
                    />
                  </HeroTextField>
                  <Description className="text-xs text-default-500">
                    {t('accounts.oauth_paste_code_help')}
                  </Description>
                  <Button
                    variant="primary"
                    onPress={completeBrowser}
                    isDisabled={completing || !pasteCode.trim()}
                  >
                    {completing ? <Spinner size="sm" /> : t('accounts.oauth_submit_code')}
                  </Button>
                </div>
              ) : null}
              {session.error ? (
                <div className="text-sm text-danger">{session.error}</div>
              ) : null}
            </div>
          ) : null}
        </div>
      ) : null}

      {tab === 'refresh' ? (
        <div className="space-y-3">
          <Description className="text-sm text-default-500">
            {t('accounts.codex_rt_hint')}
          </Description>
          <HeroTextField fullWidth>
            <Label>{t('accounts.refresh_token')}</Label>
            <TextArea
              value={refreshToken}
              onChange={(e) => setRefreshToken(e.target.value)}
              placeholder={t('accounts.refresh_token_placeholder')}
              rows={3}
            />
          </HeroTextField>
          <HeroTextField fullWidth>
            <Label>{t('accounts.codex_client_id_optional')}</Label>
            <Input
              value={clientId}
              onChange={(e) => setClientId(e.target.value)}
              placeholder="app_EMoamEEZ73f0CkXaXp7hrann"
              className="font-mono text-xs"
            />
          </HeroTextField>
          <Button variant="primary" onPress={submitRefresh} isDisabled={saving}>
            {saving ? <Spinner size="sm" /> : null}
            {t('accounts.codex_import')}
          </Button>
        </div>
      ) : null}

      {tab === 'session' ? (
        <div className="space-y-3">
          <Description className="text-sm text-default-500">
            {t('accounts.codex_session_hint')}
          </Description>
          <HeroTextField fullWidth>
            <Label>{t('accounts.codex_session')}</Label>
            <TextArea
              value={sessionRaw}
              onChange={(e) => setSessionRaw(e.target.value)}
              placeholder={t('accounts.codex_session_placeholder')}
              rows={5}
            />
          </HeroTextField>
          <Button variant="primary" onPress={submitSession} isDisabled={saving}>
            {saving ? <Spinner size="sm" /> : null}
            {t('accounts.codex_import')}
          </Button>
        </div>
      ) : null}

      {importError ? (
        <pre className="max-h-56 overflow-auto whitespace-pre-wrap break-all rounded-lg border border-danger/30 bg-danger/5 p-3 font-mono text-xs text-danger">
          {importError}
        </pre>
      ) : null}
    </div>
  );
}
