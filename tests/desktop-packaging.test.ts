import { describe, expect, it } from 'vitest';
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  realpathSync,
  rmSync,
  writeFileSync,
  existsSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { once } from 'node:events';
import path from 'node:path';
import { execFileSync, spawn } from 'node:child_process';
import { createRequire } from 'node:module';
import { createHash } from 'node:crypto';
import * as asar from '@electron/asar';
import { desktopUserData } from '../apps/desktop/src/desktop-user-data';
import {
  desktopTarget,
  releaseOrigin,
  verifyGoBuild,
  extractTrackedSnapshot,
  safeBuildEnvironment,
  verifyArchive,
  verifyBundledBinary,
  replaceOutput,
  debianMaintainer,
  macSigningOptions,
  macBuilderSigningArgs,
  desktopBuilderArgs,
  macFeedRename,
} from '../scripts/desktop-package.mjs';

describe('desktop release packaging', () => {
  it('publishes the Intel Mac update feed under its own name', () => {
    expect(macFeedRename('darwin', 'x64')).toEqual(['latest-mac.yml', 'latest-x64-mac.yml']);
    expect(macFeedRename('darwin', 'arm64')).toBeNull();
    expect(macFeedRename('linux', 'x64')).toBeNull();
  });
  it('only skips signing the discarded intermediate app in a full signed build', () => {
    const signing = {
      identity: 'Developer ID Application: Example (ABCD123456)',
      keychainProfile: 'notary',
    };
    expect(macBuilderSigningArgs(signing, true)).toEqual(['--config.mac.identity=null']);
    const { normalizeOptions } = createRequire(import.meta.url)('electron-builder/out/builder');
    expect(normalizeOptions({ config: { mac: { identity: 'null' } } }).config.mac.identity).toBeNull();
    expect(macBuilderSigningArgs(signing, false)).toEqual([
      '--config.forceCodeSigning=true',
      '--config.mac.signIgnore=/Contents/Resources/bin/lyk$',
      '--config.mac.notarize=true',
    ]);
    expect(macBuilderSigningArgs({ identity: signing.identity }, false)).toContain(
      '--config.mac.notarize=false',
    );
    expect(macBuilderSigningArgs(undefined, true)).toEqual([]);
    expect(macBuilderSigningArgs(undefined, false)).toEqual([]);
  });
  it('prepares GitHub update metadata without publishing from the packaging script', async () => {
    const config = JSON.parse(readFileSync(path.resolve('package.json'), 'utf8')).build;
    expect(config.mac.target).toContain('dmg');
    expect(config.mac.target).toContain('zip');
    expect(config.publish).toBeNull();
    const { validateConfiguration, getConfig } = createRequire(import.meta.url)(
      'app-builder-lib/out/util/config/config',
    );
    await expect(validateConfiguration(config, { isEnabled: false })).resolves.toBeUndefined();
    const { createYargs, configureBuildCommand, normalizeOptions } = createRequire(import.meta.url)(
      'electron-builder/out/builder',
    );
    const script = readFileSync(path.resolve('scripts/desktop-package.mjs'), 'utf8');
    expect(script.match(/desktopBuilderArgs\(target, (?:true|false),/g)).toHaveLength(2);
    for (const platform of ['darwin', 'linux']) {
      for (const intermediate of [true, false]) {
        const args = desktopBuilderArgs(desktopTarget(platform, 'arm64'), intermediate, undefined, undefined);
        expect(args).toContain('--config.publish.provider=github');
        expect(args).toContain('--config.publish.owner=lykeion-tech');
        expect(args).toContain('--config.publish.repo=lykeion');
        expect(args).toContain('--config.publish.releaseType=release');
        expect(args.slice(args.indexOf('--publish'), args.indexOf('--publish') + 2)).toEqual(['--publish', 'never']);
        const parsed = normalizeOptions(configureBuildCommand(createYargs()).parse(args.slice(1)));
        expect(parsed.publish).toBe('never');
        const effective = await getConfig(process.cwd(), undefined, parsed.config);
        expect(effective.publish).toEqual({ provider: 'github', owner: 'lykeion-tech', repo: 'lykeion', releaseType: 'release' });
        expect(effective.publish.publishAutoUpdate).not.toBe(false);
        await expect(validateConfiguration(effective, { isEnabled: false })).resolves.toBeUndefined();
      }
    }
  });

  it('writes builder update metadata to disk without a publisher', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'lyk-builder-metadata-'));
    try {
      const { writeUpdateInfoFiles } = createRequire(import.meta.url)(
        'app-builder-lib/out/publish/updateInfoBuilder',
      );
      const emitted: string[] = [];
      const metadata = path.join(dir, 'latest-mac.yml');
      await writeUpdateInfoFiles([{
        file: metadata,
        arch: null,
        publishConfiguration: { provider: 'github', owner: 'lykeion-tech', repo: 'lykeion', releaseType: 'release' },
        info: { version: '1.2.3', files: [{ url: 'Lykeion-1.2.3-mac.zip', sha512: 'test-digest' }], path: 'Lykeion-1.2.3-mac.zip', sha512: 'test-digest' },
        packager: {},
      }], { emitArtifactCreated: async (event: { file: string }) => { emitted.push(event.file); } });
      const written = readFileSync(metadata, 'utf8');
      expect(written).toContain('version: 1.2.3');
      expect(written).toContain('Lykeion-1.2.3-mac.zip');
      expect(emitted).toEqual([metadata]);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it('requires an explicit Developer ID identity and complete notarization configuration', () => {
    expect(macSigningOptions('darwin', {})).toBeUndefined();
    const identity = 'Developer ID Application: Example (ABCD123456)';
    expect(macSigningOptions('darwin', { LYK_MAC_SIGNING_IDENTITY: identity })).toEqual({
      identity,
    });
    expect(
      macSigningOptions('darwin', {
        LYK_MAC_SIGNING_IDENTITY: identity,
        APPLE_KEYCHAIN_PROFILE: 'notary',
      }),
    ).toEqual({ identity, keychainProfile: 'notary' });
    for (const env of [
      { LYK_MAC_SIGNING_IDENTITY: '-' },
      { LYK_MAC_SIGNING_IDENTITY: 'Developer ID Installer: Example (ABCD123456)' },
      { APPLE_KEYCHAIN_PROFILE: 'notary' },
      { LYK_MAC_SIGNING_IDENTITY: identity, APPLE_KEYCHAIN: '/some/keychain' },
    ])
      expect(() => macSigningOptions('darwin', env)).toThrow();
    expect(() => macSigningOptions('linux', { LYK_MAC_SIGNING_IDENTITY: identity })).toThrow();
  });
  it('requires a public Debian maintainer only for Linux installer builds', () => {
    expect(debianMaintainer('darwin', [], undefined)).toBeUndefined();
    expect(debianMaintainer('linux', ['--dir'], undefined)).toBeUndefined();
    expect(debianMaintainer('linux', [], 'Lykeion Team <team@example.org>')).toBe(
      'Lykeion Team <team@example.org>',
    );
    for (const value of [
      undefined,
      '',
      'secret',
      'Person <bad>',
      '  <team@example.org>',
      'A\0B <team@example.org>',
      'A\x7fB <team@example.org>',
      'Person <x@example.org>\nInjected: yes',
    ]) {
      expect(() => debianMaintainer('linux', [], value)).toThrow(
        'Set LYK_DEB_MAINTAINER to a public name and email',
      );
    }
    let failure: unknown;
    try {
      debianMaintainer('linux', [], 'private-token');
    } catch (error) {
      failure = error;
    }
    expect(failure).toBeInstanceOf(Error);
    expect(String(failure)).not.toContain('private-token');
  });
  it('keeps packaged app data per user and separate from preview', () => {
    expect(desktopUserData('/users/alice/config', true)).toBe(
      path.join('/users/alice/config', 'Lykeion'),
    );
    expect(desktopUserData('/users/bob/config', true)).toBe(
      path.join('/users/bob/config', 'Lykeion'),
    );
    expect(desktopUserData('/users/alice/config', false)).toBe(
      path.join('/users/alice/config', 'Lykeion Preview'),
    );
  });
  it('maps only approved platform and architecture pairs to Go and builder targets', () => {
    expect(desktopTarget('darwin', 'arm64')).toEqual({
      goos: 'darwin',
      goarch: 'arm64',
      builder: ['--mac', '--arm64'],
    });
    expect(desktopTarget('linux', 'x64')).toEqual({
      goos: 'linux',
      goarch: 'amd64',
      builder: ['--linux', '--x64'],
    });
    expect(() => desktopTarget('win32', 'x64')).toThrow();
    expect(() => desktopTarget('linux', 'ia32')).toThrow();
  });

  it('rejects a binary from a different revision or platform', () => {
    const info =
      'build\tGOOS=darwin\nbuild\tGOARCH=arm64\nbuild\tvcs.revision=abc123\nbuild\tvcs.modified=false';
    expect(() => verifyGoBuild(info, 'abc123', 'darwin', 'arm64')).not.toThrow();
    expect(() => verifyGoBuild(info, 'different', 'darwin', 'arm64')).toThrow();
    expect(() => verifyGoBuild(info, 'abc123', 'linux', 'arm64')).toThrow();
    expect(() =>
      verifyGoBuild(info.replace('modified=false', 'modified=true'), 'abc123', 'darwin', 'arm64'),
    ).toThrow();
  });

  it('rejects a bundled binary whose bytes differ from the verified build', () => {
    const temp = mkdtempSync(path.join(tmpdir(), 'desktop-binary-test-'));
    try {
      const built = path.join(temp, 'built');
      const bundled = path.join(temp, 'bundled');
      writeFileSync(built, 'correct');
      writeFileSync(bundled, 'correct');
      expect(() => verifyBundledBinary(built, bundled)).not.toThrow();
      writeFileSync(bundled, 'replaced');
      expect(() => verifyBundledBinary(built, bundled)).toThrow();
    } finally {
      rmSync(temp, { recursive: true, force: true });
    }
  });

  it('rejects development and malformed hosted release origins', () => {
    expect(releaseOrigin(undefined)).toBe('https://app.lykeion.tech');
    expect(releaseOrigin('https://service.example')).toBe('https://service.example');
    for (const origin of [
      'http://localhost:8080',
      'https://127.0.0.1',
      'https://0.0.0.0',
      'https://[::]',
      'https://[::ffff:127.0.0.2]',
      'https://u:p@host.test',
      'https://host.test/a',
    ])
      expect(() => releaseOrigin(origin)).toThrow();
  });

  it('builds from tracked files and excludes ignored public files and environment injection', () => {
    const temp = mkdtempSync(path.join(tmpdir(), 'desktop-snapshot-test-'));
    try {
      const repo = path.join(temp, 'repo');
      const out = path.join(temp, 'out');
      mkdirSync(path.join(repo, 'apps/dashboard/public'), { recursive: true });
      execFileSync('git', ['init', '-q', repo]);
      writeFileSync(
        path.join(repo, '.gitignore'),
        'apps/dashboard/public/private.txt\n.env.production\n',
      );
      writeFileSync(path.join(repo, 'apps/dashboard/public/ok.txt'), 'tracked');
      execFileSync('git', ['-C', repo, 'add', '.']);
      execFileSync('git', [
        '-C',
        repo,
        '-c',
        'user.name=Test',
        '-c',
        'user.email=test@example.org',
        'commit',
        '-qm',
        'fixture',
      ]);
      writeFileSync(path.join(repo, 'apps/dashboard/public/private.txt'), 'secret');
      writeFileSync(path.join(repo, '.env.production'), 'VITE_SECRET=secret');
      extractTrackedSnapshot(repo, out);
      expect(readFileSync(path.join(out, 'apps/dashboard/public/ok.txt'), 'utf8')).toBe('tracked');
      expect(existsSync(path.join(out, 'apps/dashboard/public/private.txt'))).toBe(false);
      expect(existsSync(path.join(out, '.env.production'))).toBe(false);
      const env = safeBuildEnvironment({
        PATH: '/bin',
        HOME: '/tmp',
        VITE_SECRET: 'secret',
        NODE_OPTIONS: '--require evil',
      });
      expect(env.PATH).toBe('/bin');
      expect(env.VITE_SECRET).toBeUndefined();
      expect(env.NODE_OPTIONS).toBeUndefined();
    } finally {
      rmSync(temp, { recursive: true, force: true });
    }
  });

  it('ignores persisted Go overlay configuration when building release input', () => {
    const temp = mkdtempSync(path.join(tmpdir(), 'desktop-goenv-test-'));
    try {
      const project = path.join(temp, 'project');
      mkdirSync(project);
      const source = path.join(realpathSync(project), 'main.go');
      const replacement = path.join(realpathSync(temp), 'replacement.go');
      writeFileSync(path.join(project, 'go.mod'), 'module example.com/probe\n\ngo 1.26\n');
      writeFileSync(source, 'package main\nimport "fmt"\nfunc main(){fmt.Print("clean")}\n');
      writeFileSync(replacement, 'package main\nimport "fmt"\nfunc main(){fmt.Print("overlay")}\n');
      const overlay = path.join(temp, 'overlay.json');
      writeFileSync(overlay, JSON.stringify({ Replace: { [source]: replacement } }));
      const input = {
        PATH: process.env.PATH,
        HOME: temp,
        GOCACHE: process.env.GOCACHE || path.join(temp, 'go-cache'),
      };
      execFileSync('go', ['env', '-w', `GOFLAGS=-overlay=${overlay}`], { env: input });
      expect(execFileSync('go', ['run', '.'], { cwd: project, env: input, encoding: 'utf8' })).toBe(
        'overlay',
      );
      expect(
        execFileSync('go', ['run', '.'], {
          cwd: project,
          env: safeBuildEnvironment(input),
          encoding: 'utf8',
        }),
      ).toBe('clean');
    } finally {
      // Go keeps a downloaded toolchain's module cache read-only.
      execFileSync('chmod', ['-R', 'u+w', temp]);
      rmSync(temp, { recursive: true, force: true });
    }
  }, 30000);

  it('replaces the entire target after a verified build and retains prior output on copy failure', () => {
    const temp = mkdtempSync(path.join(tmpdir(), 'desktop-output-test-'));
    try {
      const output = path.join(temp, 'target');
      const full = path.join(temp, 'full');
      const dir = path.join(temp, 'dir');
      mkdirSync(full);
      mkdirSync(dir);
      writeFileSync(path.join(full, 'installer.dmg'), 'old');
      writeFileSync(path.join(full, 'origin.txt'), 'https://one.test');
      replaceOutput(full, output);
      writeFileSync(path.join(dir, 'origin.txt'), 'https://two.test');
      replaceOutput(dir, output);
      expect(existsSync(path.join(output, 'installer.dmg'))).toBe(false);
      expect(readFileSync(path.join(output, 'origin.txt'), 'utf8')).toBe('https://two.test');
      expect(() => replaceOutput(path.join(temp, 'missing'), output)).toThrow();
      expect(readFileSync(path.join(output, 'origin.txt'), 'utf8')).toBe('https://two.test');
    } finally {
      rmSync(temp, { recursive: true, force: true });
    }
  });

  it('recovers an output interrupted between renames before attempting a new copy', () => {
    const temp = mkdtempSync(path.join(tmpdir(), 'desktop-recover-test-'));
    try {
      const output = path.join(temp, 'target');
      mkdirSync(`${output}.previous`);
      writeFileSync(path.join(`${output}.previous`, 'origin.txt'), 'previous');
      expect(() => replaceOutput(path.join(temp, 'missing'), output)).toThrow();
      expect(readFileSync(path.join(output, 'origin.txt'), 'utf8')).toBe('previous');
    } finally {
      rmSync(temp, { recursive: true, force: true });
    }
  });

  it('recovers an interrupted output on the next locked startup', () => {
    const temp = mkdtempSync(path.join(tmpdir(), 'desktop-restart-test-'));
    try {
      const output = path.join(temp, 'target');
      mkdirSync(`${output}.previous`);
      writeFileSync(path.join(`${output}.previous`, 'origin.txt'), 'previous');
      const env = {
        ...safeBuildEnvironment(process.env),
        GOCACHE: process.env.GOCACHE || path.join(temp, 'go-cache'),
      };
      execFileSync(
        'go',
        [
          'run',
          path.resolve('scripts/desktop-package-lock.go'),
          `${output}.lock`,
          process.execPath,
          path.resolve('scripts/desktop-package.mjs'),
          '--recover-output',
          output,
        ],
        { env },
      );
      expect(readFileSync(path.join(output, 'origin.txt'), 'utf8')).toBe('previous');
      expect(existsSync(`${output}.previous`)).toBe(false);
    } finally {
      rmSync(temp, { recursive: true, force: true });
    }
  }, 30000);

  it.each([false, true])(
    'serializes competing builds when the first helper is killed: %s',
    async (killHelper) => {
      const temp = mkdtempSync(path.join(tmpdir(), 'desktop-lock-test-'));
      try {
        const marker = path.join(temp, 'events');
        const lock = path.join(temp, 'lock');
        const helper = path.join(temp, 'lock-helper');
        const env = {
          ...safeBuildEnvironment(process.env),
          GOCACHE: process.env.GOCACHE || path.join(temp, 'go-cache'),
        };
        execFileSync('go', ['build', '-o', helper, 'scripts/desktop-package-lock.go'], { env });
        const script = `
        const fs = require('node:fs');
        const [marker, id] = process.argv.slice(1);
        fs.appendFileSync(marker, id + ' start\\n');
        const finish = () => fs.appendFileSync(marker, id + ' end\\n');
        if (id === 'first') {
          const poll = setInterval(() => {
            if (fs.existsSync(marker + '.release')) { clearInterval(poll); finish(); }
          }, 10);
          setTimeout(() => process.exit(1), 10000).unref();
        } else finish();
      `;
        const start = (id: string) =>
          spawn(helper, [lock, process.execPath, '-e', script, marker, id]);
        const first = start('first');
        const firstExit = once(first, 'exit');
        try {
          await expect.poll(() => existsSync(marker)).toBe(true);
          if (killHelper) {
            first.kill('SIGKILL');
            expect(await firstExit).toEqual([null, 'SIGKILL']);
          }
          const second = start('second');
          const secondExit = once(second, 'exit');
          await new Promise((resolve) => setTimeout(resolve, 300));
          writeFileSync(marker + '.release', '');
          expect(await secondExit).toEqual([0, null]);
          if (!killHelper) expect(await firstExit).toEqual([0, null]);
          expect(readFileSync(marker, 'utf8').trim().split('\n')).toEqual([
            'first start',
            'first end',
            'second start',
            'second end',
          ]);
        } finally {
          writeFileSync(marker + '.release', '');
        }
      } finally {
        rmSync(temp, { recursive: true, force: true });
      }
    },
    30000,
  );

  it('inspects real asar contents and rejects missing assets or injected files', async () => {
    const temp = mkdtempSync(path.join(tmpdir(), 'desktop-asar-test-'));
    try {
      const root = path.join(temp, 'tree');
      mkdirSync(path.join(root, 'apps/desktop/out'), { recursive: true });
      mkdirSync(path.join(root, 'dist/dashboard'), { recursive: true });
      for (const file of ['main.js', 'preload.cjs', 'desktop-user-data.js', 'electron-updater.cjs'])
        writeFileSync(path.join(root, 'apps/desktop/out', file), 'ok');
      const hash = createHash('sha256').update('ok').digest('hex');
      const expected = { version: '0.1.0', commit: 'abc', platform: 'darwin', arch: 'arm64', updaterSHA256: hash };
      writeFileSync(
        path.join(root, 'apps/desktop/out/desktop-hosted-origin.txt'),
        'https://app.lykeion.tech',
      );
      writeFileSync(
        path.join(root, 'apps/desktop/out/desktop-build.json'),
        JSON.stringify(expected),
      );
      writeFileSync(path.join(root, 'dist/dashboard/index.html'), 'ok');
      const archive = path.join(temp, 'app.asar');
      await asar.createPackage(root, archive);
      expect(() =>
        verifyArchive(
          archive,
          expected,
          'https://app.lykeion.tech',
          asar,
        ),
      ).not.toThrow();
      writeFileSync(path.join(root, 'apps/desktop/out/electron-updater.cjs'), 'tampered');
      await asar.createPackage(root, archive);
      expect(() => verifyArchive(archive, expected, 'https://app.lykeion.tech', asar)).toThrow('Packaged updater mismatch');
      writeFileSync(path.join(root, 'apps/desktop/out/electron-updater.cjs'), 'ok');
      writeFileSync(path.join(root, 'dist/dashboard/private.env'), 'secret');
      await asar.createPackage(root, archive);
      expect(() =>
        verifyArchive(
          archive,
          expected,
          'https://app.lykeion.tech',
          asar,
        ),
      ).toThrow();
      rmSync(path.join(root, 'dist/dashboard/private.env'));
      writeFileSync(path.join(root, 'dist/dashboard/development.js'), 'http://127.0.0.1:8080');
      await asar.createPackage(root, archive);
      expect(() =>
        verifyArchive(
          archive,
          expected,
          'https://app.lykeion.tech',
          asar,
        ),
      ).toThrow();
      rmSync(path.join(root, 'dist/dashboard/development.js'));
      rmSync(path.join(root, 'apps/desktop/out/preload.cjs'));
      await asar.createPackage(root, archive);
      expect(() =>
        verifyArchive(
          archive,
          expected,
          'https://app.lykeion.tech',
          asar,
        ),
      ).toThrow();
    } finally {
      rmSync(temp, { recursive: true, force: true });
    }
  });

  it('includes only bundled desktop and dashboard files, with the runtime outside asar', () => {
    const pkg = JSON.parse(readFileSync('package.json', 'utf8'));
    expect(pkg.main).toBe('apps/desktop/out/main.js');
    expect(pkg.build.files).toEqual(
      expect.arrayContaining(['apps/desktop/out/**', 'dist/dashboard/**']),
    );
    expect(pkg.build.extraResources).toEqual(
      expect.arrayContaining([{ from: 'apps/desktop/out/lyk', to: 'bin/lyk' }]),
    );
    expect(pkg.build.files).not.toContain('node_modules/**');
    expect(pkg.build.asar).toBe(true);
    expect(pkg.build.mac.icon).toBe('apps/desktop/resources/icon.icns');
    expect(pkg.build.linux.icon).toBe('apps/desktop/resources/icon.png');
  });
});
