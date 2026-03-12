import * as React from 'react';
import * as ReactDOM from 'react-dom';
import * as ReactJSXRuntime from 'react/jsx-runtime';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';
import { AuthProvider } from './app/providers/AuthProvider';
import { ThemeProvider } from './app/providers/ThemeProvider';
import { ToastProvider } from './shared/components/Toast';
import { router } from './app/router';
import './i18n';
import './index.css';

// 将 React 暴露到全局，供插件前端模块通过 shim 引用
(window as unknown as Record<string, unknown>).__airgate_shared = {
  'react': React,
  'react-dom': ReactDOM,
  'react/jsx-runtime': ReactJSXRuntime,
};

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      staleTime: 30_000,
    },
  },
});

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <ToastProvider>
          <AuthProvider>
            <RouterProvider router={router} />
          </AuthProvider>
        </ToastProvider>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
