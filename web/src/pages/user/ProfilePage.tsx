import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Card, Chip, Description, Form, Input, Label, Switch, TextField as HeroTextField } from '@heroui/react';
import { useAuth } from '../../app/providers/AuthProvider';
import { usersApi } from '../../shared/api/users';
import { useToast } from '../../shared/ui';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { useMutation } from '@tanstack/react-query';
import {
  User,
  Mail,
  Shield,
  Wallet,
  Layers,
  Save,
  Lock,
  KeyRound,
  Bell,
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

  function handleUpdateUsername() {
    if (!username.trim()) {
      toast('error', t('profile.username_empty'));
      return;
    }
    profileMutation.mutate({ username: username.trim() });
  }

  function handleChangePassword(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault();
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
    <div className="p-6 max-w-3xl mx-auto">
      {/* 用户信息 */}
      <Card className="mb-6">
        <Card.Header>
          <Card.Title>{t('profile.basic_info')}</Card.Title>
        </Card.Header>
        <Card.Content>
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
              <Chip color={user.role === 'admin' ? 'accent' : 'default'} size="sm" variant="soft">
                {user.role === 'admin' ? t('nav.admin') : t('nav.user')}
              </Chip>
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
        </Card.Content>
      </Card>

      {/* 余额预警 */}
      <BalanceAlertCard
        threshold={user.balance_alert_threshold}
        balance={user.balance}
      />

      {/* 修改用户名 */}
      <Card className="mb-6">
        <Card.Header>
          <Card.Title>{t('profile.change_username')}</Card.Title>
        </Card.Header>
        <Card.Content>
          <div className="flex flex-col sm:flex-row sm:items-end gap-3 sm:gap-4">
            <div className="flex-1">
              <HeroTextField fullWidth>
                <Label>{t('profile.change_username')}</Label>
                <div className="relative">
                  <User className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
                  <Input
                    className="pl-9"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    placeholder={t('profile.username_placeholder')}
                  />
                </div>
              </HeroTextField>
            </div>
            <Button
              onPress={handleUpdateUsername}
              isDisabled={profileMutation.isPending}
              variant="primary"
              aria-busy={profileMutation.isPending}
            >
              <Save className="w-4 h-4" />
              {t('common.save')}
            </Button>
          </div>
        </Card.Content>
      </Card>

      {/* 修改密码 */}
      <Card className="mb-6">
        <Card.Header>
          <Card.Title>{t('profile.change_password')}</Card.Title>
        </Card.Header>
        <Card.Content>
          <Form className="space-y-4" onSubmit={handleChangePassword} noValidate>
            <HeroTextField fullWidth isRequired>
              <Label>{t('profile.old_password')}</Label>
              <div className="relative">
                <Lock className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
                <Input
                  className="pl-9"
                  name="current-password"
                  type="password"
                  value={passwords.old_password}
                  onChange={(e) =>
                    setPasswords({ ...passwords, old_password: e.target.value })
                  }
                  placeholder={t('profile.old_password_placeholder')}
                  autoComplete="current-password"
                  required
                />
              </div>
            </HeroTextField>
            <HeroTextField fullWidth isRequired>
              <Label>{t('profile.new_password')}</Label>
              <div className="relative">
                <KeyRound className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
                <Input
                  className="pl-9"
                  name="new-password"
                  type="password"
                  value={passwords.new_password}
                  onChange={(e) =>
                    setPasswords({ ...passwords, new_password: e.target.value })
                  }
                  placeholder={t('profile.new_password_placeholder')}
                  autoComplete="new-password"
                  required
                />
              </div>
            </HeroTextField>
            <HeroTextField fullWidth isRequired>
              <Label>{t('profile.confirm_new_password')}</Label>
              <div className="relative">
                <KeyRound className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
                <Input
                  className="pl-9"
                  name="confirm-new-password"
                  type="password"
                  value={passwords.confirm_password}
                  onChange={(e) =>
                    setPasswords({ ...passwords, confirm_password: e.target.value })
                  }
                  placeholder={t('profile.confirm_placeholder')}
                  autoComplete="new-password"
                  required
                />
              </div>
            </HeroTextField>
            <Button
              type="submit"
              isDisabled={passwordMutation.isPending}
              variant="primary"
              aria-busy={passwordMutation.isPending}
            >
              <Lock className="w-4 h-4" />
              {t('profile.change_password')}
            </Button>
          </Form>
        </Card.Content>
      </Card>
    </div>
  );
}

/* ==================== 余额预警卡片 ==================== */

function BalanceAlertCard({ threshold, balance }: { threshold: number; balance: number }) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const [enabled, setEnabled] = useState(threshold > 0);
  const [value, setValue] = useState(threshold > 0 ? String(threshold) : '');

  const mutation = useMutation({
    mutationFn: (newThreshold: number) => usersApi.updateBalanceAlert(newThreshold),
    onSuccess: () => toast('success', t('profile.balance_alert_saved')),
    onError: (err: Error) => toast('error', err.message),
  });

  function handleSave() {
    const num = enabled ? parseFloat(value) || 0 : 0;
    mutation.mutate(num);
  }

  return (
    <Card className="mb-6">
      <Card.Header>
        <Card.Title>{t('profile.balance_alert')}</Card.Title>
      </Card.Header>
      <Card.Content>
        <div className="space-y-4">
          <div className="flex items-center justify-between gap-4">
            <div className="min-w-0">
              <div className="text-sm font-medium text-text">{t('profile.balance_alert_enabled')}</div>
              <p className="mt-0.5 text-xs text-text-tertiary">{t('profile.balance_alert_desc')}</p>
            </div>
            <Switch
              aria-label={t('profile.balance_alert_enabled')}
              isSelected={enabled}
              onChange={(v) => {
                setEnabled(v);
                if (!v) mutation.mutate(0);
              }}
            >
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
            </Switch>
          </div>
          {enabled && (
            <div className="flex flex-col sm:flex-row sm:items-end gap-3 sm:gap-4">
              <div className="flex-1">
                <HeroTextField fullWidth>
                  <Label>{t('profile.balance_alert_threshold')}</Label>
                  <div className="relative">
                    <Bell className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
                    <Input
                      className="pl-9"
                      value={value}
                      inputMode="decimal"
                      onChange={(e) => setValue(e.target.value)}
                      placeholder="5.00"
                    />
                  </div>
                  <Description>{t('profile.balance_alert_hint', { balance: balance.toFixed(2) })}</Description>
                </HeroTextField>
              </div>
              <Button
                onPress={handleSave}
                isDisabled={mutation.isPending}
                variant="primary"
                aria-busy={mutation.isPending}
              >
                <Save className="w-4 h-4" />
                {t('common.save')}
              </Button>
            </div>
          )}
        </div>
      </Card.Content>
    </Card>
  );
}
