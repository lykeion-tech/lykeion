import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref, ssrContextKey } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
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
async function mount(section = 'profile') {
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
it('lists only supported shortcuts on a dedicated settings page', async () => {
  const { root } = await mount('shortcuts');
  const text = textOf(root);
  expect(text).toContain('Keyboard shortcuts');
  expect(text).toContain('Search');
  expect(text).toContain('Toggle sidebar');
  expect(text).toContain('New item');
  expect(text).toContain('New workspace tab');
  expect(text).toContain('Close workspace tab');
  expect(text).toContain('Workspace tab history');
  expect(text).toContain('Switch workspace tab');
  expect(text).toContain('Aristotle model menu');
  expect(text).not.toContain('Change shortcut');
});
it('keeps the timezone and theme controls on preferences', async () => {
  const { root } = await mount('preferences');
  const text = textOf(root);
  expect(text).toContain('Time zone');
  expect(text).toContain('Account');
  expect(text).toContain('This device only');
  expect(text).toContain('Ash');
});
