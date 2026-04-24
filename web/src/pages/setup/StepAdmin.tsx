import { type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '../../shared/components/Button';
import { Input } from '../../shared/components/Input';
import {
  ArrowLeft,
  ArrowRight,
} from 'lucide-react';
import type { AdminSetup } from '../../shared/types';

export interface StepAdminProps {
  data: AdminSetup & { confirmPassword: string };
  onChange: (data: AdminSetup & { confirmPassword: string }) => void;
  // 当 admin 是第一步（DB / Redis 全部由 env 提供）时为 undefined，不渲染返回按钮
  onPrev?: () => void;
  onNext: () => void;
}

export default function StepAdmin({ data, onChange, onPrev, onNext }: StepAdminProps) {
  const { t } = useTranslation();

  const update = (field: string, value: string) => {
    onChange({ ...data, [field]: value });
  };

  // 密码强度检查
  const getPasswordStrength = (pwd: string): { label: string; color: string; width: string } => {
    if (pwd.length < 6) return { label: t('setup.password_too_short'), color: 'var(--ag-danger)', width: '20%' };
    if (pwd.length < 8) return { label: t('setup.strength_weak'), color: 'var(--ag-danger)', width: '35%' };
    const hasUpper = /[A-Z]/.test(pwd);
    const hasLower = /[a-z]/.test(pwd);
    const hasNumber = /\d/.test(pwd);
    const hasSpecial = /[^A-Za-z0-9]/.test(pwd);
    const score = [hasUpper, hasLower, hasNumber, hasSpecial].filter(Boolean).length;
    if (score >= 3 && pwd.length >= 10) return { label: t('setup.strength_strong'), color: 'var(--ag-success)', width: '100%' };
    if (score >= 2) return { label: t('setup.strength_fair'), color: 'var(--ag-warning)', width: '65%' };
    return { label: t('setup.strength_weak'), color: 'var(--ag-danger)', width: '35%' };
  };

  const passwordMismatch = data.confirmPassword && data.password !== data.confirmPassword;
  const passwordTooShort = data.password.length > 0 && data.password.length < 8;
  const strength = data.password ? getPasswordStrength(data.password) : null;

  const canProceed =
    data.email.trim() !== '' &&
    data.password.length >= 8 &&
    data.password === data.confirmPassword;

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (canProceed) onNext();
  };

  return (
    <form className="space-y-4" onSubmit={handleSubmit} noValidate>
      <p className="text-sm text-text-secondary mb-2">
        {t('setup.step_admin_desc')}
      </p>
      <Input
        label={t('setup.admin_email')}
        type="email"
        value={data.email}
        onChange={(e) => update('email', e.target.value)}
        placeholder="admin@example.com"
        autoComplete="email"
        required
      />
      <div>
        <Input
          label={t('profile.new_password')}
          type="password"
          value={data.password}
          onChange={(e) => update('password', e.target.value)}
          placeholder={t('setup.password_too_short')}
          autoComplete="new-password"
          required
          error={passwordTooShort ? t('setup.password_too_short') : undefined}
        />
        {strength && !passwordTooShort && (
          <div className="mt-2 space-y-1">
            <div
              className="h-1 rounded-full overflow-hidden"
              style={{ background: 'var(--ag-bg-surface)' }}
            >
              <div
                className="h-full rounded-full transition-all duration-300"
                style={{ width: strength.width, background: strength.color }}
              />
            </div>
            <p className="text-xs" style={{ color: strength.color }}>
              {t('setup.password_strength')}:{strength.label}
            </p>
          </div>
        )}
      </div>
      <Input
        label={t('profile.confirm_new_password')}
        type="password"
        value={data.confirmPassword}
        onChange={(e) => update('confirmPassword', e.target.value)}
        placeholder={t('profile.confirm_placeholder')}
        autoComplete="new-password"
        required
        error={passwordMismatch ? t('profile.password_mismatch') : undefined}
      />

      {/* 操作按钮 */}
      <div className="flex justify-between pt-4">
        {onPrev ? (
          <Button
            type="button"
            variant="ghost"
            onClick={onPrev}
            icon={<ArrowLeft className="w-4 h-4" />}
          >
            {t('setup.step_redis')}
          </Button>
        ) : (
          <span />
        )}
        <Button
          type="submit"
          disabled={!canProceed}
          icon={<ArrowRight className="w-4 h-4" />}
        >
          {t('setup.step_finish')}
        </Button>
      </div>
    </form>
  );
}
