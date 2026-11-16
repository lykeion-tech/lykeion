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
  runtimeStatus: 'online' | 'offline' | 'unknown' = 'online',
  extra: {
    bases?: Record<string, string>;
    previousBases?: Record<string, string>;
    viewer?: string;
    owner?: string;
    workspace?: string;
  } = {},
) {
  workspaceId.value = 'w';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  });
  client.setQueryData(keys.me, { id: extra.viewer ?? 'owner' });
  client.setQueryData(keys.runtimes('w'), [
    {
      id: 'runtime',
      daemon_id: 'daemon',
      device_name: 'Bench computer',
      custom_name: null,
      owner_id: extra.owner ?? 'owner',
      workspace_id: extra.workspace ?? 'w',
      status: runtimeStatus,
      unhealthy_reason: unhealthyReason,
      harness: 'claude',
      cli_version: '1.0',
      active_runs: 0,
      last_seen_at: '2026-10-21T10:00:00Z',
      bases: { ...extra.bases, ...(sandbox ? { sandbox } : {}) },
    },
    ...(previous
      ? [
          {
            id: 'previous-runtime',
            daemon_id: 'daemon',
            device_name: 'Bench computer',
            custom_name: null,
            owner_id: 'owner',
            workspace_id: 'w',
            status: previous.status,
            harness: 'codex',
            cli_version: '1.0',
            active_runs: 0,
            last_seen_at: '2026-10-21T09:00:00Z',
            bases: { ...extra.previousBases, sandbox: previous.sandbox },
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
  it('explains supported CLIs and how to investigate runtime health', async () => {
    const html = await renderMachine(undefined, undefined, 'cli_missing');
    expect(html).toContain('Claude Code and Codex are supported');
    expect(html).toContain('Check daemon diagnostics below');
    expect(html).not.toContain('Add custom runtime');
  });
  it('directs offline machines to instructions on that computer', async () => {
    const html = await renderMachine(undefined, undefined, undefined, 'offline');
    expect(html).toContain('Check the daemon on this computer');
  });
  it('retains unknown runtime status', async () => {
    const html = await renderMachine(undefined, undefined, undefined, 'unknown');
    expect(html).toContain('Not reported');
  });

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

const unavailable =
  'failed: bounded scientific environment setup is unavailable on this machine; use an existing ready environment until a supported update is available';

describe('shared scientific base availability', () => {
  it('identifies unavailable bases while preserving other statuses', async () => {
    const html = await renderMachine(undefined, undefined, undefined, 'online', {
      bases: { python: 'ready', r: unavailable, julia: 'building' },
    });
    expect(html).toContain('Shared scientific setup for R is unavailable');
    expect(html).not.toContain('Shared scientific setup for Python');
  });
  it('names the fix the daemon reports', async () => {
    const html = await renderMachine(undefined, undefined, undefined, 'online', {
      bases: {
        python:
          'failed: bounded scientific environment setup is unavailable on this machine: install fuse2fs (packages fuse2fs, e2fsprogs and util-linux) so environments can be installed with a size limit',
      },
    });
    expect(html).toContain('Shared scientific setup for Python is unavailable');
    expect(html).toContain('To set up new ones, install fuse2fs');
    expect(html).not.toContain('a supported update is required');
  });
  it('omits the notice for ready, building, and unrelated failures', async () => {
    const html = await renderMachine(undefined, undefined, undefined, 'online', {
      bases: { python: 'ready', r: 'building', julia: 'failed: other error' },
    });
    expect(html).not.toContain('Shared scientific setup for');
  });
  it('ignores historical failures after readiness recovers', async () => {
    const html = await renderMachine(
      undefined,
      { sandbox: 'ready', status: 'offline' },
      undefined,
      'online',
      { bases: { python: 'ready' }, previousBases: { python: unavailable } },
    );
    expect(html).not.toContain('Shared scientific setup for');
  });
  it.each([
    ['offline', { bases: { python: unavailable } }, 'offline'],
    ['another viewer', { bases: { python: unavailable }, viewer: 'other' }, 'online'],
    ['another workspace', { bases: { python: unavailable }, workspace: 'other' }, 'online'],
  ] as const)('does not show %s reports', async (_label, extra, status) => {
    const html = await renderMachine(undefined, undefined, undefined, status, extra);
    expect(html).not.toContain('Shared scientific setup for');
  });
});

it('explains provider quota exhaustion on the machine', async () => {
  const html = await renderMachine('ready', undefined, 'provider_quota');
  expect(html).toContain('Provider usage limit reached');
  expect(html).not.toContain('provider_quota');
});
