import { useEffect, useState, type CSSProperties } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, FieldError, Form, Input, Label, Tabs, TextField as HeroTextField } from '@heroui/react';
import { useAuth } from '../app/providers/AuthProvider';
import { useSiteSettings, defaultLogoUrl } from '../app/providers/SiteSettingsProvider';
import { authApi } from '../shared/api/auth';
import { useTheme } from '../app/providers/ThemeProvider';
import { ApiError, setSessionAPIKey } from '../shared/api/client';
import { Mail, Lock, User, ArrowRight, Sun, Moon, ShieldCheck, Key, Sprout, Layers, Zap, BarChart3 } from 'lucide-react';

/** 浮尘光点的固定布局（避免 Math.random 每次渲染重排） */
const MOTES = [
  { left: '8%', size: 5, duration: '10s', delay: '0s', drift: '10px' },
  { left: '18%', size: 3, duration: '13s', delay: '2.4s', drift: '-14px' },
  { left: '30%', size: 4, duration: '9s', delay: '4.8s', drift: '6px' },
  { left: '46%', size: 6, duration: '14s', delay: '1.2s', drift: '-8px' },
  { left: '62%', size: 3, duration: '11s', delay: '5.6s', drift: '12px' },
  { left: '75%', size: 5, duration: '12s', delay: '3.1s', drift: '-10px' },
  { left: '88%', size: 4, duration: '10.5s', delay: '6.4s', drift: '8px' },
] as const;

