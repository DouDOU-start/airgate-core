import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Input, Label, Modal, Spinner, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { Eye, EyeOff, RefreshCw } from 'lucide-react';
import { useClipboard } from '../../../shared/hooks/useClipboard';
import type { CreateUserReq } from '../../../shared/types';

interface CreateUserModalProps {
  open: boolean;
  onClose: () => void;
  onSubmit: (data: CreateUserReq) => void;
  loading: boolean;
}

const defaultForm: CreateUserReq = {
  email: '',
  max_concurrency: 0,
  password: '',
  role: 'user',
  username: '',
};

export function CreateUserModal({ open, onClose, onSubmit, loading }: CreateUserModalProps) {
  const { t } = useTranslation();
  const copy = useClipboard();
  const [form, setForm] = useState<CreateUserReq>(defaultForm);
  const [showPassword, setShowPassword] = useState(false);

  const handleClose = () => {
    setForm(defaultForm);
    onClose();
  };

  const generatePassword = () => {
    const chars = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%&*';
    const arr = new Uint8Array(16);
    crypto.getRandomValues(arr);
    const pwd = Array.from(arr, (b) => chars[b % chars.length]).join('');
    setForm({ ...form, password: pwd });
    copy(pwd);
  };

  const handleSubmit = () => {
    if (!form.email || !form.password) return;
    onSubmit(form);
  };
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) handleClose();
    },
  });

  return (
    <Modal state={modalState}>
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{t('users.create')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <HeroTextField fullWidth isRequired>
                  <Label>{t('users.email')}</Label>
                  <Input
                    name="email"
                    type="email"
                    value={form.email}
                    onChange={(e) => setForm({ ...form, email: e.target.value })}
                    autoComplete="email"
                    required
                  />
                </HeroTextField>
                <div className="space-y-1.5">
                  <HeroTextField fullWidth isRequired>
                    <Label>{t('users.password')}</Label>
                    <div className="relative">
                      <Input
                        className="pr-10"
                        name="new-password"
                        type={showPassword ? 'text' : 'password'}
                        value={form.password}
                        onChange={(e) => setForm({ ...form, password: e.target.value })}
                        autoComplete="new-password"
                        required
                      />
                      <Button
                        isIconOnly
                        aria-label={showPassword ? 'Hide password' : 'Show password'}
                        className="absolute right-1 top-1/2 z-10 -translate-y-1/2"
                        size="sm"
                        type="button"
                        variant="ghost"
                        onPress={() => setShowPassword((value) => !value)}
                      >
                        {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                      </Button>
                    </div>
                  </HeroTextField>
                  <Button size="sm" variant="ghost" onPress={generatePassword}>
                    <RefreshCw className="h-3 w-3" />
                    {t('users.generate_password')}
                  </Button>
                </div>
                <HeroTextField fullWidth>
                  <Label>{t('users.username')}</Label>
                  <Input
                    name="username"
                    value={form.username}
                    onChange={(e) => setForm({ ...form, username: e.target.value })}
                    autoComplete="username"
                  />
                </HeroTextField>
                <HeroTextField fullWidth>
                  <Label>{t('users.max_concurrency')}</Label>
                  <Input
                    type="number"
                    min="0"
                    value={String(form.max_concurrency ?? 0)}
                    onChange={(e) => setForm({ ...form, max_concurrency: Number(e.target.value) })}
                  />
                </HeroTextField>
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={handleClose}>
                {t('common.cancel')}
              </Button>
              <Button variant="primary" isDisabled={loading} onPress={handleSubmit}>
                {loading ? <Spinner size="sm" /> : null}
                {t('common.create')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
