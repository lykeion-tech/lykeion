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
