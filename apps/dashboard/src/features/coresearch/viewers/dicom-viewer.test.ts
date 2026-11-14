import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import DicomViewer from './DicomViewer.vue';
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it('terminates stale and timed-out image workers without exposing errors', async () => {
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
  const file = ref(new Blob(['first']));
  const app = createTreeApp({ render: () => h(DicomViewer, { blob: file.value }) });
  const root = node('root');
  app.mount(root);
  expect(workers).toHaveLength(1);
  file.value = new Blob(['second']);
  await nextTick();
  expect(workers[0]!.terminate).toHaveBeenCalledOnce();
  await vi.advanceTimersByTimeAsync(5000);
  await nextTick();
  expect(workers[1]!.terminate).toHaveBeenCalledOnce();
  expect(textOf(root)).toContain('Image preview unavailable');
  app.unmount();
});
