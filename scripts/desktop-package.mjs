import { spawnSync } from 'node:child_process';
import {
  cpSync,
  mkdirSync,
  mkdtempSync,
  existsSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import path from 'node:path';
import os from 'node:os';
import { isIP } from 'node:net';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { updateBuildOptions } from './desktop-update-build.mjs';

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

export function desktopBuilderArgs(target, directory, signing, signingIntermediate, maintainer) {
  return [
    'node_modules/electron-builder/cli.js',
    ...target.builder,
    ...(directory ? ['--dir'] : []),
    ...(maintainer ? [`--config.linux.maintainer=${maintainer}`] : []),
    ...macBuilderSigningArgs(signing, signingIntermediate),
    '--config.publish.provider=github',
    '--config.publish.owner=lykeion-tech',
    '--config.publish.repo=lykeion',
    '--config.publish.releaseType=release',
    '--publish',
    'never',
  ];
}

export function releaseOrigin(value = 'https://app.lykeion.tech') {
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

export function debianMaintainer(platform, args, value) {
  if (platform !== 'linux' || args.includes('--dir')) return undefined;
  if (
    typeof value !== 'string' ||
    value.length > 254 ||
    [...value].some((char) => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127) ||
    !/^[^<>\r\n]*[^<>\s]\s<[^\s<>@]+@[^\s<>@]+\.[^\s<>@]+>$/.test(value)
  )
    throw new Error('Set LYK_DEB_MAINTAINER to a public name and email');
  return value;
}

export function macSigningOptions(platform, input) {
  const identity = input.LYK_MAC_SIGNING_IDENTITY;
  const profile = input.APPLE_KEYCHAIN_PROFILE;
  const keychain = input.APPLE_KEYCHAIN;
  const hasControl = (value) => [...value].some((char) => char.charCodeAt(0) < 32);
  if (!identity && !profile && !keychain) return undefined;
  if (
    platform !== 'darwin' ||
    typeof identity !== 'string' ||
    !/^Developer ID Application: [^"]+ \([A-Z0-9]{10}\)$/.test(identity) ||
    hasControl(identity) ||
    (keychain && !profile) ||
    (profile && hasControl(profile)) ||
    (keychain && hasControl(keychain))
  )
    throw new Error('Invalid macOS signing or notarization configuration');
  return {
    identity,
    ...(profile ? { keychainProfile: profile } : {}),
    ...(keychain ? { keychain } : {}),
  };
}

export function macBuilderSigningArgs(signing, intermediate) {
  if (!signing) return [];
  if (intermediate) return ['--config.mac.identity=null'];
  return [
    '--config.forceCodeSigning=true',
    '--config.mac.signIgnore=/Contents/Resources/bin/lyk$',
    `--config.mac.notarize=${Boolean(signing.keychainProfile)}`,
  ];
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
  env.GOENV = 'off';
  return env;
}

function recoverOutput(output) {
  const parent = path.dirname(output);
  mkdirSync(parent, { recursive: true });
  const backup = `${output}.previous`;
  if (existsSync(backup)) {
    if (existsSync(output)) rmSync(backup, { recursive: true, force: true });
    else renameSync(backup, output);
  }
}

export function replaceOutput(source, output) {
  recoverOutput(output);
  const parent = path.dirname(output);
  const backup = `${output}.previous`;
  const staging = mkdtempSync(path.join(parent, '.desktop-stage-'));
  let movedOld = false;
  try {
    cpSync(source, staging, { recursive: true, verbatimSymlinks: true });
    try {
      renameSync(output, backup);
      movedOld = true;
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    try {
      renameSync(staging, output);
    } catch (error) {
      if (movedOld) renameSync(backup, output);
      movedOld = false;
      throw error;
    }
    if (movedOld) rmSync(backup, { recursive: true, force: true });
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }
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

export function verifyArchive(archive, expected, hostedOrigin, asar) {
  asar.uncache(archive);
  const entries = asar.listPackage(archive);
  for (const file of [
    '/apps/desktop/out/main.js',
    '/apps/desktop/out/preload.cjs',
    '/apps/desktop/out/desktop-user-data.js',
    '/apps/desktop/out/electron-updater.cjs',
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
  for (const [name, expectedHash] of [
    ['electron-updater.cjs', expected.updaterSHA256],
  ]) {
    const actual = createHash('sha256').update(asar.extractFile(archive, `apps/desktop/out/${name}`)).digest('hex');
    if (typeof expectedHash !== 'string' || actual !== expectedHash) throw new Error('Packaged updater mismatch');
  }
  if (
    asar.extractFile(archive, 'apps/desktop/out/desktop-hosted-origin.txt').toString() !==
    hostedOrigin
  )
    throw new Error('Packaged server origin mismatch');
  for (const entry of entries.filter(
    (item) => item.startsWith('/dist/dashboard/') && item.endsWith('.js'),
  )) {
    const content = asar.extractFile(archive, entry.slice(1)).toString('utf8');
    if (
      ['http://127.0.0.1:8080', 'http://127.0.0.1:1430', 'http://localhost:1430'].some((value) =>
        content.includes(value),
      )
    )
      throw new Error('Development endpoint in packaged dashboard');
  }
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

function digest(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex');
}

export function verifyBundledBinary(built, bundled) {
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
  const input = process.argv.slice(2);
  const lockedCommit = input[0] === '--locked-build' ? input[1] : undefined;
  const [platform = process.platform, arch = process.arch, ...args] = lockedCommit
    ? input.slice(2)
    : input;
  if (args.some((arg) => arg !== '--dir')) throw new Error('Unsupported packaging option');
  const target = desktopTarget(platform, arch);
  const hostedOrigin = releaseOrigin(process.env.LYK_HOSTED_API_URL);
  const maintainer = debianMaintainer(platform, args, process.env.LYK_DEB_MAINTAINER);
  const signing = macSigningOptions(platform, process.env);
  if (signing && process.platform !== 'darwin')
    throw new Error('macOS signing requires a macOS host');
  const commit = revision();
  const version = JSON.parse(readFileSync(path.join(root, 'package.json'), 'utf8')).version;
  if (typeof version !== 'string' || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version))
    throw new Error('Desktop package requires an explicit version');
  const update = updateBuildOptions(
    process.env.LYK_DESKTOP_UPDATE_PUBLIC_KEYS,
    version,
    platform,
    arch,
    process.platform,
    process.arch,
  );
  const output = path.join(root, 'dist/desktop-release', commit, `${platform}-${arch}`);
  const env = safeBuildEnvironment(process.env);
  env.CSC_IDENTITY_AUTO_DISCOVERY = 'false';
  if (!lockedCommit) {
    mkdirSync(path.dirname(output), { recursive: true });
    run(
      'go',
      [
        'run',
        path.join(root, 'scripts/desktop-package-lock.go'),
        `${output}.lock`,
        process.execPath,
        fileURLToPath(import.meta.url),
        '--locked-build',
        commit,
        platform,
        arch,
        ...args,
      ],
      {
        env: {
          ...env,
          LYK_HOSTED_API_URL: hostedOrigin,
          ...(update ? { LYK_DESKTOP_UPDATE_PUBLIC_KEYS: update.anchors } : {}),
          ...(maintainer ? { LYK_DEB_MAINTAINER: maintainer } : {}),
          ...(signing
            ? {
                LYK_MAC_SIGNING_IDENTITY: signing.identity,
                ...(signing.keychainProfile
                  ? { APPLE_KEYCHAIN_PROFILE: signing.keychainProfile }
                  : {}),
                ...(signing.keychain ? { APPLE_KEYCHAIN: signing.keychain } : {}),
              }
            : {}),
        },
      },
    );
    return;
  }
  if (lockedCommit !== commit)
    throw new Error('Source changed while waiting for desktop packaging lock');
  const temp = mkdtempSync(path.join(os.tmpdir(), 'lykeion-desktop-release-'));
  const source = path.join(temp, 'source');
  try {
    recoverOutput(output);
    extractTrackedSnapshot(root, source);
    if (JSON.parse(readFileSync(path.join(source, 'package.json'), 'utf8')).version !== version)
      throw new Error('Desktop package version differs from source snapshot');
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
    const asar = createRequire(path.join(source, 'package.json'))('@electron/asar');
    run('npm', ['run', 'build'], { cwd: source, env: { ...env, LYK_API_URL: hostedOrigin } });
    run('node', ['scripts/desktop-dev.mjs', '--build-only'], {
      cwd: source,
      env: {
        ...env,
        LYK_HOSTED_API_URL: hostedOrigin,
        ...(update ? { LYK_DESKTOP_UPDATE_PUBLIC_KEYS: update.anchors } : {}),
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
    if (signing) {
      const identities = run('security', ['find-identity', '-v', '-p', 'codesigning'], {
        stdio: 'pipe',
        env,
      });
      if (!identities.split('\n').some((line) => line.trim().endsWith(`"${signing.identity}"`)))
        throw new Error('The requested Developer ID Application identity is not available');
      run(
        'codesign',
        [
          '--force',
          '--sign',
          signing.identity,
          '--options',
          'runtime',
          '--timestamp',
          path.join(source, 'apps/desktop/out/lyk'),
        ],
        { env },
      );
      run('codesign', ['--verify', '--strict', path.join(source, 'apps/desktop/out/lyk')], { env });
      env.CSC_NAME = signing.identity.slice('Developer ID Application: '.length);
      env.CSC_IDENTITY_AUTO_DISCOVERY = 'true';
      if (signing.keychainProfile) env.APPLE_KEYCHAIN_PROFILE = signing.keychainProfile;
      if (signing.keychain) env.APPLE_KEYCHAIN = signing.keychain;
    }
    if (revision() !== commit) throw new Error('Source changed while building desktop package');
    const manifest = {
      version,
      commit,
      platform,
      arch,
      daemonSHA256: digest(path.join(source, 'apps/desktop/out/lyk')),
      updaterSHA256: digest(path.join(source, 'apps/desktop/out/electron-updater.cjs')),
    };
    writeFileSync(
      path.join(source, 'apps/desktop/out/desktop-build.json'),
      JSON.stringify(manifest),
    );
    run(
      'node',
      desktopBuilderArgs(target, true, signing, !args.includes('--dir')),
      { cwd: source, env },
    );
    const unpacked =
      platform === 'darwin'
        ? `mac${arch === 'arm64' ? '-arm64' : ''}/Lykeion.app/Contents/Resources`
        : `linux${arch === 'arm64' ? '-arm64' : ''}-unpacked/resources`;
    const resources = path.join(source, 'dist/desktop-release', unpacked);
    verifyArchive(path.join(resources, 'app.asar'), manifest, hostedOrigin, asar);
    verifyBundledBinary(path.join(source, 'apps/desktop/out/lyk'), path.join(resources, 'bin/lyk'));
    verifyGoBuild(
      run('go', ['version', '-m', path.join(resources, 'bin/lyk')], { stdio: 'pipe', env }),
      commit,
      target.goos,
      target.goarch,
    );
    if (!args.includes('--dir')) {
      run(
        'node',
        desktopBuilderArgs(target, false, signing, false, maintainer),
        { cwd: source, env },
      );
      verifyArchive(path.join(resources, 'app.asar'), manifest, hostedOrigin, asar);
      verifyBundledBinary(
        path.join(source, 'apps/desktop/out/lyk'),
        path.join(resources, 'bin/lyk'),
      );
    }
    if (signing) {
      const app = path.dirname(path.dirname(resources));
      run('codesign', ['--verify', '--deep', '--strict', app], { env });
      if (signing.keychainProfile) run('xcrun', ['stapler', 'validate', app], { env });
    }
    replaceOutput(path.join(source, 'dist/desktop-release'), output);
  } finally {
    rmSync(temp, { recursive: true, force: true });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  if (process.argv[2] === '--replace-output' && process.argv.length === 5)
    replaceOutput(process.argv[3], process.argv[4]);
  else if (process.argv[2] === '--recover-output' && process.argv.length === 4)
    recoverOutput(process.argv[3]);
  else main();
}
