import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input } from '../../../shared/components/Input';
import type { UserResp, AdjustBalanceReq } from '../../../shared/types';

interface BalanceModalProps {
  open: boolean;
  user: UserResp;
  defaultAction: 'add' | 'subtract';
  onClose: () => void;
  onSubmit: (data: AdjustBalanceReq) => void;
  loading: boolean;
}

export function BalanceModal({ open, user, defaultAction, onClose, onSubmit, loading }: BalanceModalProps) {
  const { t } = useTranslation();
  const [form, setForm] = useState<AdjustBalanceReq>({
    action: defaultAction, amount: 0, remark: t('users.remark_admin_adjust'),
  });

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={defaultAction === 'add' ? t('users.topup') : t('users.refund')}
      width="420px"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>{t('common.cancel')}</Button>
          <Button onClick={() => onSubmit(form)} loading={loading}>{t('common.confirm')}</Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="border border-glass-border bg-bg-elevated shadow-sm rounded-lg px-4 py-3">
          <p className="text-xs text-text-tertiary uppercase tracking-wider">{t('users.current_balance')}</p>
          <p className="text-lg font-bold mt-1 font-mono">${user.balance.toFixed(2)}</p>
        </div>
        <div>
          <Input
            label={t('users.amount')}
            type="number"
            required
            min="0"
            max={defaultAction === 'subtract' ? String(user.balance) : undefined}
            step="0.01"
            value={String(form.amount)}
            onChange={(e) => {
              const val = Number(e.target.value);
              setForm({ ...form, amount: defaultAction === 'subtract' ? Math.min(val, user.balance) : val });
            }}
          />
          {defaultAction === 'subtract' && (
            <button
              type="button"
              className="mt-1 text-[11px] text-primary hover:text-primary/80 transition-colors cursor-pointer"
              onClick={() => setForm({ ...form, amount: user.balance })}
            >
              {t('users.withdraw_all')}
            </button>
          )}
        </div>
        <Input
          label={t('users.remark')}
          placeholder={t('users.remark_placeholder')}
          value={form.remark ?? ''}
          onChange={(e) => setForm({ ...form, remark: e.target.value })}
        />
      </div>
    </Modal>
  );
}
