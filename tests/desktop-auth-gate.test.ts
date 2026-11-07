// @vitest-environment node
import { expect, it } from 'vitest';
import { DesktopAuthGate } from '../apps/desktop/src/desktop-auth-gate';

it('drains admitted authentication requests despite daemon failure and rejects new requests', async () => {
  const gate = new DesktopAuthGate();
  await gate.mutation(1);
  await gate.mutation(-1);
  let finished = false;
  const shutdown = gate
    .shutdown(async () => {
      throw new Error('daemon failed');
    })
    .then((result) => {
      finished = true;
      return result;
    });
  await expect(gate.mutation(2)).rejects.toThrow();
  gate.finishMutation(1);
  await Promise.resolve();
  expect(finished).toBe(false);
  gate.finishMutation(-1);
  const result = await shutdown;
  expect(result[0]?.status).toBe('fulfilled');
  expect(result[1]?.status).toBe('rejected');
});
