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
vi.mock('./settings/CommunicationPanel.vue', () => ({
  default: { render: () => h('div', 'Slack panel') },
}));
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
it('excludes Colleagues from the settings rail and its search', async () => {
  const { root } = await mount();
  const search = all(root).find((n) => n.props['aria-label'] === 'Search settings')!;
  (search.props['onUpdate:modelValue'] as (v: string) => void)('colleagues');
  await nextTick();
  expect(all(root).some((n) => n.tag === 'a' && n.props.href === '/colleagues')).toBe(false);
  expect(textOf(root)).toContain('No matching settings.');
});
it('keeps profile information and usage analytics in distinct labelled sections', async () => {
  const { root } = await mount();
  expect(all(root).some((n) => n.tag === 'h3' && textOf(n) === 'Personal information')).toBe(true);
  expect(
    all(root).some((n) => n.tag === 'section' && n.props['aria-label'] === 'Usage analytics'),
  ).toBe(true);
});
it('preserves the existing settings section selector and excluded destinations', async () => {
  const { root } = await mount('updates');
  expect(all(root).some((n) => n.props['aria-label'] === 'Settings section')).toBe(true);
  expect(textOf(root)).toContain('Storage');
  expect(textOf(root)).toContain('Updates');
});
it('drops an unsaved workspace name when switching workspaces', async () => {
  const { root, client } = await mount('general');
  const field = () => all(root).find((n) => n.props['aria-label'] === 'Workspace name')!;
  expect(field().value).toBe('Lab');
  (field().props['onUpdate:modelValue'] as (v: string) => void)('Private draft');
  await nextTick();
  expect(field().value).toBe('Private draft');
  client.setQueryData(keys.workspace('w2'), { id: 'w2', name: 'Second lab', description: '' });
  workspaceId.value = 'w2';
  await nextTick();
  await nextTick();
  expect(field().value).toBe('Second lab');
});
it('keeps the narrow-screen selector within Settings without a Colleagues option', async () => {
  const { root, router } = await mount('storage');
  const active = all(root).find((n) => n.tag === 'button' && n.props['aria-current'] === 'page');
  expect(active && textOf(active)).toContain('Storage');
  const select = all(root).find((n) => n.props['aria-label'] === 'Settings section')!;
  expect(all(select).some((n) => n.tag === 'option' && n.props.value === 'colleagues')).toBe(false);
  (select.props.onChange as (e: unknown) => void)({ target: { value: 'skills' } });
  await router.isReady();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(router.currentRoute.value.fullPath).toBe('/settings?tab=skills');
});
it('groups Machines, Communication and Code under Connections on desktop and mobile', async () => {
  const { root } = await mount();
  const nav = all(root).find((n) => n.tag === 'nav' && n.props['aria-label'] === 'Settings')!;
  const groups = all(nav).filter((n) => n.tag === 'section');
  const connections = groups.find((group) =>
    all(group).some((n) => n.tag === 'strong' && textOf(n) === 'Connections'),
  );
  expect(connections).toBeDefined();
  const labels = ['Machines', 'Communication', 'Code'];
  expect(
    all(connections!)
      .filter((n) => n.tag === 'button')
      .map(textOf),
  ).toEqual(labels);
  const mobile = all(nav).find((n) => n.tag === 'optgroup' && n.props.label === 'Connections');
  expect(mobile).toBeDefined();
  expect(
    all(mobile!)
      .filter((n) => n.tag === 'option')
      .map(textOf),
  ).toEqual(labels);

  const search = all(nav).find((n) => n.props['aria-label'] === 'Search settings')!;
  (search.props['onUpdate:modelValue'] as (v: string) => void)('communication');
  await nextTick();
  expect(
    all(nav)
      .filter((n) => n.tag === 'button')
      .map(textOf),
  ).toEqual(['Communication']);
});

it('opens Communication with the workspace badge', async () => {
  const { root } = await mount('communication');
  const active = all(root).find((n) => n.tag === 'button' && n.props['aria-current'] === 'page');
  expect(active && textOf(active)).toContain('Communication');
  expect(all(root).some((n) => n.tag === 'h2' && textOf(n) === 'Communication')).toBe(true);
  expect(all(root).some((n) => n.props.class === 'scope-badge')).toBe(true);
  expect(textOf(root)).toContain('Slack panel');
});

it('opens Code in workspace settings with its scope badge', async () => {
  const { root } = await mount('code');
  expect(all(root).some((n) => n.tag === 'h2' && textOf(n) === 'Code')).toBe(true);
  expect(
    all(root).some((n) => n.props.class === 'scope-badge' && textOf(n).includes('Lab workspace')),
  ).toBe(true);
});
