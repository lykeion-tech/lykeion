import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import SmilesViewer from './SmilesViewer.vue';
const mock = vi.hoisted(() => ({
  workers: [] as Array<{
    onmessage: ((event: { data: string }) => void) | null;
    onerror: (() => void) | null;
    terminate: ReturnType<typeof vi.fn>;
    postMessage: ReturnType<typeof vi.fn>;
  }>,
}));
vi.mock('./smiles-worker-factory', () => ({
  createSmilesWorker: () => {
    const w = { onmessage: null, onerror: null, terminate: vi.fn(), postMessage: vi.fn() };
    mock.workers.push(w);
    return w;
  },
}));

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
it('terminates stale workers and revokes image URLs on file switch and unmount', async () => {
  mock.workers.length = 0;
  const workers = mock.workers;
  vi.stubGlobal('Worker', class {});
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:smiles');
  const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const text = ref('CCO');
  const app = createTreeApp({
    render: () => h(SmilesViewer, { text: text.value, partial: false }),
  });
  const root = node('root');
  app.mount(root);
  text.value = 'C';
  await nextTick();
  expect(workers[0]!.terminate).toHaveBeenCalled();
  workers[0]!.onmessage?.({ data: '<svg></svg>' });
  await nextTick();
  expect(textOf(root)).toContain('Drawing molecule');
  workers[1]!.onmessage?.({ data: '<svg></svg>' });
  await nextTick();
  app.unmount();
  expect(revoke).toHaveBeenCalledWith('blob:smiles');
});

it('terminates a worker that does not respond', async () => {
  mock.workers.length = 0;
  vi.stubGlobal('Worker', class {});
  vi.useFakeTimers();
  try {
    const app = createTreeApp({ render: () => h(SmilesViewer, { text: 'CCO', partial: false }) });
    const root = node('root');
    app.mount(root);
    await vi.advanceTimersByTimeAsync(10001);
    expect(mock.workers[0]!.terminate).toHaveBeenCalled();
    expect(textOf(root)).toContain('Preview unavailable');
    app.unmount();
  } finally {
    vi.useRealTimers();
  }
});
