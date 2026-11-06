import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import TiffViewer from './TiffViewer.vue';
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it('terminates stale and timed-out TIFF workers', async () => {
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
  const app = createTreeApp({ render: () => h(TiffViewer, { blob: file.value }) });
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
it('tells users when a 16-bit image is displayed at reduced precision', async () => {
  let worker: FakeWorker;
  class FakeWorker {
    onmessage: ((event: unknown) => void) | null = null;
    onerror = null;
    terminate = vi.fn();
    postMessage = vi.fn();
    constructor() {
      worker = this;
    }
  }
  vi.stubGlobal('Worker', FakeWorker);
  vi.stubGlobal('self', { location: new URL('http://localhost/') });
  const app = createTreeApp({ render: () => h(TiffViewer, { blob: new Blob(['file']) }) });
  const root = node('root');
  app.mount(root);
  worker!.onmessage!({
    data: {
      result: {
        width: 1,
        height: 1,
        pages: 1,
        aspect: 1,
        spacingKnown: true,
        displayReducedPrecision: true,
        rgba: new Uint8ClampedArray([0, 0, 0, 255]),
      },
    },
  });
  await nextTick();
  expect(textOf(root)).toContain('displayed with 8-bit precision');
  app.unmount();
});
