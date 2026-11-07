import { spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import path from 'node:path';
import os from 'node:os';
import { isIP } from 'node:net';
import { createHash } from 'node:crypto';
import * as asar from '@electron/asar';

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
    url.hostname.replace(/\.$/, '') === 'localhost' ||
    url.hostname.replace(/\.$/, '').endsWith('.localhost') ||
    isIP(url.hostname.replace(/^\[|\]$/g, '')) !== 0
  )
    throw new Error('Invalid hosted release origin');
  return url.origin;
}

export function safeBuildEnvironment(input) {
  const env = {};
  for (const key of [
    'PATH',
    'HOME',
    'TMPDIR',
    'USER',
    'LOGNAME',
    'GOCACHE',
    'GOPATH',
    'SYSTEMROOT',
    'WINDIR',
  ]) {
    if (input[key]) env[key] = input[key];
  }
  return env;
}

export function extractTrackedSnapshot(repo, destination) {
  mkdirSync(destination, { recursive: true });
  const tarball = path.join(path.dirname(destination), `${path.basename(destination)}.tar`);
  try {
    run('git', ['-C', repo, 'archive', '--format=tar', `--output=${tarball}`, 'HEAD']);
    run('tar', ['-xf', tarball, '-C', destination]);
  } finally {
    rmSync(tarball, { force: true });
  }
}

export function verifyArchive(archive, expected, hostedOrigin) {
  const entries = asar.listPackage(archive);
  for (const file of [
    '/apps/desktop/out/main.js',
    '/apps/desktop/out/preload.cjs',
    '/apps/desktop/out/desktop-build.json',
    '/apps/desktop/out/desktop-hosted-origin.txt',
    '/dist/dashboard/index.html',
  ]) {
    if (!entries.includes(file)) throw new Error(`Missing packaged asset: ${file}`);
  }
  if (
    entries.some(
      (entry) =>
        /(^|\/)(?:node_modules|\.env(?:\.|$)|[^/]+\.env$)/i.test(entry) ||
        entry === '/apps/desktop/out/lyk',
    )
  )
    throw new Error('Unexpected packaged file');
  const manifest = JSON.parse(
    asar.extractFile(archive, 'apps/desktop/out/desktop-build.json').toString(),
  );
  if (JSON.stringify(manifest) !== JSON.stringify(expected))
    throw new Error('Package metadata mismatch');
  if (
    asar.extractFile(archive, 'apps/desktop/out/desktop-hosted-origin.txt').toString() !==
    hostedOrigin
  )
    throw new Error('Packaged server origin mismatch');
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

export function verifyBundledBinary(built, bundled) {
  const digest = (file) => createHash('sha256').update(readFileSync(file)).digest('hex');
  if (digest(built) !== digest(bundled))
    throw new Error('Bundled daemon differs from verified build');
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
  const version = JSON.parse(readFileSync(path.join(root, 'package.json'), 'utf8')).version;
  if (typeof version !== 'string' || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version))
    throw new Error('Desktop package requires an explicit version');
  const temp = mkdtempSync(path.join(os.tmpdir(), 'lykeion-desktop-release-'));
  const source = path.join(temp, 'source');
  const env = safeBuildEnvironment(process.env);
  env.CSC_IDENTITY_AUTO_DISCOVERY = 'false';
  try {
    extractTrackedSnapshot(root, source);
    // Go discovers this VCS marker; Git is directed at the selected revision and isolated source tree.
    mkdirSync(path.join(source, '.git'));
    const gitDir = run('git', ['rev-parse', '--absolute-git-dir'], { stdio: 'pipe' });
    const userConfig = path.join(temp, 'npm-user-config');
    const globalConfig = path.join(temp, 'npm-global-config');
    writeFileSync(userConfig, '');
    writeFileSync(globalConfig, '');
    run(
      'npm',
      [
        'ci',
        '--ignore-scripts',
        '--no-audit',
        '--no-fund',
        `--userconfig=${userConfig}`,
        `--globalconfig=${globalConfig}`,
      ],
      { cwd: source, env },
    );
    run('npm', ['run', 'build'], { cwd: source, env: { ...env, LYK_API_URL: hostedOrigin } });
    run('node', ['scripts/desktop-dev.mjs', '--build-only'], {
      cwd: source,
      env: {
        ...env,
        LYK_HOSTED_API_URL: hostedOrigin,
        GOOS: target.goos,
        GOARCH: target.goarch,
        CGO_ENABLED: '0',
        GIT_DIR: gitDir,
        GIT_WORK_TREE: source,
      },
    });
    verifyGoBuild(
      run('go', ['version', '-m', path.join(source, 'apps/desktop/out/lyk')], {
        stdio: 'pipe',
        env,
      }),
      commit,
      target.goos,
      target.goarch,
    );
    if (revision() !== commit) throw new Error('Source changed while building desktop package');
    const manifest = { version, commit, platform, arch };
    writeFileSync(
      path.join(source, 'apps/desktop/out/desktop-build.json'),
      JSON.stringify(manifest),
    );
    run(
      'node',
      ['node_modules/electron-builder/cli.js', ...target.builder, '--dir', '--publish', 'never'],
      { cwd: source, env },
    );
    const unpacked =
      platform === 'darwin'
        ? `mac${arch === 'arm64' ? '-arm64' : ''}/Lykeion.app/Contents/Resources`
        : `linux${arch === 'arm64' ? '-arm64' : ''}-unpacked/resources`;
    const resources = path.join(source, 'dist/desktop-release', unpacked);
    verifyArchive(path.join(resources, 'app.asar'), manifest, hostedOrigin);
    verifyBundledBinary(path.join(source, 'apps/desktop/out/lyk'), path.join(resources, 'bin/lyk'));
    verifyGoBuild(
      run('go', ['version', '-m', path.join(resources, 'bin/lyk')], { stdio: 'pipe', env }),
      commit,
      target.goos,
      target.goarch,
    );
    if (!args.includes('--dir'))
      run(
        'node',
        ['node_modules/electron-builder/cli.js', ...target.builder, '--publish', 'never'],
        { cwd: source, env },
      );
    mkdirSync(path.join(root, 'dist/desktop-release'), { recursive: true });
    cpSync(path.join(source, 'dist/desktop-release'), path.join(root, 'dist/desktop-release'), {
      recursive: true,
    });
  } finally {
    rmSync(temp, { recursive: true, force: true });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href)
  main();
