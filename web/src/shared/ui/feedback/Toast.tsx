import type { ReactNode } from 'react';
import { ToastProvider as HeroToastProvider, toast as heroToast } from '@heroui/react';

export type ToastType = 'success' | 'error' | 'danger' | 'warning' | 'info';

export interface ToastApi {
  toast: (type: ToastType, message: ReactNode, title?: ReactNode) => string;
}

function notify(type: ToastType, message: ReactNode, title?: ReactNode): string {
  const options = title ? { description: message } : undefined;
  const content = title ?? message;

  switch (type) {
    case 'success':
      return heroToast.success(content, options);
    case 'warning':
      return heroToast.warning(content, options);
    case 'error':
    case 'danger':
      return heroToast.danger(content, options);
    case 'info':
    default:
      return heroToast.info(content, options);
  }
}

const toastApi: ToastApi = { toast: notify };

export function ToastProvider({ children }: { children: ReactNode }) {
  return (
    <>
      {children}
      <HeroToastProvider maxVisibleToasts={4} placement="top end" width={420} />
    </>
  );
}

export function useToast(): ToastApi {
  return toastApi;
}

export { heroToast as toast };
