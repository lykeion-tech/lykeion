import { afterEach, describe, expect, it } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from '@vue/server-renderer';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import MachinesView from '../apps/dashboard/src/views/MachinesView.vue';
import { workspaceId } from '../apps/dashboard/src/api/session';
import { keys } from '../apps/dashboard/src/api/keys';

async function renderMachine(sandbox?: string) {
  workspaceId.value = 'w';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  });
  client.setQueryData(keys.runtimes('w'), [
    {
      id: 'runtime',
      daemon_id: 'daemon',
      device_name: 'Bench computer',
      custom_name: null,
      owner_id: 'owner',
      status: 'online',
      harness: 'claude',
      cli_version: '1.0',
      active_runs: 0,
      last_seen_at: '2026-10-21T10:00:00Z',
      bases: sandbox ? { sandbox } : {},
    },
  ]);
  client.setQueryData(keys.members('w'), []);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/machines/:id?', component: MachinesView }],
  });
  await router.push('/machines/daemon');
  await router.isReady();
  const app = createSSRApp({ render: () => h(MachinesView, { machineId: 'daemon' }) });
  app.use(VueQueryPlugin, { queryClient: client });
  app.use(router);
  try {
    return await renderToString(app);
  } finally {
    client.clear();
  }
}

afterEach(() => {
  workspaceId.value = '';
});

describe('machine sandbox readiness', () => {
  it('renders the daemon failure reason in the active machine detail', async () => {
    const html = await renderMachine('failed: install bubblewrap');
    expect(html).toContain('Bench computer');
    expect(html).toContain('Can&#39;t run agents: install bubblewrap');
    expect(html).toContain('role="alert"');
  });
  it.each(['ready', undefined])('shows no failure when sandbox status is %s', async (sandbox) => {
    expect(await renderMachine(sandbox)).not.toContain('run agents:');
  });
});
