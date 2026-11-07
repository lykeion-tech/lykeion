// @vitest-environment node
import { describe, expect, it, vi } from 'vitest';
import {
  desktopEntryURL,
  desktopAssetPath,
  trustedDesktopFrame,
  installDesktopRenderer,
  desktopPartition,
  validExternalURL,
  createRequestCancellation,
} from '../apps/desktop/src/renderer-origin';

describe('desktop renderer origin', () => {
  it('cancels only the matching active request and fences early or duplicate IDs', () => {
    const first = '11111111-1111-4111-8111-111111111111';
    const second = '22222222-2222-4222-8222-222222222222';
    const third = '33333333-3333-4333-8333-333333333333';
    const registry = createRequestCancellation();
    const abortFirst = vi.fn();
    const abortSecond = vi.fn();
    expect(registry.register(first, abortFirst)).toBe(true);
    expect(registry.register(second, abortSecond)).toBe(true);
    expect(registry.register(first, vi.fn())).toBe(false);
    registry.cancel(first);
    expect(abortFirst).toHaveBeenCalledOnce();
    expect(abortSecond).not.toHaveBeenCalled();
    registry.cancel(third);
    expect(registry.register(third, vi.fn())).toBe(false);
    expect(registry.register('bad-id', vi.fn())).toBe(false);
  });
  it('separates complete origins, including ports and related domains', () => {
    expect(desktopPartition('http://127.0.0.1:8080')).not.toBe(
      desktopPartition('http://127.0.0.1:9090'),
    );
    expect(desktopPartition('https://lab.example')).not.toBe(
      desktopPartition('https://dev.lab.example'),
    );
    expect(desktopPartition('https://lab.example')).toBe(desktopPartition('https://lab.example'));
  });
  it('allows only ordinary credential-free external web addresses', () => {
    expect(validExternalURL('https://example.org/paper')).toBe(true);
    expect(validExternalURL('mailto:researcher@example.org')).toBe(true);
    expect(validExternalURL('mailto:researcher@example.org?subject=Study')).toBe(true);
    expect(validExternalURL('mailto:researcher@example.org%0abcc:other@example.org')).toBe(false);
    for (const value of [
      'file:///etc/passwd',
      'javascript:alert(1)',
      'https://user:pass@example.org/',
    ])
      expect(validExternalURL(value)).toBe(false);
  });
  it.each(['https://lykeion.ia', 'https://lab.example:8443', 'http://127.0.0.1:8080'])(
    'serves the dashboard on the chosen origin %s',
    (origin) => {
      const entry = desktopEntryURL(origin);
      expect(new URL(entry).origin).toBe(origin);
      expect(desktopAssetPath(entry, origin, '/bundle')).toBe('/bundle/index.html');
      expect(desktopAssetPath(`${entry}assets/app.js`, origin, '/bundle')).toBe(
        '/bundle/assets/app.js',
      );
      expect(desktopAssetPath(`${origin}/api/me`, origin, '/bundle')).toBeNull();
      expect(
        desktopAssetPath('https://other.example/__lykeion_desktop__/', origin, '/bundle'),
      ).toBeNull();
    },
  );
  it('blocks escape and remote HTML within its reserved asset prefix', () => {
    const origin = 'https://lab.example';
    expect(
      desktopAssetPath(`${origin}/__lykeion_desktop__/%2e%2e%2fsecret`, origin, '/bundle'),
    ).toBe(false);
    expect(
      desktopAssetPath(`${origin}/__lykeion_desktop__/assets/evil.html`, origin, '/bundle'),
    ).toBe(false);
    expect(desktopAssetPath(`${origin}/__lykeion_desktop__/missing.js`, origin, '/bundle')).toBe(
      '/bundle/missing.js',
    );
  });
  it('trusts only the main frame of the exact local entry, not remote paths', () => {
    const frame = { url: 'https://lab.example/__lykeion_desktop__/#/studies' };
    const sender = { mainFrame: frame };
    expect(trustedDesktopFrame({ sender, senderFrame: frame }, sender, 'https://lab.example')).toBe(
      true,
    );
    expect(trustedDesktopFrame({ sender, senderFrame: {} }, sender, 'https://lab.example')).toBe(
      false,
    );
    frame.url = 'https://lab.example/api/me';
    expect(trustedDesktopFrame({ sender, senderFrame: frame }, sender, 'https://lab.example')).toBe(
      false,
    );
    frame.url = 'https://evil.example/__lykeion_desktop__/';
    expect(trustedDesktopFrame({ sender, senderFrame: frame }, sender, 'https://lab.example')).toBe(
      false,
    );
  });
  it('blocks remote executable resources and all requests to a different server', () => {
    let before: (
      details: { url: string; resourceType: string },
      done: (value: { cancel: boolean }) => void,
    ) => void = () => {};
    const fake = {
      webRequest: {
        onBeforeRequest: (_filter: unknown, callback: typeof before) => {
          before = callback;
        },
      },
      protocol: { handle: vi.fn() },
    };
    installDesktopRenderer(fake as never, 'https://lab.example', '/bundle');
    const decision = (url: string, resourceType: string) => {
      const done = vi.fn();
      before({ url, resourceType }, done);
      return done.mock.calls[0]![0].cancel;
    };
    expect(decision('https://lab.example/__lykeion_desktop__/assets/app.js', 'script')).toBe(false);
    expect(decision('https://lab.example/api/hostile.js', 'script')).toBe(true);
    expect(decision('https://lab.example/api/file', 'xhr')).toBe(false);
    expect(decision('https://other.example/api/file', 'xhr')).toBe(true);
    expect(decision('https://lab.example/api/me', 'mainFrame')).toBe(true);
    expect(decision('wss://lab.example/ws', 'webSocket')).toBe(false);
    expect(decision('wss://other.example/ws', 'webSocket')).toBe(true);
    expect(decision('ws://lab.example/ws', 'webSocket')).toBe(true);
  });
});
