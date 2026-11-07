import { spawnSync } from 'node:child_process';
import { readFileSync, rmSync, writeFileSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import path from 'node:path';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

export function desktopTarget(platform, arch) {
  if (!['darwin', 'linux'].includes(platform) || !['x64', 'arm64'].includes(arch))
    throw new Error('Unsupported desktop target');
  return {
    goos: platform,
    goarch: arch === 'x64' ? 'amd64' : 'arm64',
    builder: [platform === 'darwin' ? '--mac' : '--linux', `--${arch}`],
  };
}

export function releaseOrigin(value = 'https://lykeion.ia') {
  let url;
  try {
    url = new URL(value);
  } catch {
    throw new Error('Invalid hosted release origin');
  }
  if (
    url.protocol !== 'https:' ||
    url.username ||
    url.password ||
    url.pathname !== '/' ||
    url.search ||
    url.hash ||
    url.hostname === 'localhost' ||
    url.hostname.endsWith('.localhost') ||
    url.hostname.startsWith('127.') ||
    url.hostname === '[::1]' ||
    url.hostname === '[::ffff:7f00:1]'
  )
    throw new Error('Invalid hosted release origin');
  return url.origin;
}

export function verifyGoBuild(info, commit, goos, goarch) {
  const lines = new Set(info.split(/\r?\n/).map((line) => line.trim()));
  for (const expected of [
    `build\tGOOS=${goos}`,
    `build\tGOARCH=${goarch}`,
    `build\tvcs.revision=${commit}`,
    'build\tvcs.modified=false',
  ]) {
    if (!lines.has(expected))
      throw new Error('Built daemon does not match release source and target');
  }
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, stdio: 'inherit', ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} exited with status ${result.status}`);
  return result.stdout?.toString().trim();
}

function revision() {
  if (run('git', ['status', '--porcelain', '--untracked-files=all'], { stdio: 'pipe' }))
    throw new Error('Desktop packages require a clean source checkout');
  return run('git', ['rev-parse', 'HEAD'], { stdio: 'pipe' });
}

function main() {
  const [platform = process.platform, arch = process.arch, ...args] = process.argv.slice(2);
  if (args.some((arg) => arg !== '--dir')) throw new Error('Unsupported packaging option');
  const target = desktopTarget(platform, arch);
  const hostedOrigin = releaseOrigin(process.env.LYK_HOSTED_API_URL);
  const commit = revision();
  const gitDir = run('git', ['rev-parse', '--absolute-git-dir'], { stdio: 'pipe' });
  const version = JSON.parse(readFileSync(path.join(root, 'package.json'), 'utf8')).version;
  if (typeof version !== 'string' || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version))
    throw new Error('Desktop package requires an explicit version');
  rmSync(path.join(root, 'apps/desktop/out'), { recursive: true, force: true });
  run('npm', ['run', 'build'], { env: { ...process.env, LYK_API_URL: hostedOrigin } });
  run('node', ['scripts/desktop-dev.mjs', '--build-only'], {
    env: {
      ...process.env,
      LYK_HOSTED_API_URL: hostedOrigin,
      GOOS: target.goos,
      GOARCH: target.goarch,
      CGO_ENABLED: '0',
      GIT_DIR: gitDir,
      GIT_WORK_TREE: root,
    },
  });
  verifyGoBuild(
    run('go', ['version', '-m', path.join(root, 'apps/desktop/out/lyk')], { stdio: 'pipe' }),
    commit,
    target.goos,
    target.goarch,
  );
  if (revision() !== commit) throw new Error('Source changed while building desktop package');
  writeFileSync(
    path.join(root, 'apps/desktop/out/desktop-build.json'),
    JSON.stringify({ version, commit, platform, arch }),
  );
  run('node', [
    'node_modules/electron-builder/cli.js',
    ...target.builder,
    ...args,
    '--publish',
    'never',
  ]);
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href)
  main();
