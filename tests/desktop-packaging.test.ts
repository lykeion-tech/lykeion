import { describe, expect, it } from 'vitest';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import * as asar from '@electron/asar';
import {
  desktopTarget,
  releaseOrigin,
  verifyGoBuild,
  extractTrackedSnapshot,
  safeBuildEnvironment,
  verifyArchive,
  verifyBundledBinary,
} from '../scripts/desktop-package.mjs';

describe('desktop release packaging', () => {
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
    expect(releaseOrigin(undefined)).toBe('https://lykeion.ia');
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

  it('inspects real asar contents and rejects missing assets or injected files', async () => {
    const temp = mkdtempSync(path.join(tmpdir(), 'desktop-asar-test-'));
    try {
      const root = path.join(temp, 'tree');
      mkdirSync(path.join(root, 'apps/desktop/out'), { recursive: true });
      mkdirSync(path.join(root, 'dist/dashboard'), { recursive: true });
      for (const file of ['main.js', 'preload.cjs'])
        writeFileSync(path.join(root, 'apps/desktop/out', file), 'ok');
      writeFileSync(
        path.join(root, 'apps/desktop/out/desktop-hosted-origin.txt'),
        'https://lykeion.ia',
      );
      writeFileSync(
        path.join(root, 'apps/desktop/out/desktop-build.json'),
        JSON.stringify({ version: '0.1.0', commit: 'abc', platform: 'darwin', arch: 'arm64' }),
      );
      writeFileSync(path.join(root, 'dist/dashboard/index.html'), 'ok');
      const archive = path.join(temp, 'app.asar');
      await asar.createPackage(root, archive);
      expect(() =>
        verifyArchive(
          archive,
          { version: '0.1.0', commit: 'abc', platform: 'darwin', arch: 'arm64' },
          'https://lykeion.ia',
        ),
      ).not.toThrow();
      writeFileSync(path.join(root, 'dist/dashboard/private.env'), 'secret');
      await asar.createPackage(root, archive);
      expect(() =>
        verifyArchive(
          archive,
          { version: '0.1.0', commit: 'abc', platform: 'darwin', arch: 'arm64' },
          'https://lykeion.ia',
        ),
      ).toThrow();
      rmSync(path.join(root, 'dist/dashboard/private.env'));
      writeFileSync(path.join(root, 'dist/dashboard/development.js'), 'http://127.0.0.1:8080');
      await asar.createPackage(root, archive);
      expect(() =>
        verifyArchive(
          archive,
          { version: '0.1.0', commit: 'abc', platform: 'darwin', arch: 'arm64' },
          'https://lykeion.ia',
        ),
      ).toThrow();
      rmSync(path.join(root, 'dist/dashboard/development.js'));
      rmSync(path.join(root, 'apps/desktop/out/preload.cjs'));
      await asar.createPackage(root, archive);
      expect(() =>
        verifyArchive(
          archive,
          { version: '0.1.0', commit: 'abc', platform: 'darwin', arch: 'arm64' },
          'https://lykeion.ia',
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
