import { spawn, spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { buildDesktopUpdater } from './desktop-updater-bundle.mjs';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import {
  desktopGoEnvironment,
  updateBuildFlags,
  updateBuildOptions,
  verifyUpdateBuildInfo,
} from './desktop-update-build.mjs';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const output = path.join(root, 'apps/desktop/out');
mkdirSync(output, { recursive: true });
await buildDesktopUpdater(root, path.join(output, 'electron-updater.cjs'));
function build(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, stdio: 'inherit', ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}
const version = JSON.parse(readFileSync(path.join(root, 'package.json'), 'utf8')).version;
const goos = process.env.GOOS ?? process.platform;
const goarch = process.env.GOARCH ?? (process.arch === 'x64' ? 'amd64' : process.arch);
const arch = goarch === 'amd64' ? 'x64' : goarch;
const update = updateBuildOptions(
  process.env.LYK_DESKTOP_UPDATE_PUBLIC_KEYS,
  version,
  goos,
  arch,
  process.platform,
  process.arch,
);
build(
  'go',
  ['build', '-p', '1', ...updateBuildFlags(update), '-o', path.join(output, 'lyk'), './cmd/lyk'],
  {
    cwd: path.join(root, 'server'),
    env: desktopGoEnvironment(process.env, !!update),
  },
);
if (update) {
  const result = spawnSync(path.join(output, 'lyk'), ['desktop-update-build-info'], {
    cwd: root,
    env: { HOME: process.env.HOME ?? '', PATH: process.env.PATH ?? '' },
    encoding: 'utf8',
    timeout: 10000,
    maxBuffer: 4096,
  });
  if (result.error || result.status !== 0)
    throw new Error('Built desktop update trust could not be verified');
  verifyUpdateBuildInfo(result.stdout, update);
}
build(process.execPath, [
  path.join(root, 'node_modules/typescript/bin/tsc'),
  '-p',
  'apps/desktop/tsconfig.json',
]);
writeFileSync(
  path.join(output, 'desktop-hosted-origin.txt'),
  process.env.LYK_HOSTED_API_URL ?? 'https://lykeion.ia',
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
