import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Spinner } from '@heroui/react';
import { ShieldCheck } from 'lucide-react';
import { oauthApi } from '../shared/api/oauth';
import { ApiError, getToken } from '../shared/api/client';
import { queryKeys } from '../shared/queryKeys';
import { useSiteSettings, defaultLogoUrl } from '../app/providers/SiteSettingsProvider';

// OAuth 授权页（/oauth/authorize，SPA 路由）。
// 外部应用把浏览器重定向到这里；本页校验登录态后把授权参数转发给后端签发授权码，
// 再携带 code+state 回跳应用。第一方应用静默通过，第三方展示确认页。
interface AuthorizeParams {
  clientId: string;
  redirectUri: string;
  scope: string;
  state: string;
  codeChallenge: string;
  codeChallengeMethod: string;
}

function parseParams(): AuthorizeParams {
  const q = new URLSearchParams(window.location.search);
  return {
    clientId: q.get('client_id') ?? '',
    redirectUri: q.get('redirect_uri') ?? '',
    scope: q.get('scope') ?? '',
    state: q.get('state') ?? '',
    codeChallenge: q.get('code_challenge') ?? '',
    codeChallengeMethod: q.get('code_challenge_method') ?? '',
  };
}

// 回跳地址追加查询参数（redirect_uri 已在后端白名单校验过）
function appendQuery(base: string, params: Record<string, string>): string {
  const url = new URL(base);
  for (const [key, value] of Object.entries(params)) {
    if (value) url.searchParams.set(key, value);
  }
  return url.toString();
}

function AppIcon({ icon }: { icon: string }) {
  if (icon.startsWith('http://') || icon.startsWith('https://')) {
    return <img alt="" className="h-12 w-12 rounded-xl" src={icon} />;
  }
  if (icon) {
    return <span className="text-4xl leading-none">{icon}</span>;
  }
  return null;
}

export default function OAuthAuthorizePage() {
  const { t } = useTranslation();
  const { site_logo: siteLogo } = useSiteSettings();
  const params = useMemo(parseParams, []);
  const [denied, setDenied] = useState(false);
  const autoFired = useRef(false);

  const hasToken = !!getToken();
  const paramsValid =
    params.clientId !== '' &&
    params.redirectUri !== '' &&
    params.codeChallenge !== '' &&
    params.codeChallengeMethod === 'S256';

  // 未登录：带回跳地址去登录页
  useEffect(() => {
    if (!hasToken) {
      const target = window.location.pathname + window.location.search;
      window.location.replace(`/login?redirect=${encodeURIComponent(target)}`);
    }
  }, [hasToken]);

  const infoQuery = useQuery({
    queryKey: queryKeys.oauthAuthorizeInfo(params.clientId, params.redirectUri, params.scope),
    queryFn: () => oauthApi.authorizeInfo(params.clientId, params.redirectUri, params.scope),
    enabled: hasToken && paramsValid,
    retry: false,
  });

  const authorizeMutation = useMutation({
    mutationFn: () =>
      oauthApi.authorize({
        client_id: params.clientId,
        redirect_uri: params.redirectUri,
        scope: params.scope,
        state: params.state,
        code_challenge: params.codeChallenge,
        code_challenge_method: params.codeChallengeMethod,
      }),
    onSuccess: (resp) => {
      window.location.replace(
        appendQuery(params.redirectUri, { code: resp.code, state: resp.state }),
      );
    },
  });

  // 第一方应用：拿到授权信息后静默签发授权码。
  // deps 用 mutate（useMutation 返回的 mutate 引用稳定）而非 mutation 对象，
  // 避免 mutation 状态每次变化都重跑 effect。
  const info = infoQuery.data;
  const { mutate: fireAuthorize } = authorizeMutation;
  useEffect(() => {
    if (info?.first_party && !autoFired.current) {
      autoFired.current = true;
      fireAuthorize();
    }
  }, [info, fireAuthorize]);

  const handleDeny = () => {
    setDenied(true);
    window.location.replace(
      appendQuery(params.redirectUri, { error: 'access_denied', state: params.state }),
    );
  };

  const errorMessage = !paramsValid
    ? t('oauth_authorize.invalid_request')
    : infoQuery.error instanceof ApiError
      ? infoQuery.error.message
      : authorizeMutation.error instanceof ApiError
        ? authorizeMutation.error.message
        : infoQuery.error || authorizeMutation.error
          ? t('oauth_authorize.failed')
          : '';

  const busy =
    !hasToken ||
    denied ||
    (paramsValid && infoQuery.isLoading) ||
    authorizeMutation.isPending ||
    authorizeMutation.isSuccess ||
    Boolean(info?.first_party && !authorizeMutation.isError);

  return (
    <div className="flex min-h-screen items-center justify-center bg-surface-secondary p-4">
      <Card className="w-full max-w-md">
        <Card.Content>
          <div className="flex flex-col items-center gap-4 py-6 text-center">
            <img alt="" className="h-10 w-10" src={siteLogo || defaultLogoUrl} />
            {errorMessage ? (
              <>
                <Alert status="danger">{errorMessage}</Alert>
                <Button variant="secondary" onPress={() => window.location.replace('/')}>
                  {t('oauth_authorize.back_home')}
                </Button>
              </>
            ) : busy || !info ? (
              <>
                <Spinner size="lg" />
                <div className="text-sm text-text-tertiary">{t('oauth_authorize.redirecting')}</div>
              </>
            ) : (
              <>
                <AppIcon icon={info.icon} />
                <div>
                  <div className="text-lg font-semibold text-text">
                    {t('oauth_authorize.consent_title', { name: info.name })}
                  </div>
                  {info.description ? (
                    <div className="mt-1 text-sm text-text-tertiary">{info.description}</div>
                  ) : null}
                </div>
                <div className="w-full rounded-lg bg-surface-secondary p-3 text-left">
                  <div className="mb-2 text-xs font-medium text-text-secondary">
                    {t('oauth_authorize.scope_title')}
                  </div>
                  <div className="space-y-2">
                    {info.scopes.map((scope) => (
                      <div key={scope} className="flex items-center gap-2 text-xs text-text-secondary">
                        <ShieldCheck className="h-4 w-4 shrink-0 text-success" />
                        {t(scopeTranslationKey(scope))}
                      </div>
                    ))}
                  </div>
                </div>
                <div className="flex w-full gap-3">
                  <Button className="flex-1" variant="secondary" onPress={handleDeny}>
                    {t('oauth_authorize.deny')}
                  </Button>
                  <Button
                    className="flex-1"
                    variant="primary"
                    onPress={() => authorizeMutation.mutate()}
                  >
                    {t('oauth_authorize.approve')}
                  </Button>
                </div>
              </>
            )}
          </div>
        </Card.Content>
      </Card>
    </div>
  );
}

function scopeTranslationKey(scope: string): string {
  const keys: Record<string, string> = {
    profile: 'oauth_authorize.scope_profile',
    'wallet.read': 'oauth_authorize.scope_wallet_read',
    'wallet.debit': 'oauth_authorize.scope_wallet_debit',
    'wallet.refund': 'oauth_authorize.scope_wallet_refund',
    'payment.read': 'oauth_authorize.scope_payment_read',
    'payment.create': 'oauth_authorize.scope_payment_create',
  };
  return keys[scope] ?? scope;
}
