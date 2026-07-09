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
      // stale-while-revalidate：切页返回 / 窗口回焦时先渲染缓存，再后台刷新。
      // 缓存数据立即可见不阻塞首屏，但保证导航和跨标签页操作后数据不长期滞留。
      // 个别高频或昂贵查询可在调用处用 staleTime / refetchOnWindowFocus 覆盖。
      staleTime: 0,
      refetchOnMount: true,
      refetchOnWindowFocus: true,
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
