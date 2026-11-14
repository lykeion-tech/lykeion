import { describe, expect, it, vi } from 'vitest';
import { DesktopUpdateCheck, installDesktopUpdateIPC } from '../apps/desktop/src/desktop-update';

describe('desktop update check', () => {
  it('accepts only allowlisted error categories and rejects hostile helper fields', async () => {
    let result: unknown = { state: 'error', category: 'network' };
    const check = new DesktopUpdateCheck(() => true, () => 'doc', () => true, async () => true, async () => result as never);
    expect(await check.check()).toEqual({ state: 'error', category: 'network' });
    result = { state: 'error', category: 'network', detail: 'https://secret.test/?token=secret' };
    await expect(check.check()).rejects.toThrow('Update check unavailable');
    result = { state: 'error', category: 'secret token' };
    await expect(check.check()).rejects.toThrow('Update check unavailable');
  });
  it('does not inspect or execute a helper when the release gate is closed', async () => {
    const verify = vi.fn(async () => true);
    const run = vi.fn(async () => ({ state: 'current' as const, version: '1.2.3' }));
    const check = new DesktopUpdateCheck(
      () => false,
      () => 'doc',
      () => true,
      verify,
      run,
    );
    expect(check.available()).toBe(false);
    await expect(check.check()).rejects.toThrow('Update check unavailable');
    expect(verify).not.toHaveBeenCalled();
    expect(run).not.toHaveBeenCalled();
  });

  it('does not start a helper after the trusted document changes', async () => {
    let document: string | null = 'before';
    let finish!: (value: boolean) => void;
    const verify = vi.fn(
      () =>
        new Promise<boolean>((resolve) => {
          finish = resolve;
        }),
    );
    const run = vi.fn(async () => ({ state: 'current' as const, version: '1.2.3' }));
    const check = new DesktopUpdateCheck(
      () => true,
      () => document,
      () => true,
      verify,
      run,
    );
    const pending = check.check();
    document = 'after';
    finish(true);
    await expect(pending).rejects.toThrow('Update check unavailable');
    expect(run).not.toHaveBeenCalled();
  });

  it('cancels a running operation without returning a stale result', async () => {
    let finish!: (value: { state: 'current'; version: string }) => void;
    let aborted = false;
    const run = vi.fn((signal: AbortSignal) => {
      signal.addEventListener('abort', () => {
        aborted = true;
      });
      return new Promise<{ state: 'current'; version: string }>((resolve) => {
        finish = resolve;
      });
    });
    const check = new DesktopUpdateCheck(
      () => true,
      () => 'doc',
      () => true,
      async () => true,
      run,
    );
    const pending = check.check();
    await vi.waitFor(() => expect(run).toHaveBeenCalled());
    check.cancel();
    let stopped = false;
    const stop = check.waitForStop().then(() => { stopped = true; });
    await Promise.resolve();
    expect(stopped).toBe(false);
    await expect(check.check()).rejects.toThrow('Update check unavailable');
    finish({ state: 'current', version: '1.2.3' });
    await expect(pending).rejects.toThrow('Update check unavailable');
    await stop;
    expect(stopped).toBe(true);
    expect(aborted).toBe(true);
  });

  it('accepts only the current trusted main frame and no renderer arguments', async () => {
    const handlers = new Map<
      string,
      (event: { sender: unknown; senderFrame: unknown }, value?: unknown) => unknown
    >();
    const ipc = {
      handle: (
        name: string,
        fn: (event: { sender: unknown; senderFrame: unknown }, value?: unknown) => unknown,
      ) => {
        handlers.set(name, fn);
      },
    };
    const current = { mainFrame: { url: 'https://example.test/__lykeion_desktop__/' } };
    const update = new DesktopUpdateCheck(
      () => false,
      () => 'doc',
      () => true,
      async () => true,
      async () => ({ state: 'current', version: '1.2.3' }),
    );
    installDesktopUpdateIPC(
      ipc,
      () => current,
      'https://example.test',
      () => 'doc',
      update,
    );
    const available = handlers.get('lykeion:update:available');
    if (!available) throw new Error('availability handler missing');
    expect(available({ sender: current, senderFrame: current.mainFrame }, 'doc')).toBe(false);
    expect(() => available({ sender: {}, senderFrame: current.mainFrame }, 'doc')).toThrow(
      'Invalid sender',
    );
    expect(() => available({ sender: current, senderFrame: {} }, 'doc')).toThrow('Invalid sender');
    expect(() =>
      available({ sender: current, senderFrame: current.mainFrame }, 'arbitrary'),
    ).toThrow('Invalid update request');
  });
});
