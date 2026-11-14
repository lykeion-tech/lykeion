import { spawn, spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { buildDesktopUpdater } from './desktop-updater-bundle.mjs';
import { mkdirSync, writeFileSync } from 'node:fs';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const output = path.join(root, 'apps/desktop/out');
mkdirSync(output, { recursive: true });
await buildDesktopUpdater(root, path.join(output, 'electron-updater.cjs'));
function build(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, stdio: 'inherit', ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}
build('go', ['build', '-p', '1', '-o', path.join(output, 'lyk'), './cmd/lyk'], {
  cwd: path.join(root, 'server'),
  env: { ...process.env, GOFLAGS: '', GOENV: 'off', GOWORK: 'off', GOEXPERIMENT: '' },
});
build(process.execPath, [
  path.join(root, 'node_modules/typescript/bin/tsc'),
  '-p',
  'apps/desktop/tsconfig.json',
]);
writeFileSync(
  path.join(output, 'desktop-hosted-origin.txt'),
  process.env.LYK_HOSTED_API_URL ?? 'https://app.lykeion.tech',
);
if (!process.argv.includes('--build-only')) {
  const child = spawn(
    process.execPath,
    [path.join(root, 'node_modules/electron/cli.js'), path.join(output, 'main.js')],
    {
      cwd: root,
      stdio: 'inherit',
      env: {
        ...process.env,
        LYKEION_RENDERER_URL: process.env.LYKEION_RENDERER_URL ?? 'http://127.0.0.1:1430',
      },
    },
  );
  child.on('error', () => {
    console.error('Unable to launch Electron preview');
    process.exitCode = 1;
  });
  child.on('exit', (code) => {
    process.exitCode = code ?? 1;
  });
}
