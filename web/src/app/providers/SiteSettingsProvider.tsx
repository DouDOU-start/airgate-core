import { createContext, useContext, useEffect, useMemo, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { settingsApi } from '../../shared/api/settings';
import { queryKeys } from '../../shared/queryKeys';
import defaultLogoUrl from '../../assets/logo.svg';

export { defaultLogoUrl };

interface SiteSettings {
  site_name: string;
  site_subtitle: string;
  site_logo: string;
  api_base_url: string;
  frontend_url: string;
  contact_info: string;
  doc_url: string;
  home_content: string;
  registration_enabled: boolean;
  email_verify_enabled: boolean;
  settings_loaded: boolean;
}

const defaults: SiteSettings = {
  site_name: 'AirGate',
  site_subtitle: 'Control Panel',
  site_logo: '',
  api_base_url: '',
  frontend_url: '',
  contact_info: '',
  doc_url: '',
  home_content: '',
  registration_enabled: true,
  email_verify_enabled: false,
  settings_loaded: false,
};

const SiteSettingsContext = createContext<SiteSettings>(defaults);

export function SiteSettingsProvider({ children }: { children: ReactNode }) {
  const { data, isPending } = useQuery({
    queryKey: queryKeys.siteSettings(),
    queryFn: () => settingsApi.getPublic(),
    staleTime: 60_000,
    refetchOnWindowFocus: true,
  });

  const value: SiteSettings = useMemo(() => ({
    ...defaults,
    ...data,
    // Boolean 字段从字符串转换
    registration_enabled: data?.registration_enabled !== 'false',
    email_verify_enabled: data?.email_verify_enabled === 'true',
    settings_loaded: !isPending,
  }), [data, isPending]);

  // 动态设置 favicon（优先自定义 logo，否则使用默认 logo）
  useEffect(() => {
    const logoHref = value.site_logo || defaultLogoUrl;
    let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    if (!link) {
      link = document.createElement('link');
      link.rel = 'icon';
      document.head.appendChild(link);
    }
    link.href = logoHref;
  }, [value.site_logo]);

  return (
    <SiteSettingsContext.Provider value={value}>
      {children}
    </SiteSettingsContext.Provider>
  );
}

export function useSiteSettings(): SiteSettings {
  return useContext(SiteSettingsContext);
}
