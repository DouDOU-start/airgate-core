import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Input, Label, Modal, Spinner, TextArea, TextField as HeroTextField, useOverlayState } from '@heroui/react';
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
    action: defaultAction,
    amount: 0,
    remark: t('users.remark_admin_adjust'),
  });

  const isRefund = defaultAction === 'subtract';
  const afterBalance = useMemo(() => {
    const amount = Number.isFinite(form.amount) ? form.amount : 0;
    return user.balance + (isRefund ? -amount : amount);
  }, [form.amount, isRefund, user.balance]);
  const avatarLetter = (user.email || user.username || '?').charAt(0).toUpperCase();
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  return (
    <Modal state={modalState}>
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="sm">
          <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '460px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{isRefund ? t('users.refund') : t('users.topup')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <div className="flex items-center gap-3 rounded-lg border border-glass-border bg-bg-elevated px-4 py-3">
                  <div className="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-full bg-primary-subtle text-base font-semibold text-primary">
                    {avatarLetter}
                  </div>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium text-text">{user.email}</p>
                    <p className="mt-0.5 font-mono text-xs text-text-tertiary">
                      {t('users.current_balance')}: ${user.balance.toFixed(7)}
                    </p>
                  </div>
                </div>

                <div>
                  <HeroTextField fullWidth isRequired>
                    <Label>{isRefund ? t('users.refund_amount', '退款金额') : t('users.topup_amount', '充值金额')}</Label>
                    <div className="relative">
                      <span className="pointer-events-none absolute left-3 top-1/2 z-10 -translate-y-1/2 text-sm text-text-tertiary">$</span>
                      <Input
                        className="pl-7"
                        type="number"
                        min="0"
                        max={isRefund ? String(user.balance) : undefined}
                        step="0.01"
                        value={String(form.amount)}
                        onChange={(e) => {
                          const value = Number(e.target.value);
                          setForm({ ...form, amount: isRefund ? Math.min(value, user.balance) : value });
                        }}
                        required
                      />
                    </div>
                  </HeroTextField>
                  {isRefund ? (
                    <Button size="sm" variant="ghost" onPress={() => setForm({ ...form, amount: user.balance })}>
                      {t('users.withdraw_all')}
                    </Button>
                  ) : null}
                </div>

                <HeroTextField fullWidth>
                  <Label>{t('users.remark')}</Label>
                  <TextArea
                    placeholder={t('users.remark_placeholder')}
                    value={form.remark ?? ''}
                    onChange={(e) => setForm({ ...form, remark: e.target.value })}
                  />
                </HeroTextField>

                <div className="flex items-center justify-between rounded-lg border border-primary/30 bg-primary-subtle px-4 py-3">
                  <span className="text-sm text-text-secondary">
                    {t('users.balance_after_op', '操作后余额')}:
                  </span>
                  <span className={`font-mono text-lg font-bold ${afterBalance < 0 ? 'text-danger' : 'text-text'}`}>
                    ${afterBalance.toFixed(7)}
                  </span>
                </div>
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button variant="primary" isDisabled={loading} onPress={() => onSubmit(form)}>
                {loading ? <Spinner size="sm" /> : null}
                {t('common.confirm')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
