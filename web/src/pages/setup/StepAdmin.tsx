import { type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, FieldError, Form, Input, Label, Meter, TextField as HeroTextField } from '@heroui/react';
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
  const getPasswordStrength = (pwd: string): { label: string; status: 'danger' | 'warning' | 'success'; value: number } => {
    if (pwd.length < 6) return { label: t('setup.password_too_short'), status: 'danger', value: 20 };
    if (pwd.length < 8) return { label: t('setup.strength_weak'), status: 'danger', value: 35 };
    const hasUpper = /[A-Z]/.test(pwd);
    const hasLower = /[a-z]/.test(pwd);
    const hasNumber = /\d/.test(pwd);
    const hasSpecial = /[^A-Za-z0-9]/.test(pwd);
    const score = [hasUpper, hasLower, hasNumber, hasSpecial].filter(Boolean).length;
    if (score >= 3 && pwd.length >= 10) return { label: t('setup.strength_strong'), status: 'success', value: 100 };
    if (score >= 2) return { label: t('setup.strength_fair'), status: 'warning', value: 65 };
    return { label: t('setup.strength_weak'), status: 'danger', value: 35 };
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
    <Form className="space-y-4" onSubmit={handleSubmit}>
      <p className="text-sm text-text-secondary mb-2">
        {t('setup.step_admin_desc')}
      </p>
      <HeroTextField fullWidth isRequired>
        <Label>{t('setup.admin_email')}</Label>
        <Input
          name="email"
          type="email"
          value={data.email}
          onChange={(e) => update('email', e.target.value)}
          placeholder="admin@example.com"
          autoComplete="email"
          required
        />
      </HeroTextField>
      <div>
        <HeroTextField fullWidth isInvalid={passwordTooShort} isRequired>
          <Label>{t('profile.new_password')}</Label>
          <Input
            name="new-password"
            type="password"
            value={data.password}
            onChange={(e) => update('password', e.target.value)}
            placeholder={t('setup.password_too_short')}
            autoComplete="new-password"
            aria-invalid={passwordTooShort || undefined}
            required
          />
          {passwordTooShort ? <FieldError>{t('setup.password_too_short')}</FieldError> : null}
        </HeroTextField>
        {strength && !passwordTooShort && (
          <Meter
            aria-label={t('setup.password_strength')}
            className="mt-2"
            color={strength.status}
            maxValue={100}
            minValue={0}
            size="sm"
            value={strength.value}
          >
            <Meter.Track>
              <Meter.Fill />
            </Meter.Track>
            <Meter.Output>
              {t('setup.password_strength')}:{strength.label}
            </Meter.Output>
          </Meter>
        )}
      </div>
      <HeroTextField fullWidth isInvalid={Boolean(passwordMismatch)} isRequired>
        <Label>{t('profile.confirm_new_password')}</Label>
        <Input
          name="confirm-new-password"
          type="password"
          value={data.confirmPassword}
          onChange={(e) => update('confirmPassword', e.target.value)}
          placeholder={t('profile.confirm_placeholder')}
          autoComplete="new-password"
          aria-invalid={Boolean(passwordMismatch) || undefined}
          required
        />
        {passwordMismatch ? <FieldError>{t('profile.password_mismatch')}</FieldError> : null}
      </HeroTextField>

      {/* 操作按钮 */}
      <div className="flex justify-between pt-4">
        {onPrev ? (
          <Button
            type="button"
            variant="ghost"
            onPress={onPrev}
          >
            <ArrowLeft className="w-4 h-4" />
            {t('setup.step_redis')}
          </Button>
        ) : (
          <span />
        )}
        <Button
          type="submit"
          isDisabled={!canProceed}
        >
          <ArrowRight className="w-4 h-4" />
          {t('setup.step_finish')}
        </Button>
      </div>
    </Form>
  );
}
