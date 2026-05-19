import type { ComponentProps, CSSProperties, ReactNode } from 'react';
import { Modal, Surface } from '@heroui/react';
import { DialogTriggerShim } from './DialogTriggerShim';

type ModalContainerProps = ComponentProps<typeof Modal.Container>;
type ModalRootProps = ComponentProps<typeof Modal>;

interface CommonModalProps {
  bodyClassName?: string;
  children: ReactNode;
  className?: string;
  description?: ReactNode;
  dialogStyle?: CSSProperties;
  footer?: ReactNode;
  icon?: ReactNode;
  iconClassName?: string;
  placement?: ModalContainerProps['placement'];
  scroll?: ModalContainerProps['scroll'];
  showCloseTrigger?: boolean;
  size?: ModalContainerProps['size'];
  state: ModalRootProps['state'];
  surface?: boolean;
  surfaceClassName?: string;
  title: ReactNode;
}

function cx(...classes: Array<string | false | null | undefined>) {
  return classes.filter(Boolean).join(' ');
}

export function CommonModal({
  bodyClassName,
  children,
  className,
  description,
  dialogStyle,
  footer,
  icon,
  iconClassName,
  placement = 'auto',
  scroll = 'inside',
  showCloseTrigger = true,
  size = 'md',
  state,
  surface = true,
  surfaceClassName,
  title,
}: CommonModalProps) {
  return (
    <Modal state={state}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement={placement} scroll={scroll} size={size}>
          <Modal.Dialog className={cx('ag-elevation-modal', className)} style={dialogStyle}>
            {showCloseTrigger ? <Modal.CloseTrigger /> : null}
            <Modal.Header>
              {icon ? (
                <Modal.Icon className={cx('bg-accent-soft text-accent-soft-foreground', iconClassName)}>
                  {icon}
                </Modal.Icon>
              ) : null}
              <Modal.Heading>{title}</Modal.Heading>
              {description ? <p className="mt-1.5 text-sm leading-5 text-muted">{description}</p> : null}
            </Modal.Header>
            <Modal.Body className={cx('p-6', bodyClassName)}>
              {surface ? (
                <Surface className={surfaceClassName} variant="default">
                  {children}
                </Surface>
              ) : children}
            </Modal.Body>
            {footer ? <Modal.Footer>{footer}</Modal.Footer> : null}
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
