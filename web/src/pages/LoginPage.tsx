import { useEffect, useState } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, FieldError, Form, Input, Label, Tabs, TextField as HeroTextField } from '@heroui/react';
import { useAuth } from '../app/providers/AuthProvider';
import { useSiteSettings, defaultLogoUrl } from '../app/providers/SiteSettingsProvider';
import { authApi } from '../shared/api/auth';
import { useTheme } from '../app/providers/ThemeProvider';
import { ApiError, setSessionAPIKey } from '../shared/api/client';
import { Mail, Lock, User, ArrowRight, Sun, Moon, ShieldCheck, Key } from 'lucide-react';

type TabKey = 'login' | 'register' | 'apikey';

// consumeLoginRedirect 读取 ?redirect= 回跳地址（OAuth 授权页等场景）。
// 仅允许站内相对路径，防开放重定向；带查询串所以用 location 跳转而非 router navigate。
function consumeLoginRedirect(): string | null {
  const raw = new URLSearchParams(window.location.search).get('redirect');
  if (raw && raw.startsWith('/') && !raw.startsWith('//')) return raw;
  return null;
}

/* ==================== 登录表单 ==================== */

function LoginForm() {
  const navigate = useNavigate();
  const { login } = useAuth();
  const { t } = useTranslation();

  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError('');

    try {
      const resp = await authApi.login({ email, password });
      login(resp.token, resp.user);
      const redirect = consumeLoginRedirect();
      if (redirect) {
        window.location.assign(redirect);
      } else {
        navigate({ to: '/' });
      }
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
      } else {
        setError(t('auth.login_failed'));
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <Form onSubmit={handleSubmit} className="space-y-4">
      <HeroTextField fullWidth isRequired>
        <Label>{t('auth.email')}</Label>
        <div className="relative">
          <Mail className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
          <Input
            className="pl-9"
            name="email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder={t('auth.email_placeholder')}
            autoComplete="username"
            autoFocus
            required
          />
        </div>
      </HeroTextField>
      <HeroTextField fullWidth isRequired>
        <Label>{t('auth.password')}</Label>
        <div className="relative">
          <Lock className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
          <Input
            className="pl-9"
            name="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder={t('auth.password_placeholder')}
            autoComplete="current-password"
            required
          />
        </div>
      </HeroTextField>
      {error && (
        <Alert status="danger">
          <Alert.Content>
            <Alert.Description>{error}</Alert.Description>
          </Alert.Content>
        </Alert>
      )}
      <Button type="submit" isDisabled={loading} className="w-full h-11" variant="primary" aria-busy={loading}>
        <ArrowRight className="w-4 h-4" />
        {t('common.login')}
      </Button>
    </Form>
  );
}

/* ==================== 注册表单 ==================== */

