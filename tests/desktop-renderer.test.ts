// @vitest-environment node
import { describe, expect, it, vi } from 'vitest';
import { EventEmitter } from 'node:events';
import { loadDesktopWindow } from '../apps/desktop/src/desktop-window-recovery';
import {
  desktopEntryURL,
  desktopAssetPath,
  trustedDesktopFrame,
  installDesktopRenderer,
  desktopPartition,
  validExternalURL,
  createRequestCancellation,
  bindDesktopRequestLifetime,
} from '../apps/desktop/src/renderer-origin';

describe('bundled window recovery', () => {
  const origin = 'https://lab.example';
  function fixture() {
    const contents = new EventEmitter();
    let current = true;
    let fail!: (error: Error) => void;
    let choose!: (retry: boolean) => void;
    const owner = Object.assign(new EventEmitter(), {
      webContents: contents,
      loadURL: vi.fn(
        () =>
          new Promise<void>((_, reject) => {
            fail = reject;
          }),
      ),
    });
    const ask = vi.fn(
      (_signal: AbortSignal) =>
        new Promise<boolean>((resolve) => {
          choose = resolve;
        }),
    );
    const recovery = loadDesktopWindow(owner as never, origin, () => current, ask);
    return {
      owner,
      contents,
      ask,
      recovery,
      crash: () => contents.emit('render-process-gone', {}, { reason: 'crashed' }),
      fail: (error = new Error('entry unavailable')) => fail(error),
      choose: (value: boolean) => choose(value),
      replace: () => {
        current = false;
      },
    };
  }
  it('coalesces crash and failed entry load until the user retries the fixed URL', async () => {
    const t = fixture();
    t.crash();
    t.crash();
    t.fail();
    await Promise.resolve();
    expect(t.ask).toHaveBeenCalledTimes(1);
    expect(t.owner.loadURL).toHaveBeenCalledTimes(1);
    t.choose(true);
    await vi.waitFor(() => expect(t.owner.loadURL).toHaveBeenCalledTimes(2));
    expect(t.owner.loadURL.mock.calls).toEqual([
      [desktopEntryURL(origin)],
      [desktopEntryURL(origin)],
    ]);
    t.fail();
    await vi.waitFor(() => expect(t.ask).toHaveBeenCalledTimes(2));
    t.choose(false);
    await Promise.resolve();
    expect(t.owner.loadURL).toHaveBeenCalledTimes(2);
  });
  it.each(['close', 'quit', 'replace', 'navigate'])(
    'drops a stale Retry after %s',
    async (action) => {
      const t = fixture();
      t.fail();
      await vi.waitFor(() => expect(t.ask).toHaveBeenCalledOnce());
      if (action === 'close') t.owner.emit('close');
      if (action === 'quit') t.recovery.pause();
      if (action === 'replace') t.replace();
      if (action === 'navigate') t.contents.emit('did-navigate', {}, desktopEntryURL(origin), 200);
      t.choose(true);
      await Promise.resolve();
      expect(t.owner.loadURL).toHaveBeenCalledTimes(1);
      if (action !== 'replace') expect(t.ask.mock.calls[0]![0].aborted).toBe(true);
    },
  );
  it('restores the current failure prompt after an unsuccessful quit', async () => {
    const t = fixture();
    t.crash();
    t.recovery.pause();
    t.choose(true);
    await Promise.resolve();
    expect(t.owner.loadURL).toHaveBeenCalledTimes(1);
    t.recovery.resume();
    expect(t.ask).toHaveBeenCalledTimes(2);
    t.choose(true);
    await vi.waitFor(() => expect(t.owner.loadURL).toHaveBeenCalledTimes(2));
  });
  it('ignores clean exits, aborted loads, API failures and untrusted navigation failures', async () => {
    const t = fixture();
    t.contents.emit('render-process-gone', {}, { reason: 'clean-exit' });
    t.contents.emit('did-fail-load', {}, -2, 'failed', 'https://foreign.example', true);
    t.contents.emit('did-fail-load', {}, -2, 'failed', origin + '/api/me', false);
    t.fail(Object.assign(new Error('aborted'), { code: 'ERR_ABORTED', errno: -3 }));
    await Promise.resolve();
    expect(t.ask).not.toHaveBeenCalled();
  });
});

