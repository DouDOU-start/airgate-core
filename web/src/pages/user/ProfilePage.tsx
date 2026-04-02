import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation } from '@tanstack/react-query';
import { useAuth } from '../../app/providers/AuthProvider';
import { usersApi } from '../../shared/api/users';
import { authApi } from '../../shared/api/auth';
import { useToast } from '../../shared/components/Toast';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { Card } from '../../shared/components/Card';
import { Button } from '../../shared/components/Button';
import { Input } from '../../shared/components/Input';
import { Badge } from '../../shared/components/Badge';
import {
  User,
  Mail,
  Shield,
  Wallet,
  Layers,
  Save,
  Lock,
  KeyRound,
  ShieldCheck,
  ShieldOff,
  Smartphone,
  X,
} from 'lucide-react';

export default function ProfilePage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const { user } = useAuth();

  // 修改用户名
  const [username, setUsername] = useState(user?.username || '');
  const profileMutation = useCrudMutation<unknown, { username: string }>({
    mutationFn: (data) => usersApi.updateProfile(data),
    successMessage: t('profile.username_updated'),
    queryKey: queryKeys.userMe(),
  });

  // 修改密码
  const [passwords, setPasswords] = useState({
    old_password: '',
    new_password: '',
    confirm_password: '',
  });
  const passwordMutation = useCrudMutation<unknown, { old_password: string; new_password: string }>({
    mutationFn: (data) => usersApi.changePassword(data),
    successMessage: t('profile.password_changed'),
    queryKey: queryKeys.userMe(),
    onSuccess: () => {
      setPasswords({ old_password: '', new_password: '', confirm_password: '' });
    },
  });

  // TOTP 设置
  const [totpStep, setTotpStep] = useState<'idle' | 'setup' | 'verify'>('idle');
  const [totpUri, setTotpUri] = useState('');
  const [totpCode, setTotpCode] = useState('');
  const [disableCode, setDisableCode] = useState('');

  const totpSetupMutation = useMutation({
    mutationFn: () => authApi.totpSetup(),
    onSuccess: (data) => {
      setTotpUri(data.uri);
      setTotpStep('verify');
    },
    onError: (err: Error) => toast('error', err.message),
  });

  const totpVerifyMutation = useCrudMutation<unknown, string>({
    mutationFn: (code) => authApi.totpVerify({ code }),
    successMessage: t('profile.totp_enabled_success'),
    queryKey: queryKeys.userMe(),
    onSuccess: () => {
      setTotpStep('idle');
      setTotpCode('');
      setTotpUri('');
    },
  });

  const totpDisableMutation = useCrudMutation<unknown, string>({
    mutationFn: (code) => authApi.totpDisable({ code }),
    successMessage: t('profile.totp_disabled_success'),
    queryKey: queryKeys.userMe(),
    onSuccess: () => {
      setDisableCode('');
    },
  });

  function handleUpdateUsername() {
    if (!username.trim()) {
      toast('error', t('profile.username_empty'));
      return;
    }
    profileMutation.mutate({ username: username.trim() });
  }

  function handleChangePassword() {
    if (!passwords.old_password || !passwords.new_password) {
      toast('error', t('profile.password_incomplete'));
      return;
    }
    if (passwords.new_password !== passwords.confirm_password) {
      toast('error', t('profile.password_mismatch'));
      return;
    }
    if (passwords.new_password.length < 6) {
      toast('error', t('profile.password_too_short'));
      return;
    }
    passwordMutation.mutate({
      old_password: passwords.old_password,
      new_password: passwords.new_password,
    });
  }

  if (!user) return null;

  return (
    <div className="p-6 max-w-3xl">
      {/* 用户信息 */}
      <Card title={t('profile.basic_info')} className="mb-6">
        <div className="space-y-4">
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-2 w-28 shrink-0">
              <Mail className="w-4 h-4 text-text-tertiary" />
              <span className="text-xs font-medium text-text-secondary">{t('profile.email')}</span>
            </div>
            <span className="text-sm text-text">{user.email}</span>
          </div>
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-2 w-28 shrink-0">
              <Shield className="w-4 h-4 text-text-tertiary" />
              <span className="text-xs font-medium text-text-secondary">{t('profile.role')}</span>
            </div>
            <Badge variant={user.role === 'admin' ? 'info' : 'default'}>
              {user.role === 'admin' ? t('nav.admin') : t('nav.user')}
            </Badge>
          </div>
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-2 w-28 shrink-0">
              <Wallet className="w-4 h-4 text-text-tertiary" />
              <span className="text-xs font-medium text-text-secondary">{t('profile.balance')}</span>
            </div>
            <span className="text-sm text-text font-mono">
              ${user.balance.toFixed(4)}
            </span>
          </div>
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-2 w-28 shrink-0">
              <Layers className="w-4 h-4 text-text-tertiary" />
              <span className="text-xs font-medium text-text-secondary">{t('profile.concurrency')}</span>
            </div>
            <span className="text-sm text-text font-mono">
              {user.max_concurrency}
            </span>
          </div>
        </div>
      </Card>

      {/* 修改用户名 */}
      <Card title={t('profile.change_username')} className="mb-6">
        <div className="flex items-end gap-4">
          <div className="flex-1">
            <Input
              label={t('profile.change_username')}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder={t('profile.username_placeholder')}
              icon={<User className="w-4 h-4" />}
            />
          </div>
          <Button
            onClick={handleUpdateUsername}
            loading={profileMutation.isPending}
            icon={<Save className="w-4 h-4" />}
          >
            {t('common.save')}
          </Button>
        </div>
      </Card>

      {/* 修改密码 */}
      <Card title={t('profile.change_password')} className="mb-6">
        <div className="space-y-4">
          <Input
            label={t('profile.old_password')}
            type="password"
            required
            value={passwords.old_password}
            onChange={(e) =>
              setPasswords({ ...passwords, old_password: e.target.value })
            }
            placeholder={t('profile.old_password_placeholder')}
            icon={<Lock className="w-4 h-4" />}
          />
          <Input
            label={t('profile.new_password')}
            type="password"
            required
            value={passwords.new_password}
            onChange={(e) =>
              setPasswords({ ...passwords, new_password: e.target.value })
            }
            placeholder={t('profile.new_password_placeholder')}
            icon={<KeyRound className="w-4 h-4" />}
          />
          <Input
            label={t('profile.confirm_new_password')}
            type="password"
            required
            value={passwords.confirm_password}
            onChange={(e) =>
              setPasswords({ ...passwords, confirm_password: e.target.value })
            }
            placeholder={t('profile.confirm_placeholder')}
            icon={<KeyRound className="w-4 h-4" />}
          />
          <Button
            onClick={handleChangePassword}
            loading={passwordMutation.isPending}
            icon={<Lock className="w-4 h-4" />}
          >
            {t('profile.change_password')}
          </Button>
        </div>
      </Card>

      {/* TOTP 双因素认证 */}
      <Card title={t('profile.totp_title')}>
        {user.totp_enabled ? (
          /* 已启用 —— 显示禁用入口 */
          <div className="space-y-4">
            <div className="flex items-center gap-3">
              <Badge variant="success">{t('profile.totp_enabled')}</Badge>
              <span className="text-sm text-text-secondary">
                {t('profile.totp_enabled_desc')}
              </span>
            </div>
            <div className="flex items-end gap-4">
              <div className="flex-1">
                <Input
                  label={t('profile.totp_code')}
                  value={disableCode}
                  onChange={(e) => setDisableCode(e.target.value)}
                  placeholder={t('profile.totp_code_placeholder')}
                  maxLength={6}
                  icon={<ShieldOff className="w-4 h-4" />}
                />
              </div>
              <Button
                variant="danger"
                onClick={() => disableCode && totpDisableMutation.mutate(disableCode)}
                loading={totpDisableMutation.isPending}
                icon={<X className="w-4 h-4" />}
              >
                {t('profile.totp_disable')}
              </Button>
            </div>
          </div>
        ) : (
          /* 未启用 */
          <div className="space-y-4">
            {totpStep === 'idle' && (
              <div>
                <p className="text-sm text-text-secondary mb-4">
                  {t('profile.totp_enable_desc')}
                </p>
                <Button
                  onClick={() => totpSetupMutation.mutate()}
                  loading={totpSetupMutation.isPending}
                  icon={<ShieldCheck className="w-4 h-4" />}
                >
                  {t('profile.totp_enable')}
                </Button>
              </div>
            )}

            {totpStep === 'verify' && (
              <div className="space-y-4">
                <p className="text-sm text-text-secondary">
                  {t('profile.totp_scan_uri')}
                </p>
                <div
                  className="border border-glass-border bg-bg-elevated shadow-sm rounded-lg p-3 break-all text-sm text-text-secondary font-mono"
                >
                  <div className="flex items-start gap-2">
                    <Smartphone className="w-4 h-4 text-primary mt-0.5 shrink-0" />
                    <span>{totpUri}</span>
                  </div>
                </div>
                <div className="flex items-end gap-4">
                  <div className="flex-1">
                    <Input
                      label={t('profile.totp_verify_code')}
                      value={totpCode}
                      onChange={(e) => setTotpCode(e.target.value)}
                      placeholder={t('profile.totp_verify_placeholder')}
                      maxLength={6}
                      icon={<ShieldCheck className="w-4 h-4" />}
                    />
                  </div>
                  <Button
                    onClick={() => totpCode && totpVerifyMutation.mutate(totpCode)}
                    loading={totpVerifyMutation.isPending}
                    icon={<ShieldCheck className="w-4 h-4" />}
                  >
                    {t('profile.totp_verify_enable')}
                  </Button>
                  <Button
                    variant="secondary"
                    onClick={() => {
                      setTotpStep('idle');
                      setTotpUri('');
                      setTotpCode('');
                    }}
                  >
                    {t('common.cancel')}
                  </Button>
                </div>
              </div>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}