function RegisterForm({ onSuccess }: { onSuccess: () => void }) {
  const { t } = useTranslation();
  const site = useSiteSettings();
  const settingsReady = site.settings_loaded;
  const needVerify = site.email_verify_enabled;

  const [step, setStep] = useState<1 | 2>(1);
  const [email, setEmail] = useState('');
  const [verifyCode, setVerifyCode] = useState('');
  const [verifiedEmail, setVerifiedEmail] = useState('');
  const [verifiedCode, setVerifiedCode] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [sendingCode, setSendingCode] = useState(false);
  const [codeSent, setCodeSent] = useState(false);
  const [countdown, setCountdown] = useState(0);
  const [error, setError] = useState('');

  const passwordMismatch = confirmPassword !== '' && password !== confirmPassword;

  const resetVerifiedEmail = () => {
    setVerifiedEmail('');
    setVerifiedCode('');
  };

  // 倒计时
  useEffect(() => {
    if (countdown <= 0) return;
    const timer = window.setInterval(() => {
      setCountdown((c) => (c <= 1 ? 0 : c - 1));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [countdown]);

  useEffect(() => {
    if (settingsReady && needVerify && step === 2 && (!verifiedEmail || !verifiedCode)) {
      setStep(1);
    }
  }, [needVerify, settingsReady, step, verifiedCode, verifiedEmail]);

  // 发送验证码
  const handleSendCode = async () => {
    const normalizedEmail = email.trim();
    if (!normalizedEmail) { setError(t('auth.email_required')); return; }
    setSendingCode(true);
    setError('');
    resetVerifiedEmail();
    try {
      await authApi.sendVerifyCode(normalizedEmail);
      setEmail(normalizedEmail);
      setVerifyCode('');
      setCodeSent(true);
      setCountdown(60);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t('auth.send_code_failed'));
    } finally {
      setSendingCode(false);
    }
  };

  // 第一步：验证邮箱 → 进入第二步
  const handleStep1 = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!settingsReady) return;

    const normalizedEmail = email.trim();
    const normalizedCode = verifyCode.trim();
    if (!normalizedEmail) { setError(t('auth.email_required')); return; }

    if (!needVerify) {
      setEmail(normalizedEmail);
      setStep(2);
      return;
    }
    if (!normalizedCode) { setError(t('auth.code_required')); return; }
    setLoading(true);
    setError('');
    try {
      await authApi.verifyCode(normalizedEmail, normalizedCode);
      setEmail(normalizedEmail);
      setVerifyCode(normalizedCode);
      setVerifiedEmail(normalizedEmail);
      setVerifiedCode(normalizedCode);
      setStep(2);
    } catch (err) {
      resetVerifiedEmail();
      setError(err instanceof ApiError ? err.message : t('auth.register_failed'));
    } finally {
      setLoading(false);
    }
  };

  // 第二步：提交注册
  const handleStep2 = async (e: React.FormEvent) => {
    e.preventDefault();
    if (password !== confirmPassword) { setError(t('auth.password_mismatch')); return; }
    if (password.length < 8) { setError(t('auth.password_too_short')); return; }

    const registrationEmail = needVerify ? verifiedEmail : email.trim();
    if (needVerify && (!verifiedEmail || !verifiedCode || email.trim() !== verifiedEmail)) {
      setStep(1);
      setError(t('auth.email_verification_required'));
      return;
    }

    setLoading(true);
    setError('');
    try {
      await authApi.register({
        email: registrationEmail,
        password,
        username: username || undefined,
        verify_code: needVerify ? verifiedCode : undefined,
      });
      onSuccess();
    } catch (err) {
      if (err instanceof ApiError) {
        // 验证码错误则回到第一步。
        // 脆弱性说明：后端注册接口对验证码无效/缺失只回 HTTP 400 + 中文 message，
        // 没有可区分的业务 code（见 backend handleRegisterError），前端只能按文案
        // 关键字匹配；后端若增设错误码或改文案，此分支需同步调整。
        if (err.message.includes('验证码')) {
          setStep(1);
          setVerifyCode('');
          resetVerifiedEmail();
        }
        setError(err.message);
      } else {
        setError(t('auth.register_failed'));
      }
    } finally {
      setLoading(false);
    }
  };

  // 第一步：输入邮箱（+ 验证码）
  if (step === 1) {
    return (
      <Form onSubmit={handleStep1} className="space-y-4">
        <HeroTextField fullWidth isRequired>
          <Label>{t('auth.email')}</Label>
          <div className="relative">
            <Mail className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
            <Input
              className="pl-9"
              name="email"
              type="email"
              value={email}
              onChange={(e) => {
                setEmail(e.target.value);
                setError('');
                setCodeSent(false);
                setCountdown(0);
                resetVerifiedEmail();
              }}
              placeholder={t('auth.email_placeholder')}
              autoComplete="email"
              autoFocus
              required
            />
          </div>
        </HeroTextField>
        {needVerify && (
          <div className="flex items-end gap-2">
            <HeroTextField fullWidth isRequired>
              <Label>{t('auth.verify_code')}</Label>
              <div className="relative">
                <ShieldCheck className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
                <Input
                  className="pl-9"
                  name="verify_code"
                  value={verifyCode}
                  onChange={(e) => {
                    setVerifyCode(e.target.value);
                    setError('');
                    resetVerifiedEmail();
                  }}
                  placeholder={t('auth.verify_code_placeholder')}
                  maxLength={6}
                  required
                />
              </div>
            </HeroTextField>
            <Button
              type="button"
              variant="secondary"
              onPress={handleSendCode}
              isDisabled={sendingCode || countdown > 0 || !email.trim() || !settingsReady}
              className="shrink-0 h-[42px]"
              aria-busy={sendingCode}
            >
              {countdown > 0 ? `${countdown}s` : codeSent ? t('auth.resend_code') : t('auth.send_code')}
            </Button>
          </div>
        )}
        {error && (
          <Alert status="danger">
            <Alert.Content>
              <Alert.Description>{error}</Alert.Description>
            </Alert.Content>
          </Alert>
        )}
        <Button type="submit" isDisabled={loading || !settingsReady} className="w-full h-11" variant="primary" aria-busy={loading}>
          <ArrowRight className="w-4 h-4" />
          {t('auth.next_step')}
        </Button>
      </Form>
    );
  }

  // 第二步：填写密码等信息
  return (
    <Form onSubmit={handleStep2} className="space-y-4">
      {/* 已验证的邮箱（只读展示） */}
      <div className="flex items-center gap-2 px-3.5 py-2.5 rounded-[10px] border border-glass-border bg-surface text-sm text-text-secondary">
        <Mail className="w-4 h-4 text-text-tertiary shrink-0" />
        <span className="truncate">{needVerify ? verifiedEmail : email}</span>
        <Button
          className="ml-auto shrink-0"
          size="sm"
          variant="ghost"
          onPress={() => setStep(1)}
        >
          {t('auth.change_email')}
        </Button>
      </div>
      <HeroTextField fullWidth>
        <Label>{t('auth.username')}</Label>
        <div className="relative">
          <User className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
          <Input
            className="pl-9"
            name="username"
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder={t('auth.username_placeholder')}
            autoFocus
          />
        </div>
      </HeroTextField>
      <HeroTextField fullWidth isRequired>
        <Label>{t('auth.password')}</Label>
        <div className="relative">
          <Lock className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
          <Input
            className="pl-9"
            name="new-password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder={t('auth.password_hint')}
            autoComplete="new-password"
            required
          />
        </div>
      </HeroTextField>
      <HeroTextField fullWidth isInvalid={passwordMismatch} isRequired>
        <Label>{t('auth.confirm_password')}</Label>
        <div className="relative">
          <Lock className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
          <Input
            className="pl-9"
            name="confirm-new-password"
            type="password"
            value={confirmPassword}
            onChange={(e) => setConfirmPassword(e.target.value)}
            placeholder={t('auth.confirm_placeholder')}
            autoComplete="new-password"
            aria-invalid={passwordMismatch || undefined}
            required
          />
        </div>
        {passwordMismatch ? <FieldError>{t('auth.password_mismatch')}</FieldError> : null}
      </HeroTextField>
      {error && (
        <Alert status="danger">
          <Alert.Content>
            <Alert.Description>{error}</Alert.Description>
          </Alert.Content>
        </Alert>
      )}
      <Button type="submit" isDisabled={loading} className="w-full h-11" variant="primary" aria-busy={loading}>
        {t('common.register')}
      </Button>
    </Form>
  );
}

