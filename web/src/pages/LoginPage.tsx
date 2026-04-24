import { useEffect, useState } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { Button } from '../shared/components/Button';
import { Input } from '../shared/components/Input';
import { Alert } from '../shared/components/Alert';
import { useAuth } from '../app/providers/AuthProvider';
import { useSiteSettings, defaultLogoUrl } from '../app/providers/SiteSettingsProvider';
import { authApi } from '../shared/api/auth';
import { useTheme } from '../app/providers/ThemeProvider';
import { ApiError, setSessionAPIKey } from '../shared/api/client';
import { Mail, Lock, User, ArrowRight, Sun, Moon, ShieldCheck, Key, Activity } from 'lucide-react';

type TabKey = 'login' | 'register' | 'apikey';

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
      navigate({ to: '/' });
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
    <form onSubmit={handleSubmit} className="space-y-4">
      <Input
        label={t('auth.email')}
        type="email"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        placeholder={t('auth.email_placeholder')}
        autoComplete="username"
        icon={<Mail className="w-4 h-4" />}
        required
        autoFocus
      />
      <Input
        label={t('auth.password')}
        type="password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        placeholder={t('auth.password_placeholder')}
        autoComplete="current-password"
        icon={<Lock className="w-4 h-4" />}
        required
      />
      {error && <Alert variant="error">{error}</Alert>}
      <Button type="submit" loading={loading} className="w-full h-11" icon={<ArrowRight className="w-4 h-4" />}>
        {t('common.login')}
      </Button>
    </form>
  );
}

/* ==================== 注册表单 ==================== */

