import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Eye, EyeOff } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input } from '../../../shared/components/Input';
import type { UserResp, UpdateUserReq } from '../../../shared/types';

interface EditUserModalProps {
  open: boolean;
  user: UserResp;
  onClose: () => void;
  onSubmit: (data: UpdateUserReq) => void;
  loading: boolean;
}

export function EditUserModal({ open, user, onClose, onSubmit, loading }: EditUserModalProps) {
  const { t } = useTranslation();
  const [form, setForm] = useState<UpdateUserReq>({
    username: user.username,
    role: user.role,
    max_concurrency: user.max_concurrency,
    status: user.status as 'active' | 'disabled',
  });
  const [showPassword, setShowPassword] = useState(false);

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    onSubmit(form);
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('users.edit')}
      footer={
        <>
          <Button type="button" variant="secondary" onClick={onClose}>{t('common.cancel')}</Button>
          <Button type="submit" form="edit-user-form" loading={loading}>{t('common.save')}</Button>
        </>
      }
    >
      <form id="edit-user-form" className="space-y-4" onSubmit={handleSubmit} noValidate>
        <Input label={t('users.email')} value={user.email} disabled />
        <Input label={t('users.username')} value={form.username ?? ''} onChange={(e) => setForm({ ...form, username: e.target.value })} />
        <div className="relative">
          <Input
            label={t('users.password')}
            type={showPassword ? 'text' : 'password'}
            placeholder={t('accounts.leave_empty_to_keep')}
            value={form.password ?? ''}
            onChange={(e) => setForm({ ...form, password: e.target.value || undefined })}
            autoComplete="new-password"
          />
          <button
            type="button"
            className="absolute right-3 bottom-[10px] text-text-tertiary hover:text-text-secondary transition-colors cursor-pointer"
            onClick={() => setShowPassword(!showPassword)}
          >
            {showPassword ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
          </button>
        </div>
        <Input
          label={t('users.max_concurrency')}
          type="number"
          min="0"
          value={String(form.max_concurrency ?? 0)}
          onChange={(e) => setForm({ ...form, max_concurrency: Number(e.target.value) })}
        />
        <div className="space-y-1.5">
          <label className="block text-xs font-medium text-text-secondary uppercase tracking-wider">{t('common.status')}</label>
          <div className="flex items-center gap-2">
            <button
              type="button"
              className="relative inline-flex h-5 w-9 items-center rounded-full transition-colors cursor-pointer"
              style={{ backgroundColor: form.status === 'active' ? 'var(--ag-success)' : 'var(--ag-text-tertiary)' }}
              onClick={() => setForm({ ...form, status: form.status === 'active' ? 'disabled' : 'active' })}
            >
              <span
                className="inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform shadow-sm"
                style={{ transform: form.status === 'active' ? 'translateX(18px)' : 'translateX(3px)' }}
              />
            </button>
            <span className="text-xs" style={{ color: form.status === 'active' ? 'var(--ag-success)' : 'var(--ag-text-tertiary)' }}>
              {form.status === 'active' ? t('status.enabled') : t('status.disabled')}
            </span>
          </div>
        </div>
      </form>
    </Modal>
  );
}
