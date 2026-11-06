import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { all, createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import NiftiViewer from './NiftiViewer.vue';
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it('disables the old slice control while a new axis loads', async () => {
  const workers: FakeWorker[] = [];
  class FakeWorker {
    onmessage: ((event: unknown) => void) | null = null;
    onerror = null;
    terminate = vi.fn();
    postMessage = vi.fn();
    constructor() {
      workers.push(this);
    }
  }
  vi.stubGlobal('Worker', FakeWorker);
  vi.stubGlobal('self', { location: new URL('http://localhost/') });
  const app = createTreeApp({
    render: () => h(NiftiViewer, { blob: new Blob(['file']), compressed: false }),
  });
  const root = node('root');
  app.mount(root);
  workers[0]!.onmessage!({
    data: {
      result: {
        width: 1,
        height: 1,
        sliceCount: 8,
        anatomical: true,
        plane: 'Axial',
        aspect: 1,
        spacingApplied: true,
        rgba: new Uint8ClampedArray([0, 0, 0, 255]),
      },
    },
  });
  await nextTick();
  const select = all(root).find((n) => n.tag === 'select')!;
  (select.props.onChange as (e: unknown) => void)({ target: { value: '0' } });
  await nextTick();
  const slice = all(root).find((n) => n.tag === 'input')!;
  expect(slice.props.disabled).toBe(true);
  app.unmount();
});
it('terminates stale or timed-out NIfTI workers without exposing errors', async () => {
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
  const app = createTreeApp({
    render: () => h(NiftiViewer, { blob: file.value, compressed: false }),
  });
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
