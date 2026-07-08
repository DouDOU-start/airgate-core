import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { useTranslation } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';
import { I18nProvider } from '@heroui/react';
import { AuthProvider } from './app/providers/AuthProvider';
import { ThemeProvider } from './app/providers/ThemeProvider';
import { SiteSettingsProvider } from './app/providers/SiteSettingsProvider';
import { ToastProvider } from './shared/ui';
import { router } from './app/router';
import './i18n';
import './index.css';

function AppProviders() {
  const { i18n } = useTranslation();
  const locale = i18n.resolvedLanguage === 'en' ? 'en-US' : 'zh-CN';

  return (
    <I18nProvider locale={locale}>
      <SiteSettingsProvider>
        <AuthProvider>
          <RouterProvider router={router} />
        </AuthProvider>
      </SiteSettingsProvider>
    </I18nProvider>
  );
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      // 短时间内切页返回直接复用缓存，避免路由切换时重复请求抢占首屏渲染。
      staleTime: 60_000,
      refetchOnMount: true,
      refetchOnWindowFocus: false,
    },
  },
});

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <ToastProvider>
          <AppProviders />
        </ToastProvider>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