describe('desktop renderer origin', () => {
  it('does not let an earlier redirect release the current request registration', () => {
    const registry = createRequestCancellation();
    const doc = registry.beginDocument();
    const id = `${doc}.11111111-1111-4111-8111-111111111111`;
    const oldHop = vi.fn();
    const newHop = vi.fn();
    expect(registry.register(id, oldHop)).toBe(true);
    registry.release(id, oldHop);
    expect(registry.register(id, newHop)).toBe(true);
    registry.release(id, oldHop);
    registry.cancel(id);
    expect(newHop).toHaveBeenCalledOnce();
  });
  it('drains active requests when a document departs without affecting new registrations', () => {
    const registry = createRequestCancellation();
    const doc = registry.beginDocument();
    const first = `${doc}.11111111-1111-4111-8111-111111111111`;
    const early = `${doc}.33333333-3333-4333-8333-333333333333`;
    const old = vi.fn();
    const next = vi.fn();
    registry.register(first, old);
    registry.cancel(early);
    registry.cancelAll();
    expect(old).toHaveBeenCalledOnce();
    expect(registry.register(early, vi.fn())).toBe(false);
    const nextDoc = registry.beginDocument();
    const second = `${nextDoc}.22222222-2222-4222-8222-222222222222`;
    expect(registry.register(first, vi.fn())).toBe(false);
    expect(registry.register(second, next)).toBe(true);
    registry.cancel(first);
    expect(next).not.toHaveBeenCalled();
  });
  it('ignores attempted navigations and drains only current crashes and destruction', () => {
    const contents = new EventEmitter();
    const cancellation = { cancelAll: vi.fn() };
    let current = true;
    const closed = bindDesktopRequestLifetime(
      contents as never,
      cancellation as never,
      () => current,
    );
    contents.emit('did-start-navigation', {}, 'https://lab.example/', true, true);
    contents.emit('did-start-navigation', {}, 'https://lab.example/', false, false);
    expect(cancellation.cancelAll).not.toHaveBeenCalled();
    contents.emit('did-start-navigation', {}, 'https://lab.example/', false, true);
    expect(cancellation.cancelAll).not.toHaveBeenCalled();
    contents.emit('render-process-gone');
    expect(cancellation.cancelAll).toHaveBeenCalledTimes(1);
    current = false;
    contents.emit('destroyed');
    closed();
    expect(cancellation.cancelAll).toHaveBeenCalledTimes(1);
  });
  it('cancels only the matching active request and fences early or duplicate IDs', () => {
    const registry = createRequestCancellation();
    const doc = registry.beginDocument();
    const first = `${doc}.11111111-1111-4111-8111-111111111111`;
    const second = `${doc}.22222222-2222-4222-8222-222222222222`;
    const third = `${doc}.33333333-3333-4333-8333-333333333333`;
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
  it.each(['https://app.lykeion.tech', 'https://lab.example:8443', 'http://127.0.0.1:8080'])(
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
    expect(decision('https://lab.example/auth/google/callback?code=test', 'xhr')).toBe(true);
    expect(decision('https://lab.example/auth/desktop/google/exchange', 'xhr')).toBe(true);
    expect(decision('https://lab.example/auth/desktop/google/start', 'xhr')).toBe(true);
    expect(decision('https://lab.example/auth/desktop%2Fgoogle/exchange', 'xhr')).toBe(true);
    expect(decision('https://lab.example/%61uth/google/callback', 'xhr')).toBe(true);
    expect(decision('https://other.example/api/file', 'xhr')).toBe(true);
    expect(decision('https://lab.example/api/me', 'mainFrame')).toBe(true);
    expect(decision('wss://lab.example/ws', 'webSocket')).toBe(false);
    expect(decision('wss://other.example/ws', 'webSocket')).toBe(true);
    expect(decision('ws://lab.example/ws', 'webSocket')).toBe(true);
  });
});