function RegisterForm({ onSuccess }: { onSuccess: () => void }) {
  const { t } = useTranslation();
  const site = useSiteSettings();
  const needVerify = site.email_verify_enabled;

  const [step, setStep] = useState<1 | 2>(1);
  const [email, setEmail] = useState('');
  const [verifyCode, setVerifyCode] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [sendingCode, setSendingCode] = useState(false);
  const [codeSent, setCodeSent] = useState(false);
  const [countdown, setCountdown] = useState(0);
  const [error, setError] = useState('');

  const passwordMismatch = confirmPassword !== '' && password !== confirmPassword;

  // 倒计时
  useEffect(() => {
    if (countdown <= 0) return;
    const timer = window.setInterval(() => {
      setCountdown((c) => (c <= 1 ? 0 : c - 1));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [countdown]);

  // 发送验证码
  const handleSendCode = async () => {
    if (!email) { setError(t('auth.email_required')); return; }
    setSendingCode(true);
    setError('');
    try {
      await authApi.sendVerifyCode(email);
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
    if (!needVerify) {
      // 不需要验证码，直接进入第二步
      setStep(2);
      return;
    }
    if (!verifyCode) { setError(t('auth.code_required')); return; }
    setStep(2);
  };

  // 第二步：提交注册
  const handleStep2 = async (e: React.FormEvent) => {
    e.preventDefault();
    if (password !== confirmPassword) { setError(t('auth.password_mismatch')); return; }
    if (password.length < 8) { setError(t('auth.password_too_short')); return; }

    setLoading(true);
    setError('');
    try {
      await authApi.register({
        email,
        password,
        username: username || undefined,
        verify_code: needVerify ? verifyCode : undefined,
      });
      onSuccess();
    } catch (err) {
      if (err instanceof ApiError) {
        // 验证码错误则回到第一步
        if (err.message.includes('验证码')) {
          setStep(1);
          setVerifyCode('');
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
      <form onSubmit={handleStep1} className="space-y-4">
        <Input
          label={t('auth.email')}
          type="email"
          value={email}
          onChange={(e) => { setEmail(e.target.value); setError(''); }}
          placeholder={t('auth.email_placeholder')}
          autoComplete="email"
          icon={<Mail className="w-4 h-4" />}
          required
          autoFocus
        />
        {needVerify && (
          <div>
            <label className="block text-[13px] font-medium text-text-secondary mb-1.5">
              {t('auth.verify_code')}
            </label>
            <div className="flex gap-2">
              <div className="flex-1">
                <Input
                  value={verifyCode}
                  onChange={(e) => { setVerifyCode(e.target.value); setError(''); }}
                  placeholder={t('auth.verify_code_placeholder')}
                  icon={<ShieldCheck className="w-4 h-4" />}
                  maxLength={6}
                  required
                />
              </div>
              <Button
                type="button"
                variant="secondary"
                onClick={handleSendCode}
                loading={sendingCode}
                disabled={countdown > 0 || !email}
                className="shrink-0 h-[42px]"
              >
                {countdown > 0 ? `${countdown}s` : codeSent ? t('auth.resend_code') : t('auth.send_code')}
              </Button>
            </div>
          </div>
        )}
        {error && <Alert variant="error">{error}</Alert>}
        <Button type="submit" className="w-full h-11" icon={<ArrowRight className="w-4 h-4" />}>
          {t('auth.next_step')}
        </Button>
      </form>
    );
  }

  // 第二步：填写密码等信息
  return (
    <form onSubmit={handleStep2} className="space-y-4">
      {/* 已验证的邮箱（只读展示） */}
      <div className="flex items-center gap-2 px-3.5 py-2.5 rounded-[10px] border border-glass-border bg-surface text-sm text-text-secondary">
        <Mail className="w-4 h-4 text-text-tertiary shrink-0" />
        <span className="truncate">{email}</span>
        <button type="button" onClick={() => setStep(1)} className="ml-auto text-xs text-primary hover:underline shrink-0">
          {t('auth.change_email')}
        </button>
      </div>
      <Input
        label={t('auth.username')}
        value={username}
        onChange={(e) => setUsername(e.target.value)}
        placeholder={t('auth.username_placeholder')}
        icon={<User className="w-4 h-4" />}
        autoFocus
      />
      <Input
        label={t('auth.password')}
        type="password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        placeholder={t('auth.password_hint')}
        autoComplete="new-password"
        icon={<Lock className="w-4 h-4" />}
        required
      />
      <Input
        label={t('auth.confirm_password')}
        type="password"
        value={confirmPassword}
        onChange={(e) => setConfirmPassword(e.target.value)}
        placeholder={t('auth.confirm_placeholder')}
        autoComplete="new-password"
        icon={<Lock className="w-4 h-4" />}
        required
        error={passwordMismatch ? t('auth.password_mismatch') : undefined}
      />
      {error && <Alert variant="error">{error}</Alert>}
      <Button type="submit" loading={loading} className="w-full h-11">
        {t('common.register')}
      </Button>
    </form>
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
      // 把用户输入的原文 Key 暂存到 sessionStorage，供 CCS 导入等需要原文的功能使用。
      setSessionAPIKey(apiKey);
      login(resp.token, { ...resp.user, api_key_id: resp.api_key_id, api_key_name: resp.api_key_name });
      navigate({ to: '/' });
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
    <form onSubmit={handleSubmit} className="space-y-4">
      <Input
        label="API Key"
        type="password"
        value={apiKey}
        onChange={(e) => setApiKey(e.target.value)}
        placeholder="sk-..."
        autoComplete="off"
        icon={<Key className="w-4 h-4" />}
        required
        autoFocus
      />
      <p className="text-[11px] text-text-tertiary">{t('auth.apikey_login_hint')}</p>
      {error && <Alert variant="error">{error}</Alert>}
      <Button type="submit" loading={loading} className="w-full h-11" icon={<ArrowRight className="w-4 h-4" />}>
        {t('common.login')}
      </Button>
    </form>
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
    <div className="min-h-screen flex relative overflow-hidden">
      {/* ===== 左侧装饰面板（桌面端） ===== */}
      <div
        className="hidden lg:flex lg:w-[45%] xl:w-[50%] relative items-center justify-center"
        style={{
          background: theme === 'dark'
            ? 'linear-gradient(135deg, var(--ag-bg-elevated), var(--ag-bg))'
            : 'linear-gradient(135deg, var(--ag-primary), color-mix(in srgb, var(--ag-primary) 60%, var(--ag-info)))',
        }}
      >
        {/* 装饰纹理 */}
        <div
          className="absolute inset-0"
          style={{
            opacity: theme === 'dark' ? 0.04 : 0.1,
            backgroundImage: `radial-gradient(circle at 25% 25%, ${theme === 'dark' ? 'var(--ag-primary)' : 'white'} 1px, transparent 1px), radial-gradient(circle at 75% 75%, ${theme === 'dark' ? 'var(--ag-primary)' : 'white'} 1px, transparent 1px)`,
            backgroundSize: '48px 48px',
          }}
        />
        {/* 暗色模式主题色光晕 */}
        {theme === 'dark' && (
          <>
            <div className="absolute top-[5%] left-[10%] w-[400px] h-[400px] rounded-full opacity-[0.08]"
              style={{ background: 'radial-gradient(circle, var(--ag-primary), transparent 65%)' }}
            />
            <div className="absolute bottom-[10%] right-[5%] w-[300px] h-[300px] rounded-full opacity-[0.05]"
              style={{ background: 'radial-gradient(circle, var(--ag-info), transparent 65%)' }}
            />
          </>
        )}
        {/* 装饰大圆 */}
        <div className="absolute top-[10%] right-[-5%] w-[300px] h-[300px] rounded-full"
          style={{ border: `1px solid color-mix(in srgb, ${theme === 'dark' ? 'var(--ag-primary)' : 'white'} 15%, transparent)` }}
        />
        <div className="absolute bottom-[15%] left-[-8%] w-[200px] h-[200px] rounded-full"
          style={{ border: `1px solid color-mix(in srgb, ${theme === 'dark' ? 'var(--ag-primary)' : 'white'} 10%, transparent)` }}
        />

        {/* 内容 */}
        <div className={`relative z-10 px-12 max-w-md ${theme === 'dark' ? 'text-text' : 'text-white'}`}>
          <div className="flex items-center gap-3 mb-8">
            <img src={site.site_logo || defaultLogoUrl} alt="" className={`w-10 h-10 rounded-sm object-cover ${theme === 'dark' ? '' : (!site.site_logo ? '' : 'brightness-0 invert')}`} />
            <span className="text-xl font-bold tracking-tight">{site.site_name || 'AirGate'}</span>
          </div>
          <h2 className="text-3xl font-bold leading-snug mb-4">
            {t('auth.welcome_title')}
          </h2>
          <p className={`text-sm leading-relaxed ${theme === 'dark' ? 'text-text-tertiary' : 'text-white/70'}`}>
            {t('auth.welcome_desc')}
          </p>
          <div className="flex gap-3 mt-10">
            {[t('auth.feature_1'), t('auth.feature_2'), t('auth.feature_3')].map((f) => (
              <span
                key={f}
                className="text-[11px] px-3 py-1.5 rounded-full font-medium"
                style={{
                  background: theme === 'dark' ? 'var(--ag-primary-subtle)' : 'rgba(255,255,255,0.1)',
                  border: `1px solid ${theme === 'dark' ? 'var(--ag-glass-border)' : 'rgba(255,255,255,0.1)'}`,
                  color: theme === 'dark' ? 'var(--ag-primary)' : 'white',
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
        <button
          onClick={toggleTheme}
          className="absolute top-4 right-4 z-10 flex items-center justify-center w-9 h-9 rounded-xl text-text-tertiary hover:text-text-secondary hover:bg-bg-hover transition-colors"
          title={theme === 'dark' ? 'Light mode' : 'Dark mode'}
        >
          {theme === 'dark' ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
        </button>
        {/* 背景装饰（移动端 + 桌面右侧） */}
        <div className="absolute inset-0 pointer-events-none overflow-hidden">
          <div className="absolute -top-[40%] -right-[20%] w-[600px] h-[600px] rounded-full opacity-[0.05]"
            style={{ background: 'radial-gradient(circle, var(--ag-primary), transparent 65%)' }}
          />
          <div className="absolute -bottom-[30%] -left-[15%] w-[400px] h-[400px] rounded-full opacity-[0.03]"
            style={{ background: 'radial-gradient(circle, var(--ag-info), transparent 65%)' }}
          />
        </div>

        <div className="relative w-full max-w-[420px]" style={{ animation: 'ag-slide-up 0.45s cubic-bezier(0.16, 1, 0.3, 1)' }}>
          {/* 移动端 Logo */}
          <div className="text-center mb-8 lg:hidden">
            <img src={site.site_logo || defaultLogoUrl} alt="" className="w-11 h-11 rounded-sm mb-3 mx-auto object-cover" />
            <h1 className="text-lg font-bold text-text tracking-tight">
              {site.site_name || t('app_name')}
            </h1>
          </div>

          {/* Tab 切换 */}
          <div className="flex gap-1 mb-6 p-1 rounded-xl bg-bg-hover/60">
            {([
              { key: 'login' as const, label: t('common.login') },
              ...(site.registration_enabled ? [{ key: 'register' as const, label: t('common.register') }] : []),
              { key: 'apikey' as const, label: 'API Key' },
            ]).map((tab) => (
              <button
                key={tab.key}
                onClick={() => { setActiveTab(tab.key); setRegisterSuccess(false); }}
                className={`flex-1 py-2 text-sm font-medium rounded-lg transition-all ${
                  activeTab === tab.key
                    ? 'bg-bg-elevated text-text shadow-sm'
                    : 'text-text-tertiary hover:text-text-secondary'
                }`}
              >
                {tab.label}
              </button>
            ))}
          </div>

          {/* 表单 */}
          <div className="border border-glass-border bg-bg-elevated shadow-sm rounded-xl p-6">
            {registerSuccess && activeTab === 'login' && (
              <Alert variant="success" className="mb-5">{t('auth.register_success')}</Alert>
            )}

            {activeTab === 'apikey' ? (
              <APIKeyLoginForm />
            ) : activeTab === 'register' && site.registration_enabled ? (
              <RegisterForm onSuccess={handleRegisterSuccess} />
            ) : (
              <LoginForm />
            )}
          </div>

          {/* 底部 */}
          <div className="mt-6 flex flex-col items-center gap-2">
            <a
              href="/status"
              className="inline-flex items-center gap-1.5 text-[11px] text-text-tertiary hover:text-primary transition-colors"
            >
              <Activity className="w-3 h-3" />
              {t('nav.status')}
            </a>
            <p className="text-center text-[10px] text-text-tertiary font-mono uppercase tracking-[0.15em]">
              Powered by {site.site_name || 'AirGate'}
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
