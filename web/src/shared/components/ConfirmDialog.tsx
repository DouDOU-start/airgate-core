import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertDialog, Button, Spinner } from '@heroui/react';
import { DialogTriggerShim } from './DialogTriggerShim';

/**
 * 统一确认弹窗：Icon（danger/warning）+ 标题 + 描述 + 取消/确认两键。
 * loading 时确认按钮禁用并显示 Spinner；取消与遮罩关闭统一走 onOpenChange(false)。
 */
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  onConfirm,
  loading = false,
  status = 'danger',
  confirmVariant = 'danger',
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description: ReactNode;
  onConfirm: () => void;
  /** 确认请求在途状态：禁用确认键并显示 Spinner */
  loading?: boolean;
  /** 头部 Icon 状态，默认 danger */
  status?: 'danger' | 'warning';
  /** 确认按钮样式，默认 danger */
  confirmVariant?: 'danger' | 'primary';
}) {
  const { t } = useTranslation();

  return (
    <AlertDialog isOpen={open} onOpenChange={onOpenChange}>
      <DialogTriggerShim />
      <AlertDialog.Backdrop>
        <AlertDialog.Container placement="center" size="sm">
          <AlertDialog.Dialog className="ag-elevation-modal">
            <AlertDialog.Header>
              <AlertDialog.Icon status={status} />
              <AlertDialog.Heading>{title}</AlertDialog.Heading>
            </AlertDialog.Header>
            <AlertDialog.Body>{description}</AlertDialog.Body>
            <AlertDialog.Footer>
              <Button variant="secondary" onPress={() => onOpenChange(false)}>
                {t('common.cancel')}
              </Button>
              <Button
                aria-busy={loading}
                isDisabled={loading}
                variant={confirmVariant}
                onPress={onConfirm}
              >
                {loading ? <Spinner size="sm" /> : null}
                {t('common.confirm')}
              </Button>
            </AlertDialog.Footer>
          </AlertDialog.Dialog>
        </AlertDialog.Container>
      </AlertDialog.Backdrop>
    </AlertDialog>
  );
}
