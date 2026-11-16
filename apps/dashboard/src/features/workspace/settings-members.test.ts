import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref, ssrContextKey } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import SettingsView from './SettingsView.vue';
import { workspaceId } from '../../api/session';
import { keys } from '../../api/keys';

vi.mock('../machines/MachinesView.vue', () => ({ default: { render: () => h('div') } }));
vi.mock('./settings/StoragePanel.vue', () => ({ default: { render: () => h('div') } }));
vi.mock('./settings/library/SkillPanel.vue', () => ({ default: { render: () => h('div') } }));
vi.mock('./settings/library/ConnectorPanel.vue', () => ({ default: { render: () => h('div') } }));
vi.mock('./settings/AnalyticsPanel.vue', () => ({
  default: { render: () => h('div', 'Usage analytics') },
}));
const apps: Array<() => void> = [];
afterEach(() => {
  apps.splice(0).forEach((unmount) => unmount());
  workspaceId.value = '';
  vi.unstubAllGlobals();
});
async function mount(section = 'profile', role = 'owner') {
  vi.stubGlobal('document', { addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal('Document', class {});
  vi.stubGlobal('ShadowRoot', class {});
  vi.stubGlobal('window', { innerWidth: 1000, addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => Response.json([])),
  );
  workspaceId.value = 'w';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  client.setQueryData(keys.workspaces, [{ id: 'w', name: 'Lab', role }]);
  client.setQueryData(keys.me, { id: 'u', name: 'Researcher', email: 'r@example.test' });
  client.setQueryData(keys.workspace('w'), { id: 'w', name: 'Lab', description: '' });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:path(.*)*', component: { render: () => null } }],
  });
  await router.push('/settings');
  const props = ref(section);
  const app = createTreeApp({ render: () => h(SettingsView, { section: props.value }) });
  app.use(VueQueryPlugin, { queryClient: client });
  app.use(router);
  app.provide(ssrContextKey, {});
  const root = node('root');
  app.mount(root);
  await nextTick();
  apps.push(() => {
    app.unmount();
    client.clear();
  });
  return { root, client, router, props };
}
it('links workspace members and invitations from General settings', async () => {
  const { root } = await mount('general');
  expect(textOf(root)).toContain('Members and invitations');
  expect(
    all(root).some(
      (n) =>
        n.tag === 'a' && n.props.href === '/colleagues' && textOf(n).includes('Manage colleagues'),
    ),
  ).toBe(true);
});
it('keeps workspace editing read-only for members', async () => {
  const { root } = await mount('general', 'member');
  const field = all(root).find((n) => n.props['aria-label'] === 'Workspace name')!;
  expect(field.props.disabled).toBe(true);
  expect(textOf(root)).not.toContain('Delete workspace');
});
