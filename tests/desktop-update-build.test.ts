import { createHash, generateKeyPairSync } from 'node:crypto';
import { describe, expect, it } from 'vitest';
import {
  desktopGoEnvironment,
  updateBuildFlags,
  updateBuildOptions,
  verifyUpdateBuildInfo,
} from '../scripts/desktop-update-build.mjs';

describe('desktop update release trust', () => {
  it('disables caller-controlled Go flags and config with or without anchors', () => {
    const env = desktopGoEnvironment({
      GOFLAGS: '-overlay=/tmp/evil',
      GOENV: '/tmp/config',
      GOWORK: '/tmp/work',
      GOEXPERIMENT: 'unsafe',
      GOTOOLCHAIN: 'auto',
      GOOS: 'linux',
    }, true);
    expect(env).toMatchObject({
      GOFLAGS: '',
      GOENV: 'off',
      GOWORK: 'off',
      GOEXPERIMENT: '',
      GOTOOLCHAIN: 'local',
      GOOS: 'linux',
      GOMAXPROCS: '2',
    });
  });
  it('preserves development toolchain selection without release anchors', () => {
    expect(desktopGoEnvironment({ GOTOOLCHAIN: 'auto' }).GOTOOLCHAIN).toBe('auto');
    expect(desktopGoEnvironment({}).GOTOOLCHAIN).toBeUndefined();
  });
  it('is disabled when no release anchors are supplied', () => {
    expect(
      updateBuildOptions(undefined, '0.1.0', 'darwin', 'arm64', 'darwin', 'arm64'),
    ).toBeUndefined();
  });

  it('validates canonical public anchors, native target and stable version', () => {
    const { publicKey } = generateKeyPairSync('ed25519');
    const der = publicKey.export({ type: 'spki', format: 'der' });
    const encoded = der.toString('base64');
    const options = updateBuildOptions(encoded, '1.2.3', 'darwin', 'arm64', 'darwin', 'arm64');
    if (!options) throw new Error('expected configured update trust');
    expect(options.keyIDs).toEqual([createHash('sha256').update(der).digest('hex')]);
    expect(updateBuildFlags(options)).toEqual([
      '-ldflags',
      `-X=lykeion/server/internal/desktopupdate.embeddedVersion=1.2.3 -X=lykeion/server/internal/desktopupdate.embeddedKeys=${encoded}`,
    ]);
    expect(() =>
      verifyUpdateBuildInfo(JSON.stringify({ version: '1.2.3', keyIDs: options.keyIDs }), options),
    ).not.toThrow();
    for (const [keys, version, platform, arch] of [
      ['bad', '1.2.3', 'darwin', 'arm64'],
      [encoded, '1.2.3-beta', 'darwin', 'arm64'],
      [encoded, '1.2.3 -X=bad', 'darwin', 'arm64'],
      [encoded, '1.2.3', 'linux', 'arm64'],
      [encoded + ',' + encoded, '1.2.3', 'darwin', 'arm64'],
    ])
      expect(() => updateBuildOptions(keys, version, platform, arch, 'darwin', 'arm64')).toThrow();
    expect(() =>
      verifyUpdateBuildInfo(JSON.stringify({ version: '1.2.4', keyIDs: options.keyIDs }), options),
    ).toThrow();
    expect(() =>
      verifyUpdateBuildInfo(JSON.stringify({ version: '1.2.3', keyIDs: [] }), options),
    ).toThrow();
  });
});
