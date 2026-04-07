import { useQuery } from '@tanstack/react-query';
import { pluginsApi } from '../api/plugins';
import { queryKeys } from '../queryKeys';
import { FETCH_ALL_PARAMS } from '../constants';
import { useAuth } from '../../app/providers/AuthProvider';

/** 从插件 display_name 中提取平台显示名（去掉"网关""Gateway"等后缀） */
function extractPlatformName(displayName: string): string {
  return displayName
    .replace(/\s*(网关|Gateway|Plugin|插件)\s*$/i, '')
    .trim();
}

function capitalize(s: string) {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : s;
}

/**
 * 从已安装的 gateway 插件中动态获取可用平台列表。
 * 同时返回 platform → 显示名的映射。
 */
export function usePlatforms() {
  const { isAPIKeySession } = useAuth();
  const { data, isLoading } = useQuery({
    queryKey: queryKeys.platforms(),
    queryFn: async () => {
      const resp = await pluginsApi.list(FETCH_ALL_PARAMS);
      const platformSet = new Set<string>();
      const nameMap: Record<string, string> = {};
      for (const p of resp.list) {
        if (!p.platform) continue;
        platformSet.add(p.platform);
        if (!nameMap[p.platform]) {
          const raw = p.display_name || p.name || '';
          nameMap[p.platform] = raw ? extractPlatformName(raw) : capitalize(p.platform);
        }
      }
      return { platforms: [...platformSet], nameMap };
    },
    staleTime: 60_000,
    enabled: !isAPIKeySession,
  });

  return {
    platforms: data?.platforms ?? [],
    /** platform 标识符 → 显示名（如 "openai" → "OpenAI"） */
    platformName: (platform: string) => data?.nameMap[platform] || capitalize(platform),
    isLoading,
  };
}
