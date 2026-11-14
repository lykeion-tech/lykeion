import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createTreeApp, node } from '../../../../../tests/daemon-tree';
import { advanceAuthenticationEpoch } from '../../api/auth-loss';
import { workspaceId } from '../../api/session';
import AcceptInvite from './AcceptInvite.vue';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
afterEach(() => vi.unstubAllGlobals());

it.each([401, 200])(
  'ignores account A invitation response %s after account B takes over',
  async (status) => {
    const saved = new Map<string, string>();
    vi.stubGlobal('sessionStorage', {
      getItem: (key: string) => saved.get(key) ?? null,
      setItem: (key: string, value: string) => void saved.set(key, value),
    });
    const response = deferred<Response>();
    vi.stubGlobal(
      'fetch',
      vi.fn(() => response.promise),
    );
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
    });
    await router.push('/invite/account-a');
    await router.isReady();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const app = createTreeApp({ render: () => h(AcceptInvite, { token: 'account-a' }) });
    app.use(router);
    app.use(VueQueryPlugin, { queryClient: client });
    app.mount(node('root'));
    await nextTick();
    advanceAuthenticationEpoch();
    app.unmount();
    workspaceId.value = 'workspace-b';
    await router.replace('/account-b');
    response.resolve(
      status === 401
        ? Response.json({ error: 'old session' }, { status })
        : Response.json({ workspace_id: 'workspace-a' }),
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(workspaceId.value).toBe('workspace-b');
    expect(router.currentRoute.value.path).toBe('/account-b');
    expect(saved.get('lykeion.invite')).toBeUndefined();
    client.clear();
  },
);

it('ignores account A success when account B takes over during query invalidation', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => Response.json({ workspace_id: 'workspace-a' })),
  );
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push('/invite/account-a');
  await router.isReady();
  const client = new QueryClient();
  const invalidation = deferred<void>();
  const invalidate = vi.spyOn(client, 'invalidateQueries').mockReturnValue(invalidation.promise);
  const app = createTreeApp({ render: () => h(AcceptInvite, { token: 'account-a' }) });
  app.use(router);
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(node('root'));
  await vi.waitFor(() => expect(invalidate).toHaveBeenCalledOnce());
  advanceAuthenticationEpoch();
  app.unmount();
  workspaceId.value = 'workspace-b';
  await router.replace('/account-b');
  invalidation.resolve();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(workspaceId.value).toBe('workspace-b');
  expect(router.currentRoute.value.path).toBe('/account-b');
  client.clear();
});
