import { afterEach, describe, expect, it } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from '@vue/server-renderer';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import MachinesView from './MachinesView.vue';
import { workspaceId } from '../../api/session';
import { keys } from '../../api/keys';

async function renderMachine(
  sandbox?: string,
  previous?: { sandbox: string; status: 'online' | 'offline' },
  unhealthyReason?: string,
  runtimeStatus: 'online' | 'offline' = 'online',
) {
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
      status: runtimeStatus,
      unhealthy_reason: unhealthyReason,
      harness: 'claude',
      cli_version: '1.0',
      active_runs: 0,
      last_seen_at: '2026-10-21T10:00:00Z',
      bases: sandbox ? { sandbox } : {},
    },
    ...(previous
      ? [
          {
            id: 'previous-runtime',
            daemon_id: 'daemon',
            device_name: 'Bench computer',
            custom_name: null,
            owner_id: 'owner',
            status: previous.status,
            harness: 'codex',
            cli_version: '1.0',
            active_runs: 0,
            last_seen_at: '2026-10-21T09:00:00Z',
            bases: { sandbox: previous.sandbox },
          },
        ]
      : []),
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
  it.each(['online', 'offline'] as const)(
    'ignores an older failed %s runtime after the current machine reports ready',
    async (status) => {
      const html = await renderMachine('ready', { sandbox: 'failed: install bubblewrap', status });
      expect(html).not.toContain('run agents:');
    },
  );
  it('preserves a current failure even when an older runtime reports ready', async () => {
    const html = await renderMachine('failed: install bubblewrap', {
      sandbox: 'ready',
      status: 'offline',
    });
    expect(html).toContain('Can&#39;t run agents: install bubblewrap');
  });
  it('does not substitute a historical failure for an unreported current sandbox', async () => {
    const html = await renderMachine(undefined, {
      sandbox: 'failed: install bubblewrap',
      status: 'offline',
    });
    expect(html).not.toContain('run agents:');
  });
  it.each(['ready', undefined])('shows no failure when sandbox status is %s', async (sandbox) => {
    expect(await renderMachine(sandbox)).not.toContain('run agents:');
  });
});

describe('runtime readiness', () => {
  it('retains offline status when a runtime also has a preflight failure', async () => {
    const html = await renderMachine(undefined, undefined, 'cli_missing', 'offline');
    expect(html).toContain('Offline · Needs attention');
  });
  it('shows a failed CLI preflight instead of presenting an online runtime as healthy', async () => {
    const html = await renderMachine(undefined, undefined, 'cli_missing');
    expect(html).toContain('Needs attention');
    expect(html).toContain('Agent CLI is missing');
  });
});
