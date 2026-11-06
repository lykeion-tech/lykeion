import { expect, it, vi } from 'vitest';
import { queuePreview } from './preview-queue';

it('limits concurrent preview loads and cancels work that leaves the viewport', async () => {
  let active = 0,
    maximum = 0;
  const releases: Array<() => void> = [];
  const work = vi.fn(
    () =>
      new Promise<void>((resolve) => {
        active++;
        maximum = Math.max(maximum, active);
        releases.push(() => {
          active--;
          resolve();
        });
      }),
  );
  const controllers = Array.from({ length: 6 }, () => new AbortController());
  const tasks = controllers.map((c) => queuePreview(c.signal, work).catch(() => undefined));
  await vi.waitFor(() => expect(work).toHaveBeenCalledTimes(4));
  controllers[5]!.abort();
  releases.shift()!();
  await vi.waitFor(() => expect(work).toHaveBeenCalledTimes(5));
  while (releases.length) releases.shift()!();
  await Promise.all(tasks);
  expect(maximum).toBe(4);
  expect(work).toHaveBeenCalledTimes(5);
});
