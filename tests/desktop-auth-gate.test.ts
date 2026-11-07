// @vitest-environment node
import { expect, it } from 'vitest';
import { DesktopAuthGate } from '../apps/desktop/src/desktop-auth-gate';

it('transfers only an admitted, path-bound request across shutdown', async () => {
  const gate = new DesktopAuthGate();
  const token = gate.reserve('/auth/logout')!;
  let finished = false;
  const shutdown = gate
    .shutdown(async () => {
      throw new Error('daemon failed');
    })
    .then((result) => {
      finished = true;
      return result;
    });
  expect(gate.reserve('/auth/logout')).toBeNull();
  expect(gate.continueMutation(-1, token, '/auth/verify-code')).toBe(false);
  expect(gate.continueMutation(-1, token, '/auth/logout')).toBe(true);
  expect(gate.continueMutation(-2, token, '/auth/logout')).toBe(false);
  await Promise.resolve();
  expect(finished).toBe(false);
  gate.finishMutation(-1);
  const result = await shutdown;
  expect(result[0]?.status).toBe('fulfilled');
  expect(result[1]?.status).toBe('rejected');
});

it('bounds stalled shutdown and retries without admitting new requests', async () => {
  const gate = new DesktopAuthGate();
  const token = gate.reserve('/auth/logout')!;
  expect((await gate.shutdown(async () => {}, 20))[0]?.status).toBe('rejected');
  expect(gate.reserve('/auth/logout')).toBeNull();
  gate.release(token);
  expect((await gate.shutdown(async () => {}, 20))[0]?.status).toBe('fulfilled');
});

it('invalidates abandoned logout and clears only after claimed writes finish', async () => {
  const gate = new DesktopAuthGate();
  const stale = gate.reserve('/auth/logout')!;
  const active = gate.reserve('/auth/verify-code')!;
  expect(gate.continueMutation(-1, active, '/auth/verify-code')).toBe(true);
  gate.abandonUnclaimed();
  expect(gate.continueMutation(-2, stale, '/auth/logout')).toBe(false);
  let cleared = false;
  const shutdown = gate.shutdown(
    async () => {},
    1000,
    async () => {
      cleared = true;
    },
  );
  await Promise.resolve();
  expect(cleared).toBe(false);
  gate.finishMutation(-1);
  expect((await shutdown)[0]?.status).toBe('fulfilled');
  expect(cleared).toBe(true);
});

it('reports abandoned verification once so a subsequent quit can succeed', async () => {
  const gate = new DesktopAuthGate();
  const stale = gate.reserve('/auth/verify-code')!;
  gate.abandonUnclaimed();
  expect(gate.continueMutation(-1, stale, '/auth/verify-code')).toBe(false);
  expect((await gate.shutdown(async () => {}, 100))[0]?.status).toBe('rejected');
  expect((await gate.shutdown(async () => {}, 100))[0]?.status).toBe('fulfilled');
});

it('allows verification to be retried after the interruption is acknowledged', () => {
  const gate = new DesktopAuthGate();
  gate.reserve('/auth/verify-code');
  gate.abandonUnclaimed();
  expect(gate.reserve('/auth/verify-code')).toBeNull();
  expect(gate.acknowledgeInterruptedVerification()).toBe(true);
  expect(gate.reserve('/auth/verify-code')).toBeTruthy();
  expect(gate.acknowledgeInterruptedVerification()).toBe(false);
});

it('retains failed logout recovery for a later retry', async () => {
  const gate = new DesktopAuthGate();
  gate.reserve('/auth/logout');
  gate.abandonUnclaimed();
  const failed = await gate.shutdown(
    async () => {},
    100,
    async () => {
      throw new Error('storage failure');
    },
  );
  expect(failed[0]?.status).toBe('rejected');
  let cleared = false;
  const retry = await gate.shutdown(
    async () => {},
    100,
    async () => {
      cleared = true;
    },
  );
  expect(retry[0]?.status).toBe('fulfilled');
  expect(cleared).toBe(true);
});

it('holds reopened-window network until abandoned logout is cleared', async () => {
  const gate = new DesktopAuthGate();
  gate.reserve('/auth/logout');
  gate.abandonUnclaimed();
  let finish!: () => void;
  const recovery = gate.recoverAbandonedLogout(
    () =>
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
  );
  let admitted = false;
  const request = gate.networkReady().then(() => {
    admitted = true;
  });
  await Promise.resolve();
  expect(admitted).toBe(false);
  finish();
  await recovery;
  await request;
  expect(admitted).toBe(true);
});

it('bounds a hung cookie cleanup without starting a conflicting retry', async () => {
  const gate = new DesktopAuthGate();
  gate.reserve('/auth/logout');
  gate.abandonUnclaimed();
  let finish!: () => void;
  let calls = 0;
  const clear = () => {
    calls++;
    return new Promise<void>((resolve) => {
      finish = resolve;
    });
  };
  await expect(gate.recoverAbandonedLogout(clear, 20)).rejects.toThrow('did not finish');
  await expect(gate.recoverAbandonedLogout(clear, 20)).rejects.toThrow('did not finish');
  expect(calls).toBe(1);
  expect(gate.networkBlocked()).toBe(true);
  finish();
  await gate.recoverAbandonedLogout(clear, 100);
  expect(gate.networkBlocked()).toBe(false);
});
