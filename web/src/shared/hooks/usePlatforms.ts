import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { pluginsApi } from '../api/plugins';
import { queryKeys } from '../queryKeys';
import { FETCH_ALL_PARAMS } from '../constants';
import { useAuth } from '../../app/providers/AuthProvider';
import { loadPluginFrontend, registerPlatformIcon } from '../../app/plugin-loader';

/** 从插件 display_name 中提取平台显示名（去掉"网关""Gateway"等后缀） */
function extractPlatformName(displayName: string): string {
  return displayName
    .replace(/\s*(网关|Gateway|Plugin|插件)\s*$/i, '')
    .trim();
}

function capitalize(s: string) {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : s;
}

const loadedPlatformIconPlugins = new Set<string>();

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
      const presetsMap: Record<string, string[]> = {};
      const iconPlugins: Array<{ name: string; platform: string }> = [];
      for (const p of resp.list) {
        if (!p.platform) continue;
        platformSet.add(p.platform);
        if (p.name && p.has_web_assets !== false) {
          iconPlugins.push({ name: p.name, platform: p.platform });
        }
        if (!nameMap[p.platform]) {
          const raw = p.display_name || p.name || '';
          nameMap[p.platform] = raw ? extractPlatformName(raw) : capitalize(p.platform);
        }
        if (p.instruction_presets?.length && !presetsMap[p.platform]) {
          presetsMap[p.platform] = p.instruction_presets;
        }
      }
      return { platforms: [...platformSet], nameMap, presetsMap, iconPlugins };
    },
    staleTime: 60_000,
    enabled: !isAPIKeySession,
  });

  useEffect(() => {
    if (!data?.iconPlugins.length) return;

    data.iconPlugins.forEach(({ name, platform }) => {
      const key = `${platform.toLowerCase()}:${name}`;
      if (loadedPlatformIconPlugins.has(key)) return;

      loadedPlatformIconPlugins.add(key);
      loadPluginFrontend(name)
        .then((mod) => {
          if (!mod?.platformIcon) return;
          registerPlatformIcon(platform, mod.platformIcon);
        })
        .catch(() => {
          loadedPlatformIconPlugins.delete(key);
        });
    });
  }, [data]);

  return {
    platforms: data?.platforms ?? [],
    /** platform 标识符 → 显示名（如 "openai" → "OpenAI"） */
    platformName: (platform: string) => data?.nameMap[platform] || capitalize(platform),
    /** platform → 插件声明的 instruction 预设列表 */
    instructionPresets: (platform: string) => data?.presetsMap[platform] ?? [],
    isLoading,
  };
}
