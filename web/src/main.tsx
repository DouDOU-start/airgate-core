import * as React from 'react';
import * as ReactDOM from 'react-dom';
import * as ReactJSXRuntime from 'react/jsx-runtime';
import * as ReactI18next from 'react-i18next';
import { StrictMode, useState, useRef, useEffect } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';
import { AlertDialog, Button } from '@heroui/react';
import { AuthProvider } from './app/providers/AuthProvider';
import { ThemeProvider } from './app/providers/ThemeProvider';
import { SiteSettingsProvider } from './app/providers/SiteSettingsProvider';
import { ToastProvider, useToast } from './shared/ui';
import { router } from './app/router';
import './i18n';
import './index.css';

// 将 React 暴露到全局，供插件前端模块通过 shim 引用
(window as unknown as Record<string, unknown>).__airgate_shared = {
  'react': React,
  'react-dom': ReactDOM,
  'react/jsx-runtime': ReactJSXRuntime,
  'react-i18next': ReactI18next,
};

// PluginAPIBridge 把 core 内的运行时能力暴露到 window.airgate，供插件前端调用。
// 插件不能直接 useToast() —— 它们的 React 通过 shim 共享，但 ToastContext 只在
// core 的模块图里。挂全局函数是最低耦合的做法，且对插件来说只是一个普通的 window
// 调用，完全不需要引入额外依赖。
//
// 目前暴露：
//   - window.airgate.toast(kind, message, title?)        — 同 useToast().toast
//   - window.airgate.confirm(message, options?)          — 返回 Promise<boolean>
//
// 必须在 ToastProvider 内渲染，否则 useToast 拿到的是默认 noop。
type ConfirmOptions = { title?: string; danger?: boolean };
type ConfirmRequest = ConfirmOptions & { message: string; resolve: (ok: boolean) => void };

function PluginAPIBridge() {
  const { toast } = useToast();
  // 单 modal 队列：同一时刻最多展示一个 confirm；新的请求会直接 resolve(false)
  // 上一个未决的旧请求（这种"打断"语义跟原生 window.confirm 阻塞行为不同，
  // 但避免了堆叠多个 modal 把 UI 弄花）。
  const [pending, setPending] = useState<ConfirmRequest | null>(null);
  const pendingRef = useRef<ConfirmRequest | null>(null);
  pendingRef.current = pending;

  useEffect(() => {
    const w = window as unknown as {
      airgate?: {
        toast: typeof toast;
        confirm: (message: string, options?: ConfirmOptions) => Promise<boolean>;
      };
    };
    w.airgate = {
      toast,
      confirm: (message, options) =>
        new Promise<boolean>((resolve) => {
          // 打断上一个未决的请求
          if (pendingRef.current) {
            pendingRef.current.resolve(false);
          }
          setPending({ message, resolve, ...options });
        }),
    };
  }, [toast]);

  const handleClose = (ok: boolean) => {
    const cur = pendingRef.current;
    if (cur) cur.resolve(ok);
    setPending(null);
  };

  return (
    <AlertDialog
      isOpen={pending !== null}
      onOpenChange={(open) => {
        if (!open) handleClose(false);
      }}
    >
      <AlertDialog.Backdrop>
        <AlertDialog.Container placement="center" size="sm">
          <AlertDialog.Dialog className="ag-elevation-modal">
            <AlertDialog.Header>
              <AlertDialog.Icon status={pending?.danger ? 'danger' : 'accent'} />
              <AlertDialog.Heading>{pending?.title ?? '请确认'}</AlertDialog.Heading>
            </AlertDialog.Header>
            <AlertDialog.Body>{pending?.message ?? ''}</AlertDialog.Body>
            <AlertDialog.Footer>
              <Button variant="secondary" onPress={() => handleClose(false)}>
                取消
              </Button>
              <Button variant={pending?.danger ? 'danger' : 'primary'} onPress={() => handleClose(true)}>
                确认
              </Button>
            </AlertDialog.Footer>
          </AlertDialog.Dialog>
        </AlertDialog.Container>
      </AlertDialog.Backdrop>
    </AlertDialog>
  );
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      // 30s 内复用缓存避免短时间内重复打接口；但只要组件重新 mount
      // （比如用户切换侧边栏 tab 再回来）就强制 refetch，匹配"切 tab 应该看到最新数据"
      // 的直觉。window focus 仍走默认行为，不会每次回到标签页都刷一遍。
      staleTime: 30_000,
      refetchOnMount: 'always',
    },
  },
});

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <ToastProvider>
          <PluginAPIBridge />
          <SiteSettingsProvider>
            <AuthProvider>
              <RouterProvider router={router} />
            </AuthProvider>
          </SiteSettingsProvider>
        </ToastProvider>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
