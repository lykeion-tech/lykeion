import { mkdtemp, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { desktopConfigGuidance, loadDesktopConfig } from '../apps/desktop/src/runtime-config';

const dirs: string[] = [];
async function configPath() {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'lyk-desktop-config-'));
  dirs.push(dir);
  return path.join(dir, 'desktop.json');
}
afterEach(async () => Promise.all(dirs.splice(0).map((dir) => rm(dir, { recursive: true }))));

describe('desktop deployment selection', () => {
  it('uses the supplied hosted origin when no configuration file exists', async () => {
    expect(await loadDesktopConfig(await configPath(), 'https://Hosted.Example:443')).toBe(
      'https://hosted.example',
    );
  });
  it('loads the release value only when the self-hosted file is absent', async () => {
    expect(await loadDesktopConfig(await configPath(), async () => 'https://hosted.example')).toBe(
      'https://hosted.example',
    );
  });
  it('fails closed when neither a file nor a hosted origin is supplied', async () => {
    await expect(loadDesktopConfig(await configPath(), undefined)).rejects.toThrow();
  });
  it.each([
    'http://remote.example',
    'https://user:secret@hosted.example',
    'https://hosted.example/app',
  ])('rejects an invalid hosted release origin', async (hosted) => {
    const error = await loadDesktopConfig(await configPath(), hosted).catch((err) => err);
    expect(error).toBeInstanceOf(Error);
    expect(error.message).not.toContain('secret');
  });
  it('uses a valid self-hosted file independently of the hosted release value', async () => {
    const file = await configPath();
    await writeFile(file, JSON.stringify({ schemaVersion: 1, apiUrl: 'https://lab.example:8443' }));
    expect(await loadDesktopConfig(file, undefined)).toBe('https://lab.example:8443');
  });
  it('does not read the release value when a self-hosted file exists', async () => {
    const file = await configPath();
    await writeFile(file, JSON.stringify({ schemaVersion: 1, apiUrl: 'https://lab.example' }));
    expect(
      await loadDesktopConfig(file, async () => {
        throw new Error('release value inaccessible');
      }),
    ).toBe('https://lab.example');
  });
  it.each(['http://localhost:8080', 'http://127.0.0.1:8080', 'http://[::1]:8080'])(
    'accepts an explicitly configured loopback deployment',
    async (apiUrl) => {
      const file = await configPath();
      await writeFile(file, JSON.stringify({ schemaVersion: 1, apiUrl }));
      expect(await loadDesktopConfig(file, undefined)).toBe(apiUrl);
    },
  );
  it.each([
    'not json',
    JSON.stringify({ schemaVersion: 2, apiUrl: 'https://lab.example' }),
    JSON.stringify({ schemaVersion: 1, apiUrl: 'https://user:secret@lab.example' }),
    JSON.stringify({ schemaVersion: 1, apiUrl: 'https://lab.example/path' }),
    JSON.stringify({ schemaVersion: 1, apiUrl: 'https://lab.example/a/..' }),
    JSON.stringify({ schemaVersion: 1, apiUrl: 'https://lab.example/?' }),
    JSON.stringify({ schemaVersion: 1, apiUrl: 'https://lab.example/#' }),
    JSON.stringify({ schemaVersion: 1, apiUrl: 'https://lab.example/#fragment' }),
    JSON.stringify({ schemaVersion: 1, apiUrl: 'https://lab.example/?token=secret' }),
    JSON.stringify({ schemaVersion: 1, apiUrl: 'file:///tmp/test' }),
    JSON.stringify({ schemaVersion: 1, apiUrl: 'http://localhost.example' }),
    JSON.stringify({ schemaVersion: 1, apiUrl: 'http://127.0.0.2' }),
    JSON.stringify({ schemaVersion: 1, apiUrl: 'http://127.1' }),
    JSON.stringify({ schemaVersion: 1, apiUrl: 'http://[::2]' }),
  ])('rejects an invalid file without falling back or exposing its contents', async (value) => {
    const file = await configPath();
    await writeFile(file, value);
    const error = await loadDesktopConfig(file, 'https://hosted.example').catch((err) => err);
    expect(error).toBeInstanceOf(Error);
    expect(error.message).not.toContain('secret');
  });
  it('rejects an unreadable config path instead of falling back to hosted', async () => {
    const file = await configPath();
    await expect(loadDesktopConfig(path.dirname(file), 'https://hosted.example')).rejects.toThrow();
  });
  it('rejects a dangling self-hosted config symlink rather than falling back', async () => {
    const file = await configPath();
    await symlink('missing-target', file);
    await expect(loadDesktopConfig(file, 'https://hosted.example')).rejects.toThrow();
  });
  it('rejects a dangling config directory symlink rather than falling back', async () => {
    const file = await configPath();
    const dir = path.join(path.dirname(file), 'linked-dir');
    await symlink('missing-target', dir);
    await expect(
      loadDesktopConfig(path.join(dir, 'desktop.json'), 'https://hosted.example'),
    ).rejects.toThrow();
  });
  it('gives safe, distinct correction guidance for invalid, unreadable and missing release configuration', async () => {
    const file = await configPath();
    await writeFile(file, '{secret');
    const invalid = desktopConfigGuidance(
      await loadDesktopConfig(file, undefined).catch((err) => err),
    );
    const unreadable = desktopConfigGuidance(
      await loadDesktopConfig(path.dirname(file), undefined).catch((err) => err),
    );
    await rm(file);
    const missing = desktopConfigGuidance(
      await loadDesktopConfig(file, undefined).catch((err) => err),
    );
    expect(new Set([invalid, unreadable, missing]).size).toBe(3);
    expect(invalid).toContain('~/.lyk/desktop.json');
    expect(invalid).toContain('%USERPROFILE%\\.lyk\\desktop.json');
    expect(invalid).toContain('"schemaVersion": 1');
    expect(invalid).toContain('"apiUrl": "https://your-server.example"');
    expect(invalid).toContain('HTTPS');
    expect(invalid).toContain('localhost');
    expect(invalid).toMatch(/restart/i);
    expect(unreadable).toContain('~/.lyk/desktop.json');
    expect(unreadable).toContain('%USERPROFILE%\\.lyk\\desktop.json');
    expect(missing).toContain('%USERPROFILE%\\.lyk\\desktop.json');
    expect(missing).toContain('"schemaVersion": 1');
    expect(missing).toContain('"apiUrl": "https://your-server.example"');
    expect(missing).toContain('HTTPS');
    expect(missing).toMatch(/restart/i);
    expect(missing).toMatch(/release|reinstall/i);
    expect([invalid, unreadable, missing].join(' ')).not.toContain('secret');
  });
});
