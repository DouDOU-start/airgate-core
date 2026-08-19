import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Description,
  Label,
  Spinner,
  TextArea,
  TextField as HeroTextField,
} from '@heroui/react';
import { Copy, ExternalLink } from 'lucide-react';
import { accountsApi } from '../../../shared/api/accounts';
import { useToast } from '../../../shared/ui';
import type { AccountPlatform, OAuthSessionResp, StartOAuthReq } from '../../../shared/types';

/** 支持交互式 OAuth 授权的平台。 */
export const INTERACTIVE_OAUTH_PLATFORMS: AccountPlatform[] = [
  'codex',
  'claude',
  'antigravity',
  'kimi',
  'xai',
  'cursor',
];

export function supportsInteractiveOAuth(platform: string): boolean {
  return INTERACTIVE_OAUTH_PLATFORMS.includes(platform as AccountPlatform);
}

export function OAuthAuthPanel({
  platform,
  startOptions,
  disabled,
  onSuccess,
}: {
  platform: AccountPlatform;
  /** 传给 oauth/start 的调度/名称等可选字段。 */
  startOptions?: StartOAuthReq;
  disabled?: boolean;
  onSuccess: () => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();

  const [session, setSession] = useState<OAuthSessionResp | null>(null);
  const [starting, setStarting] = useState(false);
  const [pasteCode, setPasteCode] = useState('');
  const [completing, setCompleting] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const stopPoll = () => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  };

  useEffect(() => () => stopPoll(), []);

  // 平台切换时清空会话
  useEffect(() => {
    setSession(null);
    setPasteCode('');
    setStarting(false);
    setCompleting(false);
    stopPoll();
  }, [platform]);

  const isReauth = !!(startOptions?.account_id && startOptions.account_id > 0);

  const onSessionUpdate = (next: OAuthSessionResp) => {
    setSession(next);
    if (next.status === 'completed') {
      stopPoll();
      toast(
        'success',
        t(isReauth ? 'accounts.oauth_reauth_success' : 'accounts.oauth_success', {
          name: next.account_name || next.account_id,
        }),
      );
      onSuccess();
    } else if (next.status === 'failed') {
      stopPoll();
      toast('error', next.error || t('accounts.oauth_failed'));
    }
  };

  const openURL = (url?: string) => {
    if (!url) return;
    window.open(url, '_blank', 'noopener,noreferrer');
  };

  const startOAuth = async () => {
    setStarting(true);
    setSession(null);
    setPasteCode('');
    stopPoll();
    try {
      const s = await accountsApi.oauthStart(platform, startOptions ?? {});
      onSessionUpdate(s);
      if (s.status === 'pending' && s.flow === 'device') {
        pollRef.current = setInterval(async () => {
          try {
            const next = await accountsApi.oauthSession(s.id);
            onSessionUpdate(next);
          } catch {
            // keep polling
          }
        }, 2000);
      }
    } catch (err) {
      toast('error', err instanceof Error ? err.message : t('accounts.oauth_failed'));
    } finally {
      setStarting(false);
    }
  };

  const completePasteCode = async () => {
    if (!session?.id || !pasteCode.trim()) return;
    setCompleting(true);
    try {
      const next = await accountsApi.oauthComplete(session.id, pasteCode.trim());
      onSessionUpdate(next);
    } catch (err) {
      toast('error', err instanceof Error ? err.message : t('accounts.oauth_failed'));
    } finally {
      setCompleting(false);
    }
  };

  const copyText = async (text?: string, successKey = 'accounts.oauth_copied') => {
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      toast('success', t(successKey));
    } catch {
      toast('error', t('accounts.oauth_copy_failed'));
    }
  };

  const pending = session?.status === 'pending';
  const isPasteCode = session?.flow === 'paste_code';
  const isDevice = session?.flow === 'device';

  return (
    <div className="space-y-3">
      <Description className="text-sm text-default-500">
        {t('accounts.oauth_intro')}
      </Description>

      <Button
        variant="primary"
        size="sm"
        onPress={startOAuth}
        isDisabled={disabled || starting}
      >
        {starting ? <Spinner size="sm" /> : null}
        {session?.authorize_url ? t('accounts.oauth_regenerate') : t('accounts.oauth_start')}
      </Button>

      {session?.authorize_url ? (
        <div className="rounded-lg border border-primary/30 bg-primary/5 p-3 space-y-3">
          <Description className="text-sm">
            {session.message || t('accounts.oauth_open_link_hint')}
          </Description>

          {session.user_code ? (
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <span className="text-default-500">{t('accounts.oauth_user_code')}:</span>
              <code className="text-lg font-semibold tracking-wider px-2 py-0.5 rounded bg-default-100">
                {session.user_code}
              </code>
              <Button
                variant="secondary"
                size="sm"
                onPress={() => copyText(session.user_code, 'accounts.oauth_code_copied')}
              >
                <Copy className="w-3.5 h-3.5" />
                {t('accounts.oauth_copy_code')}
              </Button>
            </div>
          ) : null}

          <div className="flex flex-wrap gap-2">
            <Button
              variant="primary"
              size="sm"
              onPress={() => openURL(session.authorize_url)}
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

          <div className="break-all rounded bg-default-100 p-2 font-mono text-xs text-default-700 select-all">
            {session.authorize_url}
          </div>
        </div>
      ) : null}

      {isPasteCode && pending ? (
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
            onPress={completePasteCode}
            isDisabled={completing || !pasteCode.trim()}
          >
            {completing ? <Spinner size="sm" /> : t('accounts.oauth_submit_code')}
          </Button>
        </div>
      ) : null}

      {isDevice && pending ? (
        <div className="flex items-center gap-2 text-sm text-default-500">
          <Spinner size="sm" />
          {t('accounts.oauth_waiting')}
        </div>
      ) : null}

      {session?.error ? (
        <div className="text-sm text-danger">{session.error}</div>
      ) : null}
    </div>
  );
}
