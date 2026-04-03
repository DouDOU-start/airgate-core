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
      '/plugins': BACKEND,
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
