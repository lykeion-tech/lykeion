import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import { fileURLToPath } from 'node:url';

const api = process.env.LYK_API_URL ?? 'http://127.0.0.1:8080';
const dashboard = process.env.LYK_DASHBOARD_URL ?? 'http://127.0.0.1:1430';

// The landing page, the dashboard (under /app/) and the API share one origin,
// as in production, so the landing page can see the session cookie. Host is
// preserved so the cookie and the same-host WebSocket check agree.
export default defineConfig({
  root: fileURLToPath(new URL('.', import.meta.url)),
  cacheDir: fileURLToPath(new URL('../../node_modules/.vite/lykeion-web', import.meta.url)),
  plugins: [vue()],
  server: {
    port: 1440,
    strictPort: true,
    proxy: {
      '/api': { target: api },
      '/auth': { target: api },
      '/ws': { target: api, ws: true },
      '/app': { target: dashboard, ws: true },
    },
  },
  build: { outDir: '../../dist/web', emptyOutDir: true },
});
