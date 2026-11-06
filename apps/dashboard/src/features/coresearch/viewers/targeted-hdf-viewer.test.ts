import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import TargetedHdfViewer from './TargetedHdfViewer.vue';
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it('terminates stale and timed-out targeted readers', async () => {
  vi.useFakeTimers();
  const workers: FakeWorker[] = [];
  class FakeWorker {
    onmessage: ((event: MessageEvent<any>) => void) | null = null;
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
  const app = createTreeApp({
    render: () => h(TargetedHdfViewer, { blob: blob.value, mode: 'netcdf' }),
  });
  const root = node('root');
  app.mount(root);
  blob.value = new Blob(['second']);
  await nextTick();
  expect(workers[0]!.terminate).toHaveBeenCalledOnce();
  workers[0]!.onmessage?.({
    data: {
      preview: {
        items: [{ name: 'stale', type: 'int', shape: [], values: ['1'] }],
        partial: false,
      },
    },
  } as MessageEvent);
  await nextTick();
  expect(textOf(root)).not.toContain('stale');
  await vi.advanceTimersByTimeAsync(5000);
  await nextTick();
  expect(workers[1]!.terminate).toHaveBeenCalledOnce();
  expect(textOf(root)).toContain('Preview unavailable');
  app.unmount();
});
