import { useMemo, useState, useEffect } from 'react';
import { useParams } from '@tanstack/react-router';
import type { PluginFrontendModule } from '@doudou-start/airgate-theme/plugin';
import { loadPluginFrontend } from '../app/plugin-loader';
import { ChatPageLoading, PageLoading } from '../shared/components/PageLoading';

/**
 * 插件页面容器
 * 根据 URL 中的 pluginName 加载对应插件的前端模块，并渲染匹配的子路由组件。
 */
interface PluginPageProps {
  pluginNameOverride?: string;
  subPathOverride?: string;
}

export default function PluginPage({ pluginNameOverride, subPathOverride }: PluginPageProps = {}) {
  const { pluginName, _splat } = useParams({ strict: false });
  const resolvedPluginName = pluginNameOverride || pluginName;
  const [mod, setMod] = useState<PluginFrontendModule | null>(null);
  const [loadedPluginName, setLoadedPluginName] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const activeMod = loadedPluginName === resolvedPluginName ? mod : null;
  const subPath = subPathOverride || '/' + (_splat || '');
  const matched = useMemo(
    () => activeMod?.routes?.find((r) => r.path === subPath) || activeMod?.routes?.[0],
    [activeMod?.routes, subPath],
  );

  useEffect(() => {
    if (!resolvedPluginName) {
      setMod(null);
      setLoadedPluginName(null);
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setMod(null);
    setLoadedPluginName(null);
    loadPluginFrontend(resolvedPluginName).then((m) => {
      if (cancelled) return;
      setMod(m);
      setLoadedPluginName(resolvedPluginName);
      setLoading(false);
    }).catch(() => {
      if (cancelled) return;
      setMod(null);
      setLoadedPluginName(resolvedPluginName);
      setLoading(false);
    });

    return () => {
      cancelled = true;
    };
  }, [resolvedPluginName]);

  if (loading || (resolvedPluginName && loadedPluginName !== resolvedPluginName)) {
    return pluginNameOverride ? <ChatPageLoading /> : <PageLoading />;
  }

  if (!activeMod?.routes?.length) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-text-secondary">插件未提供页面</div>
      </div>
    );
  }

  if (!matched) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-text-secondary">页面未找到</div>
      </div>
    );
  }

  const PageComponent = matched.component;
  return (
    <div className="ag-plugin-scope h-full min-h-0">
      <PageComponent />
    </div>
  );
}
