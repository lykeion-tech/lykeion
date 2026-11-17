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
async function mount(section = 'profile', tokenRows: unknown[] = []) {
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
  client.setQueryData(keys.tokens, tokenRows);
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
it('explains account access, expiry, and revocation', async () => {
  const { root } = await mount('tokens');
  expect(textOf(root)).toContain('access your account');
  expect(textOf(root)).toContain('90 days');
  expect(textOf(root)).toContain('stop working');
});
it('reports a failed token creation without exposing a token', async () => {
  const { root } = await mount('tokens');
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => Response.json({ error: 'Could not create token' }, { status: 500 })),
  );
  const input = all(root).find((n) => n.props['aria-label'] === 'Token name')!;
  (input.props['onUpdate:modelValue'] as (v: string) => void)('test');
  await nextTick();
  const form = all(root).find((n) => n.tag === 'form' && n.props.class === 'token-creation-form')!;
  (form.props.onSubmit as (e: unknown) => void)({ preventDefault() {} });
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
  expect(textOf(root)).toContain('Could not create token');
  expect(textOf(root)).not.toContain('Copy it now');
});
it('reports a failed revocation and keeps the token listed', async () => {
  const { root } = await mount('tokens', [
    {
      id: 't1',
      name: 'Test client',
      created_at: '2026-01-01',
      expires_at: '2099-04-01',
      revoked_at: null,
    },
  ]);
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => Response.json({ error: 'Could not revoke token' }, { status: 500 })),
  );
  await nextTick();
  const revoke = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Revoke')!;
  (revoke.props.onClick as () => void)();
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
  expect(textOf(root)).toContain('Could not revoke token');
  expect(textOf(root)).toContain('Test client');
});
it('hides a newly created token after leaving the page', async () => {
  const { root, props } = await mount('tokens');
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init?: RequestInit) =>
      init?.method === 'POST'
        ? Response.json({ token: 'lyk_disposable_fixture', id: 't1', name: 'test' })
        : Response.json([]),
    ),
  );
  const input = all(root).find((n) => n.props['aria-label'] === 'Token name')!;
  (input.props['onUpdate:modelValue'] as (v: string) => void)('test');
  await nextTick();
  const form = all(root).find((n) => n.tag === 'form' && n.props.class === 'token-creation-form')!;
  (form.props.onSubmit as (e: unknown) => void)({ preventDefault() {} });
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
  expect(textOf(root)).toContain('lyk_disposable_fixture');
  props.value = 'profile';
  await nextTick();
  props.value = 'tokens';
  await nextTick();
  expect(textOf(root)).not.toContain('lyk_disposable_fixture');
});
it('saves a TypeSafe key once and then only says it is saved', async () => {
  const { root, client } = await mount('tokens');
  expect(textOf(root)).toContain('approve each stage yourself');
  const calls: Array<[string, RequestInit | undefined]> = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push([url, init]);
      return init?.method === 'PUT' ? new Response(null, { status: 204 }) : Response.json([]);
    }),
  );
  const input = all(root).find((n) => n.props['aria-label'] === 'TypeSafe key')!;
  expect(input.props.type).toBe('password');
  (input.props['onUpdate:modelValue'] as (v: string) => void)(' ts-fixture-key ');
  await nextTick();
  const form = all(root).find(
    (n) => n.tag === 'form' && n.props.class === 'setting-box typesafe-key',
  )!;
  (form.props.onSubmit as (e: unknown) => void)({ preventDefault() {} });
  await new Promise((resolve) => setTimeout(resolve, 0));
  const put = calls.find(([, init]) => init?.method === 'PUT')!;
  expect(put[0]).toContain('/api/me/typesafe-key');
  expect(JSON.parse(String(put[1]!.body))).toEqual({ key: 'ts-fixture-key' });
  client.setQueryData(keys.me, {
    id: 'u',
    name: 'Researcher',
    email: 'r@example.test',
    typesafe_key_set: true,
  });
  await nextTick();
  expect(textOf(root)).toContain('Key saved');
  expect(textOf(root)).not.toContain('ts-fixture-key');
  expect(all(root).some((n) => n.props['aria-label'] === 'TypeSafe key')).toBe(false);
});
