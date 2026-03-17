import { useState, useEffect } from 'react';
import { useParams } from '@tanstack/react-router';
import { loadPluginFrontend, type PluginFrontendModule } from '../app/plugin-loader';

/**
 * 插件页面容器
 * 根据 URL 中的 pluginName 加载对应插件的前端模块，并渲染匹配的子路由组件。
 */
export default function PluginPage() {
  const { pluginName, _splat } = useParams({ strict: false });
  const [mod, setMod] = useState<PluginFrontendModule | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!pluginName) return;
    setLoading(true);
    loadPluginFrontend(pluginName).then((m) => {
      setMod(m);
      setLoading(false);
    });
  }, [pluginName]);

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-text-secondary">加载插件页面...</div>
      </div>
    );
  }

  if (!mod?.routes?.length) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-text-secondary">插件未提供页面</div>
      </div>
    );
  }

  // 从 _splat 匹配插件声明的路由
  const subPath = '/' + (_splat || '');
  const matched = mod.routes.find((r) => r.path === subPath) || mod.routes[0];

  if (!matched) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-text-secondary">页面未找到</div>
      </div>
    );
  }

  const PageComponent = matched.component;
  return (
    <div className="ag-plugin-scope">
      <PageComponent />
    </div>
  );
}
