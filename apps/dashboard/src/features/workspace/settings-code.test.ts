import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import CodePanel from './settings/CodePanel.vue';
import { workspaceId } from '../../api/session';
import { keys } from '../../api/keys';

const call = vi.fn();
vi.mock('../../api/client', () => ({
  api: (...args: unknown[]) => call(...args),
  ApiError: Error,
}));
const cleanup: (() => void)[] = [];
afterEach(() => {
  cleanup.splice(0).forEach((fn) => fn());
  workspaceId.value = '';
  vi.unstubAllGlobals();
  call.mockReset();
});
const connection = {
  installation_id: 42,
  account_login: 'lab',
  connected_by: 'u',
  status: 'suspended',
  import_state: 'running',
};
async function mount(
  role = 'owner',
  configured = true,
  connections: (typeof connection)[] = [],
  query = '',
) {
  workspaceId.value = 'w';
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/settings/code', component: CodePanel }],
  });
  await router.push('/settings/code' + query);
  const client = new QueryClient({
    defaultOptions: { queries: { staleTime: Infinity, retry: false } },
  });
  client.setQueryData(keys.workspaces, [{ id: 'w', role }]);
  client.setQueryData(keys.workspace('w'), { id: 'w', display_prefix: 'LAB' });
  client.setQueryData(keys.members('w'), [{ user_id: 'u', name: 'Ada' }]);
  client.setQueryData(['ws', 'w', 'github'], { configured, auto_link: true, connections });
  call.mockResolvedValue({ configured, auto_link: false, connections });
  const app = createTreeApp({ render: () => h(CodePanel) });
  app.use(VueQueryPlugin, { queryClient: client });
  app.use(router);
  const root = node('root');
  app.mount(root);
  cleanup.push(() => {
    app.unmount();
    client.clear();
  });
  await nextTick();
  return { root, router };
}
it('shows one GitHub row and lets an owner change auto-link', async () => {
  const { root } = await mount();
  expect(all(root).filter((n) => n.props['data-app'] === 'github')).toHaveLength(1);
  expect(textOf(root)).toContain('Not connected');
  expect(textOf(root)).toContain('Connect GitHub');
  expect(textOf(root)).toContain('Closes LAB-12.');
  const toggle = all(root).find((n) => n.props.role === 'switch')!;
  await (toggle.props.onClick as () => Promise<void>)();
  expect(
    call.mock.calls.some(
      ([method, path, body]) =>
        method === 'PATCH' && path === '/api/github' && body.auto_link === false,
    ),
  ).toBe(true);
});
it('shows members connection details and disables editing', async () => {
  const { root } = await mount('member', true, [connection]);
  for (const label of [
    'Connected · lab · by Ada',
    'Importing open pull requests…',
    'Suspended on GitHub',
  ])
    expect(textOf(root)).toContain(label);
  expect(textOf(root)).not.toContain('Disconnect');
  expect(textOf(root)).not.toContain('Add account or organization');
  expect(all(root).find((n) => n.props.role === 'switch')!.props.disabled).toBe(true);
});
it('shows the server configuration and removes callback parameters after display', async () => {
  const { root, router } = await mount('owner', false, [], '?github_error=Exact%20error%21&keep=1');
  expect(textOf(root)).toContain("GitHub isn't configured on this server.");
  expect(textOf(root)).toContain('Exact error!');
  expect(textOf(root)).not.toContain('Connect GitHub');
  await vi.waitFor(() => expect(router.currentRoute.value.query).toEqual({ keep: '1' }));
});
it('uses a trusted continuation link on desktop and confirms disconnect', async () => {
  vi.stubGlobal('window', { lykeionDesktop: {} });
  const { root } = await mount('owner', true, [connection]);
  const click = (label: string) =>
    (
      all(root).find((n) => n.tag === 'button' && textOf(n).trim() === label)!.props
        .onClick as () => Promise<void>
    )();
  const url = 'https://github.com/apps/lab/installations/new?state=test';
  call.mockResolvedValueOnce({ url });
  await click('Add account or organization');
  expect(all(root).find((n) => n.tag === 'a')?.props.href).toBe(url);
  vi.stubGlobal(
    'confirm',
    vi.fn(() => false),
  );
  await click('Disconnect');
  expect(call.mock.calls.some(([method]) => method === 'DELETE')).toBe(false);
  vi.stubGlobal(
    'confirm',
    vi.fn(() => true),
  );
  await click('Disconnect');
  expect(
    call.mock.calls.some(
      ([method, path]) => method === 'DELETE' && path === '/api/github/connections/42',
    ),
  ).toBe(true);
});

it('discards a pending connect when the owner moves to another workspace', async () => {
  vi.stubGlobal('window', { lykeionDesktop: {} });
  const { root } = await mount();
  let resolve!: (value: { url: string }) => void;
  call.mockReturnValueOnce(
    new Promise((r) => {
      resolve = r;
    }),
  );
  const click = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Connect GitHub')!;
  const connecting = (click.props.onClick as () => Promise<void>)();
  workspaceId.value = 'other';
  workspaceId.value = 'w';
  resolve({ url: 'https://github.com/apps/lab/installations/new?state=stale' });
  await connecting;
  await nextTick();
  expect(all(root).some((n) => n.tag === 'a')).toBe(false);
});
it('shows the success notice and cleans the callback URL', async () => {
  const { root, router } = await mount('owner', true, [], '?github=connected');
  expect(textOf(root)).toContain('GitHub connected.');
  await vi.waitFor(() => expect(router.currentRoute.value.query).toEqual({}));
});
