import { describe, expect, it, vi } from 'vitest';
import { AuthenticationReload } from '../apps/desktop/src/desktop-auth-reload';
import { DesktopAuthGate } from '../apps/desktop/src/desktop-auth-gate';

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe('authentication reload', () => {
  it('retries after canceled quit only when persistence and mutations drain', async () => {
    let quitting = false;
    const current = true;
    let safe = true;
    const queued: Array<() => void> = [];
    const reload = vi.fn();
    const pending = new AuthenticationReload(
      () => current,
      () => quitting,
      () => safe,
      reload,
      (fn) => queued.push(fn),
    );
    pending.request();
    quitting = true;
    queued.shift()!();
    expect(reload).not.toHaveBeenCalled();
    const drained = deferred();
    safe = false;
    quitting = false;
    pending.resumeAfterQuit(drained.promise);
    await Promise.resolve();
    expect(reload).not.toHaveBeenCalled();
    safe = true;
    drained.resolve();
    await Promise.resolve();
    expect(reload).toHaveBeenCalledOnce();
  });

  it('discards stale documents and never reloads during actual quit', async () => {
    let current = true;
    let quitting = false;
    const queue: Array<() => void> = [];
    const reload = vi.fn();
    const pending = new AuthenticationReload(
      () => current,
      () => quitting,
      () => true,
      reload,
      (fn) => queue.push(fn),
    );
    pending.request();
    quitting = true;
    queue.shift()!();
    const drained = deferred();
    pending.resumeAfterQuit(drained.promise);
    current = false;
    quitting = false;
    drained.resolve();
    await Promise.resolve();
    expect(reload).not.toHaveBeenCalled();
  });

  it('does not reload if quit is still in progress after the drain', async () => {
    let quitting = true;
    const reload = vi.fn();
    const pending = new AuthenticationReload(
      () => true,
      () => quitting,
      () => true,
      reload,
    );
    pending.request();
    await new Promise<void>((resolve) => setImmediate(resolve));
    pending.resumeAfterQuit(Promise.resolve());
    await Promise.resolve();
    expect(reload).not.toHaveBeenCalled();
    quitting = false;
    pending.resumeAfterQuit(Promise.resolve());
    await Promise.resolve();
    expect(reload).toHaveBeenCalledOnce();
  });

  it('waits for the real auth mutation and persistence after failed quit', async () => {
    const gate = new DesktopAuthGate();
    const reservation = gate.reserve('/auth/verify-code')!;
    expect(gate.continueMutation(12, reservation, '/auth/verify-code')).toBe(true);
    expect((await gate.shutdown(async () => {}, 1))[0]?.status).toBe('rejected');
    const flush = deferred();
    gate.resumeAfterQuit(flush.promise);
    const reload = vi.fn();
    const pending = new AuthenticationReload(
      () => true,
      () => false,
      () => gate.canFinishVerification(),
      reload,
      () => {},
    );
    pending.request();
    pending.resumeAfterQuit(gate.waitForVerificationReady(new AbortController().signal));
    flush.resolve();
    await Promise.resolve();
    expect(reload).not.toHaveBeenCalled();
    gate.finishMutation(12);
    await new Promise<void>((resolve) => setImmediate(resolve));
    expect(reload).toHaveBeenCalledOnce();
  });

  it('waits through a second failed quit for the original pending flush', async () => {
    const gate = new DesktopAuthGate();
    const token = gate.reserve('/auth/verify-code')!;
    expect(gate.continueMutation(22, token, '/auth/verify-code')).toBe(true);
    await gate.shutdown(async () => {}, 1);
    const flush = deferred();
    gate.resumeAfterQuit(flush.promise);
    await gate.shutdown(async () => {}, 1);
    gate.resumeAfterQuit();
    const reload = vi.fn();
    const pending = new AuthenticationReload(
      () => true,
      () => false,
      () => gate.canFinishVerification(),
      reload,
      () => {},
    );
    pending.request();
    pending.resumeAfterQuit(gate.waitForVerificationReady(new AbortController().signal));
    gate.finishMutation(22);
    await Promise.resolve();
    expect(reload).not.toHaveBeenCalled();
    flush.resolve();
    await new Promise<void>((resolve) => setImmediate(resolve));
    expect(reload).toHaveBeenCalledOnce();
  });

  it('waits for abandoned logout recovery before permitting verification', async () => {
    const gate = new DesktopAuthGate();
    const token = gate.reserve('/auth/logout')!;
    gate.release(token);
    const reload = vi.fn();
    const pending = new AuthenticationReload(
      () => true,
      () => false,
      () => gate.canFinishVerification(),
      reload,
      () => {},
    );
    pending.request();
    pending.resumeAfterQuit(gate.waitForVerificationReady(new AbortController().signal));
    const clear = deferred();
    const recovery = gate.recoverAbandonedLogout(() => clear.promise);
    await Promise.resolve();
    expect(reload).not.toHaveBeenCalled();
    clear.resolve();
    await recovery;
    await Promise.resolve();
    expect(reload).toHaveBeenCalledOnce();
  });

  it('never reloads after a failed flush or an obsolete quit recovery', async () => {
    const reload = vi.fn();
    const pending = new AuthenticationReload(
      () => true,
      () => false,
      () => true,
      reload,
      () => {},
    );
    pending.request();
    const first = deferred();
    const second = deferred();
    pending.resumeAfterQuit(first.promise);
    pending.resumeAfterQuit(second.promise);
    first.resolve();
    await Promise.resolve();
    expect(reload).not.toHaveBeenCalled();
    second.resolve();
    await Promise.resolve();
    expect(reload).toHaveBeenCalledOnce();

    const failedReload = vi.fn();
    const failed = new AuthenticationReload(
      () => true,
      () => false,
      () => true,
      failedReload,
      () => {},
    );
    failed.request();
    failed.resumeAfterQuit(Promise.reject(new Error('cookie flush failed')));
    await Promise.resolve();
    expect(failedReload).not.toHaveBeenCalled();
  });

  it('subscribes again if a new auth mutation blocks the queued reload', async () => {
    let safe = true;
    const reload = vi.fn();
    const blocked = vi.fn();
    const pending = new AuthenticationReload(
      () => true,
      () => false,
      () => safe,
      reload,
      () => {},
      blocked,
    );
    pending.request();
    pending.resumeAfterQuit(Promise.resolve());
    safe = false;
    await Promise.resolve();
    expect(reload).not.toHaveBeenCalled();
    expect(blocked).toHaveBeenCalledOnce();
    safe = true;
    pending.resumeAfterQuit(Promise.resolve());
    await Promise.resolve();
    expect(reload).toHaveBeenCalledOnce();
  });

  it('removes a gate waiter when its document is abandoned', async () => {
    const gate = new DesktopAuthGate();
    const reservation = gate.reserve('/auth/verify-code')!;
    expect(gate.continueMutation(33, reservation, '/auth/verify-code')).toBe(true);
    const controller = new AbortController();
    const wait = gate.waitForVerificationReady(controller.signal);
    controller.abort();
    await expect(wait).rejects.toThrow('Authentication reload canceled');
    gate.finishMutation(33);
    await expect(
      gate.waitForVerificationReady(new AbortController().signal),
    ).resolves.toBeUndefined();
  });
});
