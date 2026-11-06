import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import ArtifactViewer from './ArtifactViewer.vue';
import CompressedFitsViewer from './CompressedFitsViewer.vue';
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
it.each(['a.fits.gz', 'A.FIT.GZ', 'a.fts.gz'])(
  'routes %s through the bounded worker without text reading',
  async (path) => {
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
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:fits');
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
    const blob = new Blob(['sentinel']),
      read = vi.spyOn(blob, 'text');
    const app = createTreeApp({ render: () => h(ArtifactViewer, { blob, path }) });
    const root = node('root');
    app.mount(root);
    await nextTick();
    await nextTick();
    expect(workers).toHaveLength(1);
    expect(workers[0]!.postMessage).toHaveBeenCalledWith({ blob });
    expect(read).not.toHaveBeenCalled();
    expect(textOf(root)).not.toContain('sentinel');
    app.unmount();
  },
);
it('terminates stale and timed-out gzip workers', async () => {
  vi.useFakeTimers();
  const workers: FakeWorker[] = [];
  class FakeWorker {
    onmessage: ((e: MessageEvent<any>) => void) | null = null;
    onerror = null;
    terminate = vi.fn();
    postMessage = vi.fn();
    constructor() {
      workers.push(this);
    }
  }
  vi.stubGlobal('Worker', FakeWorker);
  vi.stubGlobal('self', { location: new URL('http://localhost/') });
  const blob = ref(new Blob(['a']));
  const app = createTreeApp({ render: () => h(CompressedFitsViewer, { blob: blob.value }) });
  const root = node('root');
  app.mount(root);
  blob.value = new Blob(['b']);
  await nextTick();
  expect(workers[0]!.terminate).toHaveBeenCalledOnce();
  workers[0]!.onmessage?.({ data: { blob: new Blob(['stale']) } } as MessageEvent);
  await nextTick();
  expect(textOf(root)).not.toContain('stale');
  await vi.advanceTimersByTimeAsync(5000);
  await nextTick();
  expect(workers[1]!.terminate).toHaveBeenCalledOnce();
  expect(textOf(root)).toContain('preview unavailable');
  app.unmount();
});