/* ==================== API Key 登录表单 ==================== */

function APIKeyLoginForm() {
  const navigate = useNavigate();
  const { login } = useAuth();
  const { t } = useTranslation();

  const [apiKey, setApiKey] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError('');

    try {
      const resp = await authApi.loginByAPIKey({ key: apiKey });
      // 把用户输入的原文 Key 暂存到内存变量（不落存储），供 CCS 导入等需要原文的功能使用。
      setSessionAPIKey(apiKey);
      login(resp.token, { ...resp.user, api_key_id: resp.api_key_id, api_key_name: resp.api_key_name });
      const redirect = consumeLoginRedirect();
      if (redirect) {
        window.location.assign(redirect);
      } else {
        navigate({ to: '/' });
      }
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
      } else {
        setError(t('auth.login_failed'));
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <Form onSubmit={handleSubmit} className="space-y-4">
      <HeroTextField fullWidth isRequired>
        <Label>API Key</Label>
        <div className="relative">
          <Key className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
          <Input
            className="pl-9"
            name="api_key"
            type="password"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder="sk-..."
            autoComplete="off"
            autoFocus
            required
          />
        </div>
      </HeroTextField>
      <p className="text-[11px] text-text-tertiary">{t('auth.apikey_login_hint')}</p>
      {error && (
        <Alert status="danger">
          <Alert.Content>
            <Alert.Description>{error}</Alert.Description>
          </Alert.Content>
        </Alert>
      )}
      <Button type="submit" isDisabled={loading} className="w-full h-11" variant="primary" aria-busy={loading}>
        <ArrowRight className="w-4 h-4" />
        {t('common.login')}
      </Button>
    </Form>
  );
}

/* ==================== 登录页主组件 ==================== */

