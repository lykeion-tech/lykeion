import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import { fileURLToPath } from 'node:url';

// Preserve Host so the session cookie and same-host WebSocket check agree.
const api = process.env.LYK_API_URL ?? 'http://127.0.0.1:8080';

export default defineConfig({
  define: { __LYK_PUBLIC_API_ORIGIN__: JSON.stringify(new URL(api).origin) },
  root: fileURLToPath(new URL('.', import.meta.url)),
  base: './',
  plugins: [vue()],
  resolve: {
    alias: {
      '@lykeion/api': fileURLToPath(
        new URL('./src/features/coresearch/cowrite/lib/l2-api.ts', import.meta.url),
      ),
    },
  },
  server: {
    port: 1430,
    strictPort: true,
    proxy: {
      '/api': { target: api },
      '/auth': { target: api },
      '/ws': { target: api, ws: true },
    },
  },
  build: { outDir: '../../dist/dashboard', emptyOutDir: true },
});
