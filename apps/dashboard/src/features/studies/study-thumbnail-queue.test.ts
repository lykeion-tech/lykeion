import { expect, it, vi } from 'vitest';
import { queueThumbnail } from './study-thumbnail-queue';

it('limits concurrent thumbnail requests and drops a cancelled queued request', async () => {
  const releases: (() => void)[] = [];
  const started = vi.fn();
  const controllers = Array.from({ length: 5 }, () => new AbortController());
  const jobs = controllers.map((controller) =>
    queueThumbnail(() => {
      started();
      return new Promise<void>((resolve) => {
        releases.push(resolve);
      });
    }, controller.signal),
  );
  expect(started).toHaveBeenCalledTimes(4);
  controllers[4]!.abort();
  releases.forEach((release) => release());
  await Promise.all(jobs);
  expect(started).toHaveBeenCalledTimes(4);
});
