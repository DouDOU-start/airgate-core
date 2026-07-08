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
  build: {
    rollupOptions: {
      output: {
        manualChunks: {
          vendor: ['react', 'react-dom', '@tanstack/react-router', '@tanstack/react-query', 'i18next', 'react-i18next'],
          ui: ['@heroui/react', '@heroui/styles', 'lucide-react', 'motion'],
          charts: ['recharts'],
          markdown: ['react-markdown', 'remark-gfm'],
        },
      },
    },
  },
  server: {
    host: '0.0.0.0',
    port: 3000,
    // 仓库在 WSL2 的 /mnt 盘（9p 文件系统）上时按需转换极慢：
    // 服务一启动就预热首屏链路的转换缓存，避免浏览器首个请求才触发级联转换。
    warmup: {
      clientFiles: [
        './src/main.tsx',
        './src/index.css',
        './src/app/providers/AuthProvider.tsx',
        './src/app/router.tsx',
        './src/app/routePreloads.ts',
        './src/app/layout/AppShell.tsx',
        './src/pages/LoginPage.tsx',
        './src/pages/DashboardPage.tsx',
      ],
    },
    watch: {
      // /mnt 盘 9p 文件系统不支持 inotify，只能轮询（仓库挪到 WSL 原生 ext4 后可移除）
      usePolling: true,
      interval: 1000,
    },
    proxy: {
      '/api': BACKEND,
      '/uploads': BACKEND,
      '/assets-runtime': BACKEND,
      '/setup/status': BACKEND,
      '/setup/test-db': BACKEND,
      '/setup/test-redis': BACKEND,
      '/setup/install': BACKEND,
      // OpenAI 兼容接口（含 WebSocket）
      '/v1': { target: BACKEND, ws: true },
      '/responses': { target: BACKEND, ws: true },
      // /chat 既是 SPA 全屏对话页（GET /chat），也是 OpenAI 兼容裸路径（POST
      // /chat/completions）的兜底代理。bypass：纯 /chat 与 /chat/ 让 vite 走
      // SPA fallback；其余 /chat/<sub> 才转给后端，避免刷新页面时被代理到 core
      // 拿不到 SPA 而白屏。
      '/chat': {
        target: BACKEND,
        ws: true,
        bypass: (req) => {
          if (req.url === '/chat' || req.url === '/chat/') {
            return req.url;
          }
          return null;
        },
      },
      '/messages': { target: BACKEND, ws: true },
      '/models': { target: BACKEND, ws: true },
    },
  },
});
