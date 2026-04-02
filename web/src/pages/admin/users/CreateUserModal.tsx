import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Eye, EyeOff, RefreshCw } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input } from '../../../shared/components/Input';
import { useToast } from '../../../shared/components/Toast';
import type { CreateUserReq } from '../../../shared/types';

interface CreateUserModalProps {
  open: boolean;
  onClose: () => void;
  onSubmit: (data: CreateUserReq) => void;
  loading: boolean;
}

const defaultForm: CreateUserReq = {
  email: '', password: '', username: '', role: 'user', max_concurrency: 5,
};

export function CreateUserModal({ open, onClose, onSubmit, loading }: CreateUserModalProps) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const [form, setForm] = useState<CreateUserReq>(defaultForm);
  const [showPassword, setShowPassword] = useState(false);

  const generatePassword = () => {
    const chars = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%&*';
    const arr = new Uint8Array(16);
    crypto.getRandomValues(arr);
    const pwd = Array.from(arr, (b) => chars[b % chars.length]).join('');
    setForm({ ...form, password: pwd });
    navigator.clipboard.writeText(pwd).then(
      () => toast('success', t('common.copied')),
      () => { /* ignore */ },
    );
  };

  const handleSubmit = () => {
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
          <Button variant="secondary" onClick={handleClose}>{t('common.cancel')}</Button>
          <Button onClick={handleSubmit} loading={loading}>{t('common.create')}</Button>
        </>
      }
    >
      <div className="space-y-4">
        <Input label={t('users.email')} type="email" required value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} />
        <div>
          <div className="relative">
            <Input label={t('users.password')} type={showPassword ? 'text' : 'password'} required value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} />
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
          value={String(form.max_concurrency ?? 5)}
          onChange={(e) => setForm({ ...form, max_concurrency: Number(e.target.value) })}
        />
      </div>
    </Modal>
  );
}
