import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import NpzViewer from './NpzViewer.vue';

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it('terminates stale workers on file change and on timeout', async () => {
  vi.useFakeTimers();
  const workers: FakeWorker[] = [];
  class FakeWorker {
    onmessage = null;
    onerror = null;
    terminate = vi.fn();
    postMessage = vi.fn();
    constructor() {
      workers.push(this);
    }
  }
  vi.stubGlobal('Worker', FakeWorker);
  vi.stubGlobal('self', { location: new URL('http://localhost/') });
  const blob = ref(new Blob(['first']));
  const app = createTreeApp({ render: () => h(NpzViewer, { blob: blob.value }) });
  const root = node('root');
  app.mount(root);
  expect(workers, textOf(root)).toHaveLength(1);
  blob.value = new Blob(['second']);
  await nextTick();
  expect(workers[0]!.terminate).toHaveBeenCalledOnce();
  expect(workers).toHaveLength(2);
  await vi.advanceTimersByTimeAsync(5000);
  expect(workers[1]!.terminate).toHaveBeenCalledOnce();
  await nextTick();
  expect(textOf(root)).toContain('Archive preview unavailable');
  app.unmount();
});
