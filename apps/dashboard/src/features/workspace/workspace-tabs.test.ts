import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import WorkspaceTabs from './WorkspaceTabs.vue';
import { useWorkspaceTabs } from '../../state/workspace-tabs';

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
