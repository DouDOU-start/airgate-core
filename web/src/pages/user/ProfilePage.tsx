import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAuth } from '../../app/providers/AuthProvider';
import { usersApi } from '../../shared/api/users';
import { useToast } from '../../shared/components/Toast';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { Badge } from '../../shared/components/Badge';
import { Card } from '../../shared/components/Card';
import { Button } from '../../shared/components/Button';
import { Input } from '../../shared/components/Input';
import {
  User,
  Mail,
  Shield,
  Wallet,
  Layers,
  Save,
  Lock,
  KeyRound,
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
    <div className="p-6 max-w-3xl mx-auto">
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
        <div className="flex flex-col sm:flex-row sm:items-end gap-3 sm:gap-4">
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
    </div>
  );
}
