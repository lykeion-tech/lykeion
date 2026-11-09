import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createMemoryHistory, createRouter } from 'vue-router';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import { workspaceId } from '../../api/session';
import { keys } from '../../api/keys';
import CoresearchFileView from './CoresearchFileView.vue';

it('opens a selected historical file in a tab with version and provenance controls', async () => {
  vi.stubGlobal('document', { activeElement: null });
  vi.stubGlobal('Document', class {});
  vi.stubGlobal('ShadowRoot', class {});
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:controlled');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  workspaceId.value = 'lab';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  const v1 = { n: 1, size: 3, sha256: '1', created_at: '2026-01-01' };
  const v2 = { n: 2, size: 3, sha256: '2', created_at: '2026-01-02' };
  client.setQueryData(keys.coresearchProject('lab', 'p'), { id: 'p', name: 'Project' });
  client.setQueryData(keys.coresearchFiles('lab', 'p'), [
    { id: 'f', path: 'results.txt', head: v2 },
  ]);
  client.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [v2, v1]);
  client.setQueryData(keys.coresearchContent('lab', 'p', 'f', 1), new Blob(['old']));
  client.setQueryData(keys.coresearchContent('lab', 'p', 'f', 2), new Blob(['new']));
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/file', component: { render: () => null } }],
  });
  await router.push('/file?version=1');
  const app = createTreeApp({
    render: () => h(CoresearchFileView, { projectId: 'p', fileId: 'f' }),
  });
  app.use(VueQueryPlugin, { queryClient: client });
  app.use(router);
  const root = node('root');
  app.mount(root);
  try {
    await vi.waitFor(async () => {
      await nextTick();
      expect(textOf(root)).toContain('v1 (old)');
    });
    for (const label of ['Preview', 'Code', 'Execution Log', 'Transcript']) {
      expect(all(root).some((n) => n.props.role === 'tab' && textOf(n).trim() === label)).toBe(
        true,
      );
    }
    expect(all(root).some((n) => n.props['aria-label'] === 'Open file in tab')).toBe(false);
    for (const action of ['Edit content', 'Restore this version'])
      expect(all(root).some((n) => n.tag === 'button' && textOf(n).trim() === action)).toBe(false);
    expect(all(root).some((n) => n.tag === 'textarea')).toBe(false);
    expect(all(root).some((n) => String(n.props.class).split(/\s+/).includes('coresearch'))).toBe(
      true,
    );
    const next = all(root).find((n) => n.props['aria-label'] === 'Next version')!;
    (next.props.onClick as () => void)();
    await vi.waitFor(() => expect(router.currentRoute.value.query.version).toBeUndefined());
    const previous = all(root).find((n) => n.props['aria-label'] === 'Previous version')!;
    (previous.props.onClick as () => void)();
    await vi.waitFor(() => expect(router.currentRoute.value.query.version).toBe('1'));
  } finally {
    app.unmount();
    client.clear();
  }
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
