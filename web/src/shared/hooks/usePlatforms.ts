import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { pluginsApi } from '../api/plugins';
import { queryKeys } from '../queryKeys';
import { FETCH_ALL_PARAMS } from '../constants';
import { useAuth } from '../../app/providers/AuthProvider';
import { loadPluginFrontend } from '../../app/plugin-loader';
import { registerPluginFrontendModule } from '../../app/plugin-frontend-registry';

/** 从插件 display_name 中提取平台显示名（去掉"网关""Gateway"等后缀） */
function extractPlatformName(displayName: string): string {
  return displayName
    .replace(/\s*(网关|Gateway|Plugin|插件)\s*$/i, '')
    .trim();
}

function capitalize(s: string) {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : s;
}

const loadedPlatformFrontendPlugins = new Set<string>();
const OAUTH_PLANS_METADATA_KEY = 'account.oauth_plans';

type PluginOAuthPlanMeta = {
  key?: string;
  label?: string;
};

export type OAuthPlanFilterOption = {
  id: string;
  platform: string;
  platformLabel: string;
  planLabel: string;
};

const EMPTY_OAUTH_PLAN_FILTERS: OAuthPlanFilterOption[] = [];

function parseOAuthPlanFilters(platform: string, platformLabel: string, raw?: string): OAuthPlanFilterOption[] {
  if (!raw) return [];
  try {
    const items = JSON.parse(raw) as PluginOAuthPlanMeta[];
    if (!Array.isArray(items)) return [];
    return items
      .map((item) => {
        const key = item.key?.trim();
        if (!key) return null;
        const planLabel = item.label?.trim() || key;
        return {
          id: `oauth_plan:${platform}:${key}`,
          platform,
          platformLabel,
          planLabel,
        };
      })
      .filter((item): item is OAuthPlanFilterOption => item != null);
  } catch {
    return [];
  }
}

/**
 * 从已安装的 gateway 插件中动态获取可用平台列表。
 * 同时返回 platform → 显示名的映射。
 */
export function usePlatforms() {
  const { loading: authLoading, isAPIKeySession } = useAuth();
  const { data, isLoading } = useQuery({
    queryKey: queryKeys.platforms(),
    queryFn: async () => {
      const resp = await pluginsApi.list(FETCH_ALL_PARAMS);
      const platformSet = new Set<string>();
      const nameMap: Record<string, string> = {};
      const presetsMap: Record<string, string[]> = {};
      const oauthPlanFilters: OAuthPlanFilterOption[] = [];
      const oauthPlanFilterIDs = new Set<string>();
      const iconPlugins: Array<{ name: string; platform: string }> = [];
      for (const p of resp.list) {
        if (!p.platform) continue;
        platformSet.add(p.platform);
        const raw = p.display_name || p.name || '';
        const platformLabel = raw ? extractPlatformName(raw) : capitalize(p.platform);
        if (p.name && p.has_web_assets !== false) {
          iconPlugins.push({ name: p.name, platform: p.platform });
        }
        if (!nameMap[p.platform]) {
          nameMap[p.platform] = platformLabel;
        }
        for (const option of parseOAuthPlanFilters(p.platform, platformLabel, p.metadata?.[OAUTH_PLANS_METADATA_KEY])) {
          if (oauthPlanFilterIDs.has(option.id)) continue;
          oauthPlanFilterIDs.add(option.id);
          oauthPlanFilters.push(option);
        }
        if (p.instruction_presets?.length && !presetsMap[p.platform]) {
          presetsMap[p.platform] = p.instruction_presets;
        }
      }
      return { platforms: [...platformSet], nameMap, presetsMap, oauthPlanFilters, iconPlugins };
    },
    staleTime: 60_000,
    enabled: !authLoading && !isAPIKeySession,
  });

  useEffect(() => {
    if (!data?.iconPlugins.length) return;

    data.iconPlugins.forEach(({ name, platform }) => {
      const key = `${platform.toLowerCase()}:${name}`;
      if (loadedPlatformFrontendPlugins.has(key)) return;

      loadedPlatformFrontendPlugins.add(key);
      loadPluginFrontend(name)
        .then((mod) => {
          if (mod) registerPluginFrontendModule(platform, mod);
        })
        .catch(() => {
          loadedPlatformFrontendPlugins.delete(key);
        });
    });
  }, [data]);

  return {
    platforms: data?.platforms ?? [],
    /** platform 标识符 → 显示名（如 "openai" → "OpenAI"） */
    platformName: (platform: string) => data?.nameMap[platform] || capitalize(platform),
    /** platform → 插件声明的 instruction 预设列表 */
    instructionPresets: (platform: string) => data?.presetsMap[platform] ?? [],
    /** 插件声明的 OAuth 套餐筛选项，id 可直接作为 account_type 查询值 */
    oauthPlanFilters: data?.oauthPlanFilters ?? EMPTY_OAUTH_PLAN_FILTERS,
    isLoading: authLoading || isLoading,
  };
}
