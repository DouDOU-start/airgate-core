import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import http from 'node:http';

const BACKEND = 'http://localhost:9517';
const backendUrl = new URL(BACKEND);

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    {
      name: 'api-key-proxy',
      configureServer(server) {
        // 携带 Bearer token 的请求一律代理到后端（API Key 调用），支持 SSE 流式
        server.middlewares.use((req, res, next) => {
          const auth = req.headers.authorization;
          if (auth && auth.startsWith('Bearer ')) {
            const headers = { ...req.headers, host: backendUrl.host };
            const proxyReq = http.request(
              {
                hostname: backendUrl.hostname,
                port: backendUrl.port,
                path: req.url,
                method: req.method,
                headers,
              },
              (proxyRes) => {
                // 流式响应：禁用压缩，逐块转发
                res.writeHead(proxyRes.statusCode ?? 502, proxyRes.headers);
                proxyRes.on('data', (chunk) => {
                  res.write(chunk);
                  // 强制刷新，确保 SSE 数据立即发送
                  if (typeof (res as NodeJS.WritableStream & { flush?: () => void }).flush === 'function') {
                    (res as NodeJS.WritableStream & { flush?: () => void }).flush!();
                  }
                });
                proxyRes.on('end', () => res.end());
                proxyRes.on('error', () => res.end());
              },
            );
            proxyReq.on('error', () => {
              res.writeHead(502);
              res.end('Backend unavailable');
            });
            req.pipe(proxyReq);
            return;
          }
          next();
        });
      },
    },
  ],
  optimizeDeps: {
    // SDK 是 file: 链接，不预打包，确保改 token 后立即生效
    exclude: ['@airgate/theme'],
  },
  build: {
    rollupOptions: {
      output: {
        manualChunks: {
          vendor: ['react', 'react-dom', '@tanstack/react-router', '@tanstack/react-query', 'i18next', 'react-i18next'],
        },
      },
    },
  },
  server: {
    host: '0.0.0.0',
    port: 3000,
    watch: {
      usePolling: true,
      interval: 1000,
      // 监听 SDK 符号链接目标，token 变更后自动热更新
      ignored: ['!**/node_modules/@airgate/theme/**'],
    },
    proxy: {
      '/api': BACKEND,
      '/uploads': BACKEND,
      // 注意：只代理插件 assets 的请求路径（用 bypass 函数细分）。
      // /plugins/{name}/{页面} 是 SPA 路由（由 PluginPage 内部加载组件），
      // 必须让 vite 自己 fallback 到 index.html，**不能**整路代理到 core。
      // 否则 core 的 r.Static("/plugins", ...) 会把它当成文件 404 → 浏览器拿到 HTML
      // → MIME 解析失败。
      '/plugins': {
        target: BACKEND,
        bypass: (req) => {
          // 仅 /plugins/{name}/assets/... 这类静态资源放给 core；其余 SPA 路由
          // 直接返回 false 让 vite 走默认 SPA fallback 流程。
          if (req.url && /^\/plugins\/[^/]+\/assets\//.test(req.url)) {
            return null; // 走代理
          }
          return req.url; // 让 vite 处理（最终落到 index.html）
        },
      },
      // 公开状态页：完全交给后端 → airgate-health 插件 standalone 渲染。
      // core 不再维护 SPA 内的 StatusPage 组件，所以根路径 /status 也走 proxy。
      // 顺序敏感：/status 必须放在更具体的子路径之前（Vite proxy 按字典序匹配
      // 但 /status 是 /status/api 与 /status/assets 的前缀，写在哪个位置都会
      // 一并 cover——这里集中标注便于阅读）。
      '/status': BACKEND,
      '/setup/status': BACKEND,
      '/setup/test-db': BACKEND,
      '/setup/test-redis': BACKEND,
      '/setup/install': BACKEND,
      // OpenAI 兼容接口（含 WebSocket）
      '/v1': { target: BACKEND, ws: true },
      '/responses': { target: BACKEND, ws: true },
      '/chat': { target: BACKEND, ws: true },
      '/messages': { target: BACKEND, ws: true },
      '/models': { target: BACKEND, ws: true },
    },
  },
});
