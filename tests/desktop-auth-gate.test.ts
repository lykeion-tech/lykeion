// @vitest-environment node
import { describe, expect, it, vi } from 'vitest';
import { DesktopAuthGate } from '../apps/desktop/src/desktop-auth-gate';
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}
describe('desktop authentication gate', () => {
  it('quarantines a failed rollback and blocks shutdown until recovery succeeds', async () => {
    const gate = new DesktopAuthGate();
    const attempt = gate.begin();
    const install = deferred();
    let canRestore = false;
    const rollback = vi.fn(async () => {
      if (!canRestore) throw new Error('cookie restore failed');
    });
    const commit = gate.commit(attempt, () => install.promise, rollback);
    await Promise.resolve();
    gate.cancel();
    install.resolve();
    await expect(commit).rejects.toThrow('cookie restore failed');
    expect(() => gate.begin()).toThrow();
    await expect(gate.mutation(42)).rejects.toThrow();
    gate.finishMutation(42);
    const failed = await gate.shutdown(async () => {});
    expect(failed[0]?.status).toBe('rejected');
    canRestore = true;
    const recovered = await gate.shutdown(async () => {});
    expect(recovered[0]?.status).toBe('fulfilled');
    expect(rollback).toHaveBeenCalledTimes(3);
  });
  it('waits for rollback despite daemon failure and refuses new sign-ins during shutdown', async () => {
    const gate = new DesktopAuthGate();
    const attempt = gate.begin();
    const install = deferred();
    const rollback = vi.fn();
    const commit = gate.commit(attempt, () => install.promise, rollback);
    await Promise.resolve();
    let finished = false;
    const shutdown = gate
      .shutdown(async () => {
        throw new Error('daemon stop failed');
      })
      .then(() => {
        finished = true;
      });
    expect(() => gate.begin()).toThrow();
    await Promise.resolve();
    await Promise.resolve();
    expect(finished).toBe(false);
    install.resolve();
    expect(await commit).toBe(false);
    await shutdown;
    expect(rollback).toHaveBeenCalledOnce();
    expect(finished).toBe(true);
  });
  it('waits for an invalidated installation and rollback before shutdown', async () => {
    const gate = new DesktopAuthGate();
    const attempt = gate.begin();
    const wait = deferred();
    const rollback = vi.fn();
    const commit = gate.commit(
      attempt,
      async () => {
        await wait.promise;
      },
      rollback,
    );
    await Promise.resolve();
    gate.cancel();
    let settled = false;
    const shutdown = gate.settle().then(() => {
      settled = true;
    });
    await Promise.resolve();
    expect(settled).toBe(false);
    wait.resolve();
    expect(await commit).toBe(false);
    await shutdown;
    expect(rollback).toHaveBeenCalledOnce();
  });
  it('prevents an old OAuth completion after an email login or logout begins', async () => {
    const gate = new DesktopAuthGate();
    const attempt = gate.begin();
    await gate.mutation();
    const commit = vi.fn();
    expect(await gate.commit(attempt, commit)).toBe(false);
    expect(commit).not.toHaveBeenCalled();
  });
  it('holds later authentication mutations until a running cookie install finishes', async () => {
    const gate = new DesktopAuthGate();
    const attempt = gate.begin();
    const wait = deferred();
    let installed = false;
    const commit = gate.commit(attempt, async () => {
      await wait.promise;
      installed = true;
    });
    await Promise.resolve();
    let allowed = false;
    const mutation = gate.mutation().then(() => {
      allowed = true;
    });
    await Promise.resolve();
    expect(allowed).toBe(false);
    wait.resolve();
    await commit;
    await mutation;
    expect(installed).toBe(true);
    expect(allowed).toBe(true);
  });
  it('invalidates older attempts when a new attempt begins', async () => {
    const gate = new DesktopAuthGate();
    const old = gate.begin();
    gate.begin();
    expect(await gate.commit(old, vi.fn())).toBe(false);
  });
  it('rolls back a cookie install invalidated during installation before releasing a logout', async () => {
    const gate = new DesktopAuthGate();
    const attempt = gate.begin();
    const wait = deferred();
    const rollback = vi.fn();
    const commit = gate.commit(
      attempt,
      async () => {
        await wait.promise;
      },
      rollback,
    );
    await Promise.resolve();
    const logout = gate.mutation();
    wait.resolve();
    expect(await commit).toBe(false);
    await logout;
    expect(rollback).toHaveBeenCalledOnce();
  });
  it('refuses a new browser login while email authentication is in flight', async () => {
    const gate = new DesktopAuthGate();
    await gate.mutation(19);
    expect(() => gate.begin()).toThrow();
    gate.finishMutation(19);
    expect(gate.begin()).toBeGreaterThan(0);
  });
});
