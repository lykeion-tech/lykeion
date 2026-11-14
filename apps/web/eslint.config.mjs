import { defineConfig, globalIgnores } from 'eslint/config';
import nextVitals from 'eslint-config-next/core-web-vitals';
import nextTs from 'eslint-config-next/typescript';

export default defineConfig([
  ...nextVitals,
  ...nextTs,
  // The static export ships images unoptimized, so next/image would add nothing over <img>.
  { rules: { '@next/next/no-img-element': 'off' } },
  globalIgnores(['.next/**', '.next-production/**', 'out/**', 'next-env.d.ts']),
]);
