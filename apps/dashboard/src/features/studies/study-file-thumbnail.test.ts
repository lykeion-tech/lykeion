import { afterEach, expect, it, vi } from 'vitest';
import { h } from 'vue';
import StudyFileThumbnail from './StudyFileThumbnail.vue';
import { workspaceId } from '../../api/session';
import { createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';

afterEach(() => vi.unstubAllGlobals());
it('shows small CSV column names and exact dimensions in a thumbnail', async () => {
  workspaceId.value = 'w';
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(new Blob(['name,value\na,1\nb,2']), { status: 200 })),
  );
  const root = node('root');
  const app = createTreeApp({
    render: () =>
      h(StudyFileThumbnail, { name: 'results.csv', size: 20, url: '/api/cards/c/outputs/o1' }),
  });
  app.mount(root);
  await vi.waitFor(() => expect(textOf(root)).toContain('2 rows · 2 columns'));
  expect(textOf(root)).toContain('name');
  expect(textOf(root)).toContain('value');
  app.unmount();
});
it('does not fetch large files just to draw thumbnails', async () => {
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  const root = node('root');
  const app = createTreeApp({
    render: () =>
      h(StudyFileThumbnail, { name: 'large.csv', size: 10000000, url: '/api/cards/c/outputs/o1' }),
  });
  app.mount(root);
  expect(fetch).not.toHaveBeenCalled();
  app.unmount();
});

it('loads a thumbnail only after it becomes visible and aborts on unmount', async () => {
  let show!: () => void;
  vi.stubGlobal(
    'IntersectionObserver',
    class {
      constructor(callback: (entries: { isIntersecting: boolean }[]) => void) {
        show = () => callback([{ isIntersecting: true }]);
      }
      observe() {}
      disconnect() {}
    },
  );
  const fetch = vi.fn(
    (_url: string, init: RequestInit) =>
      new Promise<Response>((_, reject) => {
        init.signal?.addEventListener('abort', () => reject(new Error('aborted')));
      }),
  );
  vi.stubGlobal('fetch', fetch);
  workspaceId.value = 'w';
  const root = node('root');
  const app = createTreeApp({
    render: () =>
      h(StudyFileThumbnail, { name: 'data.csv', size: 10, url: '/api/cards/c/outputs/o1' }),
  });
  app.mount(root);
  expect(fetch).not.toHaveBeenCalled();
  show();
  expect(fetch).toHaveBeenCalledOnce();
  const signal = fetch.mock.calls[0]![1].signal!;
  app.unmount();
  expect(signal.aborted).toBe(true);
});
