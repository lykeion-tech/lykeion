import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { createMemoryHistory, createRouter, RouterView } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import WorkspaceTabs from './WorkspaceTabs.vue';
import { accountViewBlocked, useWorkspaceTabs } from '../../state/workspace-tabs';
import AcceptInvite from './AcceptInvite.vue';

const invitation = vi.hoisted(() => vi.fn(() => new Promise(() => {})));
vi.mock('../../api/client', async (original) => ({
  ...(await original<typeof import('../../api/client')>()),
  api: invitation,
}));

afterEach(() => vi.unstubAllGlobals());

it('removes private titles safely while the mounted strip changes accounts', async () => {
  vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal('document', { querySelector: () => null });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push('/studies');
  await router.isReady();
  const state = useWorkspaceTabs();
  state.clearAccount();
  state.setAccount('account-a');
  const root = node('root');
  const app = createTreeApp({
    render: () => h(WorkspaceTabs, { title: 'Studies', icon: 'tasks', collapsed: false }),
  });
  app.use(router);
  app.mount(root);
  state.openTab('/studies/private', 'Private A', 'tasks');
  await nextTick();
  expect(textOf(root)).toContain('Private A');
  state.setAccount('account-b');
  await nextTick();
  expect(textOf(root)).not.toContain('Private A');
  app.unmount();
});

it('does not accept a private invitation until identity and transition are verified', async () => {
  invitation.mockClear();
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/invite/:token', component: AcceptInvite, props: true }],
  });
  await router.push('/invite/private-token');
  await router.isReady();
  const id = ref<string>();
  const switching = ref(true);
  const app = createTreeApp({
    render: () =>
      accountViewBlocked(false, id.value, switching.value) ? h('div', 'Verifying') : h(RouterView),
  });
  app.use(router);
  app.use(VueQueryPlugin, { queryClient: new QueryClient() });
  app.mount(node('root'));
  await nextTick();
  expect(invitation).not.toHaveBeenCalled();
  id.value = 'account-b';
  await nextTick();
  expect(invitation).not.toHaveBeenCalled();
  switching.value = false;
  await nextTick();
  expect(invitation).toHaveBeenCalledOnce();
  app.unmount();
});
