import { useQuery } from '@tanstack/react-query';
import { queryKeys } from '../queryKeys';

// 探测公开状态页是否开启。
//
// /status/api/summary 是 airgate-health 插件提供的公开接口（无需登录）：
//   - 200            → 公开状态页已开启
//   - 404            → 插件已装但「公开状态页」开关被关闭（requirePublic 中间件返回）
//   - 503 / 其他     → 插件未就绪 / 未安装 / 反代未生效，对终端用户而言等同于"不可用"
//
// 用这个 hook 来决定 AppShell 顶栏与登录页底部是否展示「服务状态」入口，
// 避免用户点进去看到一片"加载失败"的空页面。
export function useStatusPageEnabled(): boolean {
  const { data } = useQuery({
    queryKey: queryKeys.statusPageEnabled(),
    queryFn: async () => {
      try {
        const r = await fetch('/status/api/summary?window=7d', {
          headers: { Accept: 'application/json' },
        });
        return r.ok;
      } catch {
        return false;
      }
    },
    staleTime: 5 * 60_000,
    retry: false,
    refetchOnWindowFocus: false,
  });
  return data === true;
}
