import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { desktopTarget, releaseOrigin, verifyGoBuild } from '../scripts/desktop-package.mjs';

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

  it('rejects development and malformed hosted release origins', () => {
    expect(releaseOrigin(undefined)).toBe('https://lykeion.ia');
    expect(releaseOrigin('https://service.example')).toBe('https://service.example');
    for (const origin of [
      'http://localhost:8080',
      'https://127.0.0.1',
      'https://u:p@host.test',
      'https://host.test/a',
    ])
      expect(() => releaseOrigin(origin)).toThrow();
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