function FloatingMotes({ tint }: { tint: string }) {
  return (
    <div aria-hidden className="pointer-events-none absolute inset-0 overflow-hidden">
      {MOTES.map((m, i) => (
        <span
          key={i}
          className="ag-float-mote absolute bottom-0 rounded-full"
          style={{
            left: m.left,
            width: m.size,
            height: m.size,
            background: tint,
            boxShadow: `0 0 ${m.size * 2.5}px ${tint}`,
            '--mote-duration': m.duration,
            '--mote-delay': m.delay,
            '--mote-drift-x': m.drift,
          } as CSSProperties}
        />
      ))}
    </div>
  );
}

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

  const features = [
    { icon: <Layers className="w-4 h-4" />, title: t('auth.feature_1'), desc: t('auth.feature_1_desc') },
    { icon: <Zap className="w-4 h-4" />, title: t('auth.feature_2'), desc: t('auth.feature_2_desc') },
    { icon: <BarChart3 className="w-4 h-4" />, title: t('auth.feature_3'), desc: t('auth.feature_3_desc') },
  ];

  // 左面板恒为深林墨（不随主题翻转）——品牌时刻的固定基调，与右侧随主题翻转的纸面表单区形成对比
  const inkFaint = 'rgba(247,243,234,0.62)';
  const inkDim = 'rgba(247,243,234,0.4)';
  const inkLine = 'rgba(247,243,234,0.16)';
  const inkFull = '#f7f3ea';

  return (
    <div className="flex min-h-screen relative overflow-hidden bg-bg text-text lg:flex-row flex-col">
      {/* ===== 左侧：深林墨海报（桌面端，恒定深绿黑） ===== */}
      <div
        className="ag-organic-canvas relative hidden flex-col justify-between overflow-hidden p-10 lg:flex lg:w-[44%] xl:w-[46%] xl:p-14"
        style={{ background: 'radial-gradient(120% 120% at 18% -8%, #1c3a25 0%, #0e1c14 52%, #070c09 100%)', color: inkFull }}
      >
        {/* 中央柔光 */}
        <div
          aria-hidden
          className="ag-blob-drift pointer-events-none absolute left-1/2 top-[36%] h-[28rem] w-[28rem] -translate-x-1/2 -translate-y-1/2 rounded-full"
          style={{ background: 'radial-gradient(closest-side, rgba(154,214,166,0.22), transparent 72%)' }}
        />
        <FloatingMotes tint="rgba(198,224,178,0.85)" />

        {/* 品牌 */}
        <div className="relative z-10 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <img src={site.site_logo || defaultLogoUrl} alt="" className="h-8 w-8 rounded-[var(--radius-md)] object-cover" />
            <span className="font-display text-base font-semibold tracking-tight">{site.site_name || 'AirGate'}</span>
          </div>
          <span className="inline-flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.28em]" style={{ color: inkDim }}>
            <Sprout className="h-3 w-3" strokeWidth={2.25} />
            AI Gateway
          </span>
        </div>

        {/* 发光几何 + 主题句 */}
        <div className="relative z-10 flex flex-col items-center text-center">
          <span
            className="ag-breathe mb-10 flex h-20 w-20 items-center justify-center rounded-[var(--radius-lg)]"
            style={{ background: inkFull }}
          >
            <img src={site.site_logo || defaultLogoUrl} alt="" className="h-14 w-14 rounded-[var(--radius-md)] object-cover" />
          </span>
          <h2 className="font-display mb-5 text-[2.5rem] font-medium leading-[1.12] tracking-[-0.02em] xl:text-[3rem]" style={{ color: inkFull }}>
            {t('auth.welcome_title_1')}
            <br />
            {t('auth.welcome_title_2')}
          </h2>
          <p className="max-w-md text-sm leading-relaxed xl:text-[15px]" style={{ color: inkFaint }}>
            {t('auth.welcome_desc')}
          </p>
        </div>

        {/* 有机分隔线 + 特性（等宽大写索引） */}
        <div className="relative z-10">
          <div className="ag-scanline mb-6" style={{ background: inkLine }} />
          <div className="flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between xl:gap-6">
            {features.map((f) => (
              <div key={f.title} className="flex items-start gap-3 xl:max-w-[30%]">
                <span className="mt-0.5 shrink-0" style={{ color: inkFull }}>{f.icon}</span>
                <span className="min-w-0">
                  <span className="block font-mono text-[11px] font-medium uppercase tracking-[0.14em]" style={{ color: inkFull }}>
                    {f.title}
                  </span>
                  <span className="mt-1 block text-xs leading-relaxed" style={{ color: inkDim }}>{f.desc}</span>
                </span>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* ===== 右侧表单区（暖纸面，随主题翻转）——与首页共用同一套有机画布纹理，视觉语言统一 ===== */}
      <div className="ag-organic-canvas relative flex flex-1 items-center justify-center overflow-hidden p-6 sm:p-8">
        {/* 接缝柔光：贴左侧品牌墨绿向右侧渗透一层极淡色带，弥合两块面板的色相断层 */}
        <div
          aria-hidden
          className="pointer-events-none absolute inset-y-0 left-0 w-24"
          style={{ background: 'linear-gradient(90deg, color-mix(in oklab, var(--ag-primary) 10%, transparent), transparent)' }}
        />

        {/* 主题切换按钮 */}
        <Button
          aria-label={theme === 'dark' ? t('common.toggle_theme_light') : t('common.toggle_theme_dark')}
          className="absolute right-4 top-4 z-10"
          isIconOnly
          size="sm"
          variant="ghost"
          onPress={toggleTheme}
        >
          {theme === 'dark' ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
        </Button>

        <div className="ag-page-body relative w-full max-w-[420px]">
          {/* 移动端品牌区：圆润徽标 + 站名 + 标语，呼应桌面端左面板的品牌时刻 */}
          <div className="mb-8 flex flex-col items-center text-center lg:hidden">
            <span className="ag-breathe mb-4 flex h-14 w-14 items-center justify-center rounded-[var(--radius-lg)] bg-primary shadow-[var(--ag-shadow-md)]">
              <img src={site.site_logo || defaultLogoUrl} alt="" className="h-10 w-10 rounded-[var(--radius-md)] object-cover" />
            </span>
            <h1 className="font-display text-xl font-medium tracking-tight text-text">
              {site.site_name || t('app_name')}
            </h1>
            <span className="mt-1.5 inline-flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.24em] text-text-tertiary">
              <Sprout className="h-3 w-3" strokeWidth={2.25} />
              AI Gateway
            </span>
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
          <Card className="shadow-[var(--ag-shadow-lg)]">
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
            <p className="text-center text-[10px] text-text-tertiary font-mono uppercase tracking-[0.14em]">
              Powered by {site.site_name || 'AirGate'}
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