export default function LoginPage() {
  const { t } = useTranslation();
  const { theme, toggleTheme } = useTheme();
  const site = useSiteSettings();
  const [activeTab, setActiveTab] = useState<TabKey>('login');
  const [registerSuccess, setRegisterSuccess] = useState(false);

  const handleRegisterSuccess = () => {
    setRegisterSuccess(true);
    setActiveTab('login');
  };

  return (
    <div className="min-h-screen flex relative overflow-hidden bg-bg-deep text-text">
      {/* ===== 左侧装饰面板（桌面端） ===== */}
      <div
        className="hidden lg:flex lg:w-[45%] xl:w-[50%] relative items-center justify-center overflow-hidden"
        style={{
          background: theme === 'dark'
            ? 'radial-gradient(circle at 25% 35%, oklch(29% 0.018 250), transparent 32%), linear-gradient(135deg, oklch(18% 0.012 250), oklch(12% 0.006 250))'
            : 'radial-gradient(circle at 25% 35%, oklch(34% 0.025 250), transparent 34%), linear-gradient(135deg, oklch(25% 0.018 250), oklch(16% 0.01 250))',
          color: 'oklch(96% 0.004 250)',
        }}
      >
        <div
          className="pointer-events-none absolute -left-28 -top-28 h-72 w-72 rounded-full blur-3xl"
          style={{ background: theme === 'dark' ? 'rgba(255,255,255,0.05)' : 'rgba(255,255,255,0.07)' }}
        />
        <div
          className="pointer-events-none absolute -bottom-32 right-10 h-80 w-80 rounded-full blur-3xl"
          style={{ background: theme === 'dark' ? 'rgba(255,255,255,0.035)' : 'rgba(255,255,255,0.05)' }}
        />
        {/* 内容 */}
        <div className="relative z-10 px-12 max-w-md">
          <div className="flex items-center gap-3 mb-8">
            <img src={site.site_logo || defaultLogoUrl} alt="" className="w-10 h-10 rounded-sm object-cover" />
            <span className="text-xl font-bold">{site.site_name || 'AirGate'}</span>
          </div>
          <h2 className="text-3xl font-bold leading-snug mb-4">
            {t('auth.welcome_title')}
          </h2>
          <p className="text-sm leading-relaxed opacity-65">
            {t('auth.welcome_desc')}
          </p>
          <div className="flex gap-3 mt-10">
            {[t('auth.feature_1'), t('auth.feature_2'), t('auth.feature_3')].map((f) => (
              <span
                key={f}
                className="text-[11px] px-3 py-1.5 rounded-[var(--radius)] font-medium border"
                style={{
                  background: 'rgba(255,255,255,0.08)',
                  borderColor: 'rgba(255,255,255,0.10)',
                }}
              >
                {f}
              </span>
            ))}
          </div>
        </div>
      </div>

      {/* ===== 右侧表单区 ===== */}
      <div className="flex-1 flex items-center justify-center p-6 sm:p-8 bg-bg-deep relative">
        {/* 主题切换按钮 */}
        <Button
          aria-label={theme === 'dark' ? t('common.toggle_theme_light') : t('common.toggle_theme_dark')}
          className="absolute top-4 right-4 z-10"
          isIconOnly
          size="sm"
          variant="ghost"
          onPress={toggleTheme}
        >
          {theme === 'dark' ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
        </Button>
        <div className="relative w-full max-w-[420px]">
          {/* 移动端 Logo */}
          <div className="text-center mb-8 lg:hidden">
            <img src={site.site_logo || defaultLogoUrl} alt="" className="w-11 h-11 rounded-sm mb-3 mx-auto object-cover" />
            <h1 className="text-lg font-bold text-text">
              {site.site_name || t('app_name')}
            </h1>
          </div>

          {/* Tab 切换 */}
          <Tabs
            className="mb-6 w-full"
            selectedKey={activeTab}
            onSelectionChange={(key) => {
              setActiveTab(key as TabKey);
              setRegisterSuccess(false);
            }}
            variant="secondary"
          >
            <Tabs.List className="w-full">
              <Tabs.Tab id="login">{t('common.login')}</Tabs.Tab>
              {site.registration_enabled ? (
                <Tabs.Tab id="register">{t('common.register')}</Tabs.Tab>
              ) : null}
              <Tabs.Tab id="apikey">API Key</Tabs.Tab>
            </Tabs.List>
          </Tabs>

          {/* 表单 */}
          <Card>
            <Card.Content className="p-6">
            {registerSuccess && activeTab === 'login' && (
              <Alert status="success" className="mb-5">
                <Alert.Content>
                  <Alert.Description>{t('auth.register_success')}</Alert.Description>
                </Alert.Content>
              </Alert>
            )}

            {activeTab === 'apikey' ? (
              <APIKeyLoginForm />
            ) : activeTab === 'register' && site.registration_enabled ? (
              <RegisterForm onSuccess={handleRegisterSuccess} />
            ) : (
              <LoginForm />
            )}
            </Card.Content>
          </Card>

          {/* 底部 */}
          <div className="mt-6 flex flex-col items-center gap-2">
            <p className="text-center text-[10px] text-text-tertiary font-mono uppercase">
              Powered by {site.site_name || 'AirGate'}
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
