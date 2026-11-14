import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import HeicViewer from './HeicViewer.vue';
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it('terminates stale or timed-out decoding workers without showing file contents', async () => {
  vi.useFakeTimers();
  const workers: FakeWorker[] = [];
  class FakeWorker {
    onmessage = null;
    onerror = null;
    postMessage = vi.fn();
    terminate = vi.fn();
    constructor() {
      workers.push(this);
    }
  }
  vi.stubGlobal('Worker', FakeWorker);
  vi.stubGlobal('self', { location: new URL('http://localhost/') });
  const blob = ref(new Blob(['private patient data']));
  const root = node('root');
  const app = createTreeApp({ render: () => h(HeicViewer, { blob: blob.value }) });
  app.mount(root);
  blob.value = new Blob(['replacement']);
  await nextTick();
  expect(workers[0]!.terminate).toHaveBeenCalledOnce();
  await vi.advanceTimersByTimeAsync(10000);
  await nextTick();
  expect(workers[1]!.terminate).toHaveBeenCalledOnce();
  expect(textOf(root)).toContain('preview unavailable');
  expect(textOf(root)).not.toContain('private patient data');
  app.unmount();
});

it('routes HEIC files to the binary viewer without reading metadata as text', async () => {
  const workers: FakeWorker[] = [];
  class FakeWorker {
    onmessage = null;
    onerror = null;
    postMessage = vi.fn();
    terminate = vi.fn();
    constructor() {
      workers.push(this);
    }
  }
  vi.stubGlobal('Worker', FakeWorker);
  vi.stubGlobal('self', { location: new URL('http://localhost/') });
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:heic');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const { default: ArtifactViewer } = await import('./ArtifactViewer.vue');
  const blob = new Blob(['private patient metadata']);
  const text = vi.spyOn(blob, 'text');
  const root = node('root');
  const app = createTreeApp({ render: () => h(ArtifactViewer, { blob, path: 'scan.HEIC' }) });
  app.mount(root);
  await nextTick();
  await nextTick();
  expect(workers).toHaveLength(1);
  expect(workers[0]!.postMessage).toHaveBeenCalledWith({ blob });
  expect(text).not.toHaveBeenCalled();
  expect(textOf(root)).not.toContain('private patient metadata');
  app.unmount();
  vi.restoreAllMocks();
});
