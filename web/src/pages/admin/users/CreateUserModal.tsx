import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Eye, EyeOff, RefreshCw } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input } from '../../../shared/components/Input';
import { useClipboard } from '../../../shared/hooks/useClipboard';
import type { CreateUserReq } from '../../../shared/types';

interface CreateUserModalProps {
  open: boolean;
  onClose: () => void;
  onSubmit: (data: CreateUserReq) => void;
  loading: boolean;
}

const defaultForm: CreateUserReq = {
  email: '', password: '', username: '', role: 'user', max_concurrency: 0,
};

export function CreateUserModal({ open, onClose, onSubmit, loading }: CreateUserModalProps) {
  const { t } = useTranslation();
  const copy = useClipboard();
  const [form, setForm] = useState<CreateUserReq>(defaultForm);
  const [showPassword, setShowPassword] = useState(false);

  const generatePassword = () => {
    const chars = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%&*';
    const arr = new Uint8Array(16);
    crypto.getRandomValues(arr);
    const pwd = Array.from(arr, (b) => chars[b % chars.length]).join('');
    setForm({ ...form, password: pwd });
    copy(pwd);
  };

  const handleSubmit = (event?: FormEvent<HTMLFormElement>) => {
    event?.preventDefault();
    if (!form.email || !form.password) return;
    onSubmit(form);
  };

  const handleClose = () => {
    setForm(defaultForm);
    onClose();
  };

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title={t('users.create')}
      footer={
        <>
          <Button type="button" variant="secondary" onClick={handleClose}>{t('common.cancel')}</Button>
          <Button type="submit" form="create-user-form" loading={loading}>{t('common.create')}</Button>
        </>
      }
    >
      <form id="create-user-form" className="space-y-4" onSubmit={handleSubmit} noValidate>
        <Input label={t('users.email')} type="email" required value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} autoComplete="email" />
        <div>
          <div className="relative">
            <Input label={t('users.password')} type={showPassword ? 'text' : 'password'} required value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} autoComplete="new-password" />
            <button
              type="button"
              className="absolute right-3 bottom-[10px] text-text-tertiary hover:text-text-secondary transition-colors cursor-pointer"
              onClick={() => setShowPassword(!showPassword)}
            >
              {showPassword ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
            </button>
          </div>
          <button
            type="button"
            className="mt-1.5 flex items-center gap-1 text-[11px] text-primary hover:text-primary/80 transition-colors cursor-pointer"
            onClick={generatePassword}
          >
            <RefreshCw className="w-3 h-3" />
            {t('users.generate_password')}
          </button>
        </div>
        <Input label={t('users.username')} value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} />
        <Input
          label={t('users.max_concurrency')}
          type="number"
          min="0"
          value={String(form.max_concurrency ?? 0)}
          onChange={(e) => setForm({ ...form, max_concurrency: Number(e.target.value) })}
        />
      </form>
    </Modal>
  );
}
