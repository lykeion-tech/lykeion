import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import BcfViewer from './BcfViewer.vue';
import ArtifactViewer from './ArtifactViewer.vue';
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it('routes BCF files to the binary viewer without reading them as text', async () => {
  const workers: FakeWorker[] = [];
  class FakeWorker {
    onmessage = null;
    onerror = null;
    terminate = vi.fn();
    postMessage = vi.fn();
    constructor() { workers.push(this); }
  }
  vi.stubGlobal('Worker', FakeWorker);
  vi.stubGlobal('self', { location: new URL('http://localhost/') });
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:bcf');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const blob = new Blob(['private binary sentinel']);
  const text = vi.spyOn(blob, 'text');
  const app = createTreeApp({ render: () => h(ArtifactViewer, { blob, path: 'sample.BCF' }) });
  const root = node('root');
  app.mount(root);
  await nextTick();
  await nextTick();
  expect(workers).toHaveLength(1);
  expect(workers[0]!.postMessage).toHaveBeenCalledWith({ blob });
  expect(text).not.toHaveBeenCalled();
  expect(textOf(root)).not.toContain('private binary sentinel');
  app.unmount();
  vi.restoreAllMocks();
});
it('terminates stale and timed-out workers', async () => {
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
  const blob = ref(new Blob(['one']));
  const app = createTreeApp({ render: () => h(BcfViewer, { blob: blob.value }) });
  const root = node('root');
  app.mount(root);
  blob.value = new Blob(['two']);
  await nextTick();
  expect(workers[0]!.terminate).toHaveBeenCalledOnce();
  await vi.advanceTimersByTimeAsync(5000);
  await nextTick();
  expect(workers[1]!.terminate).toHaveBeenCalledOnce();
  expect(textOf(root)).toContain('preview unavailable');
  app.unmount();
});
