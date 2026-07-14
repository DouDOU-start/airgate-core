import { useTranslation } from 'react-i18next';
import { Copy, ExternalLink, Globe } from 'lucide-react';
import { useSiteSettings } from '../../../app/providers/SiteSettingsProvider';
import { useClipboard } from '../../../shared/hooks/useClipboard';
import { parseCustomEndpoints, speedTestUrl } from '../../../shared/utils/endpoints';

// 展示网关的默认接入地址 + 管理员配置的备用地址（镜像域名/备用线路），
// 支持一键复制；"测速"跳转第三方公开测速站，网关本身不发起探测请求。
export function EndpointsBar() {
  const { t } = useTranslation();
  const copy = useClipboard();
  const site = useSiteSettings();

  const baseUrl = site.api_base_url || window.location.origin;
  const extra = parseCustomEndpoints(site.custom_endpoints).filter((e) => e.endpoint.trim());
  const endpoints = [
    { name: t('user_keys.endpoint_default'), endpoint: baseUrl, description: '' },
    ...extra,
  ];

  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="flex shrink-0 items-center gap-1.5 text-xs font-semibold text-accent">
        <Globe className="w-3.5 h-3.5" />
        {t('user_keys.endpoints_label')}
      </span>
      {endpoints.map((ep, idx) => (
        <div
          key={idx}
          className="inline-flex max-w-full items-center gap-1.5 rounded-full border border-glass-border bg-surface px-3 py-1.5 text-xs transition-colors hover:border-accent/50"
          title={ep.description || ep.endpoint}
        >
          {ep.name ? (
            <>
              <span className="shrink-0 font-medium text-text-secondary">{ep.name}</span>
              <span className="shrink-0 text-text-tertiary">|</span>
            </>
          ) : null}
          <span className="min-w-0 max-w-[220px] truncate font-mono text-text-secondary">{ep.endpoint}</span>
          <button
            aria-label={t('common.copy')}
            className="shrink-0 text-text-tertiary hover:text-accent"
            title={t('common.copy')}
            type="button"
            onClick={() => copy(ep.endpoint, t('user_keys.copied'))}
          >
            <Copy className="w-3 h-3" />
          </button>
          <a
            className="shrink-0 text-text-tertiary hover:text-accent"
            href={speedTestUrl(ep.endpoint)}
            rel="noopener noreferrer"
            target="_blank"
            title={t('user_keys.endpoint_test')}
            aria-label={t('user_keys.endpoint_test')}
          >
            <ExternalLink className="w-3 h-3" />
          </a>
        </div>
      ))}
    </div>
  );
}
