import { createContext, useContext, useEffect, useMemo, useState } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, FieldError, Form, Input, Label, Tabs, TextField as HeroTextField } from '@heroui/react';
import { useAuth } from '../app/providers/AuthProvider';
import { useSiteSettings, defaultLogoUrl } from '../app/providers/SiteSettingsProvider';
import { authApi } from '../shared/api/auth';
import { useTheme } from '../app/providers/ThemeProvider';
import { ApiError, setSessionAPIKey } from '../shared/api/client';
import { Mail, Lock, User, ArrowRight, Sun, Moon, ShieldCheck, Key, Sprout, Eye, EyeOff } from 'lucide-react';
import { AmbientAurora } from './login/AmbientAurora';

/**
 * 左侧小人群与右侧表单跨组件通信：表单聚焦哪个字段、密码是否显示，
 * 通过 context 上报给 LoginPage，驱动 PeekingCrowd 的行为。
 */
type VibeApi = {
  setField: (f: 'email' | 'password' | null) => void;
  setReveal: (b: boolean) => void;
};
const VibeContext = createContext<VibeApi>({ setField: () => {}, setReveal: () => {} });

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
  const vibe = useContext(VibeContext);

  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  // 切换密码可见性：同步告知左侧小人（reveal → 全体惊呼偷看）
  const toggleReveal = () => {
    const next = !showPassword;
    setShowPassword(next);
    vibe.setReveal(next);
  };

  // 表单卸载（切 Tab）时复位小人状态，避免停留在偷看/惊呼姿态
  useEffect(() => () => {
    vibe.setField(null);
    vibe.setReveal(false);
  }, [vibe]);

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
            onFocus={() => vibe.setField('email')}
            onBlur={() => vibe.setField(null)}
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
            className="pl-9 pr-10"
            name="password"
            type={showPassword ? 'text' : 'password'}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            onFocus={() => vibe.setField('password')}
            onBlur={() => vibe.setField(null)}
            placeholder={t('auth.password_placeholder')}
            autoComplete="current-password"
            required
          />
          <button
            type="button"
            onClick={toggleReveal}
            aria-label={showPassword ? t('auth.hide_password') : t('auth.show_password')}
            aria-pressed={showPassword}
            className="absolute right-2 top-1/2 z-10 flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-[var(--radius-sm)] text-text-tertiary transition-colors hover:bg-bg-hover hover:text-text"
          >
            {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
          </button>
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

  // 左侧动效交互状态：聚焦字段 + 密码是否显示，驱动 GatewayFlux 的流束表现
  const [field, setField] = useState<'email' | 'password' | null>(null);
  const [reveal, setReveal] = useState(false);
  const vibeApi = useMemo<VibeApi>(() => ({ setField, setReveal }), []);

  // 表单聚焦（或显示密码）时极光极轻微提亮
  const auroraActive = field !== null || reveal;

  const handleRegisterSuccess = () => {
    setRegisterSuccess(true);
    setActiveTab('login');
  };

  // 品牌文字随主题翻转：暗色象牙白，亮色深蓝墨（底色/极光由 AmbientAurora 按主题切换）
  const isDark = theme === 'dark';
  const inkFull = isDark ? '#f7f3ea' : '#1b2749';
  const inkDim = isDark ? 'rgba(247,243,234,0.4)' : 'rgba(27,39,73,0.55)';

  return (
    <VibeContext.Provider value={vibeApi}>
    <div className="relative flex min-h-screen flex-col overflow-hidden bg-bg text-text">
      {/* 全页统一极光背景：整页共享同一张画布，一个页面而非左右两块 */}
      <AmbientAurora active={auroraActive} className="pointer-events-none absolute inset-0" />

      {/* 主题切换按钮（右上角） */}
      <Button
        aria-label={theme === 'dark' ? t('common.toggle_theme_light') : t('common.toggle_theme_dark')}
        className="absolute right-4 top-4 z-20"
        isIconOnly
        size="sm"
        variant="ghost"
        onPress={toggleTheme}
      >
        {theme === 'dark' ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
      </Button>

      {/* 居中主区：品牌锁定 + 表单卡（表单视觉居中） */}
      <main className="relative z-10 flex flex-1 flex-col items-center justify-center px-6 py-12">
        <div className="ag-page-body w-full max-w-[420px]">
          {/* 品牌锁定：磨砂徽标 + 站名 + 副标题 */}
          <div className="mb-8 flex flex-col items-center text-center" style={{ color: inkFull }}>
            <span
              className={`ag-breathe mb-5 flex h-20 w-20 items-center justify-center rounded-[var(--radius-xl)] shadow-[var(--ag-shadow-md)] backdrop-blur-sm ring-1 ${
                isDark ? 'bg-white/[0.06] ring-white/10' : 'bg-white/60 ring-black/5'
              }`}
            >
              <img src={site.site_logo || defaultLogoUrl} alt="" className="h-14 w-14 rounded-[var(--radius-lg)] object-cover" />
            </span>
            <h1 className="font-display text-[2rem] font-medium leading-none tracking-tight">
              {site.site_name || t('app_name')}
            </h1>
            <span className="mt-3 inline-flex items-center gap-2 font-mono text-[11px] uppercase tracking-[0.26em]" style={{ color: inkDim }}>
              <Sprout className="h-3.5 w-3.5" strokeWidth={2.25} />
              {site.site_subtitle || 'AI Gateway'}
            </span>
          </div>

          {/* 表单卡：Tab 切换与对应表单同处一卡 */}
          <Card className="shadow-[var(--ag-shadow-lg)]">
            <Card.Content className="p-6">
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

          {/* Powered by */}
          <p className="mt-6 text-center font-mono text-[10px] uppercase tracking-[0.14em] text-text-tertiary">
            Powered by {site.site_name || 'AirGate'}
          </p>
        </div>
      </main>
    </div>
    </VibeContext.Provider>
  );
}
