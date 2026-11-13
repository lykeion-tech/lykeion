import type { NextConfig } from 'next';

const config: NextConfig = {
  output: process.env.NODE_ENV === 'production' ? 'export' : undefined,
  distDir: process.env.NODE_ENV === 'production' ? 'out' : '.next',
  devIndicators: false,
  agentRules: false,
  skipTrailingSlashRedirect: true,
  trailingSlash: true,
  turbopack: { root: process.cwd() },
  images: { unoptimized: true },
  ...(process.env.NODE_ENV === 'development' ? {
    async rewrites() {
      const api = process.env.LYK_API_URL ?? 'http://127.0.0.1:8080';
      const dashboard = process.env.LYK_DASHBOARD_URL ?? 'http://127.0.0.1:1430';
      return [
        { source: '/api/:path*', destination: `${api}/api/:path*` },
        { source: '/auth/:path*', destination: `${api}/auth/:path*` },
        { source: '/app/', destination: `${dashboard}/app/` },
        { source: '/app/:path*', destination: `${dashboard}/app/:path*` },
      ];
    },
  } : {}),
};
export default config;
