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
