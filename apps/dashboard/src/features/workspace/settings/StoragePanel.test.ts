import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { renderToString } from '@vue/server-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { createSSRApp } from 'vue';
import { keys } from '../../../api/keys';
import { workspaceId } from '../../../api/session';
import type { StorageMachine, StorageReport } from '../../../api/types';
import StoragePanel from './StoragePanel.vue';

afterEach(() => {
  workspaceId.value = '';
  vi.unstubAllGlobals();
});

it.each([
  ['web', undefined, 'Open the Lykeion desktop app on this machine to check local storage.'],
  ['desktop', {}, 'This machine has not reported storage yet.'],
])('explains missing storage on %s and shows reported storage', async (_name, bridge, notice) => {
  vi.stubGlobal('window', { lykeionDesktop: bridge });
  workspaceId.value = 'workspace';
  const client = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity } } });
  const time = '2026-11-22T08:00:00Z';
  const machine: StorageMachine = {
    daemon_id: 'pending',
    name: 'Machine without a report',
    online: true,
    last_seen_at: time,
    snapshot: null,
  };
  client.setQueryData(keys.me, { id: 'owner' });
  client.setQueryData<StorageReport>([...keys.storage('workspace'), 'owner'], {
    machines: [
      machine,
      {
        ...machine,
        daemon_id: 'reported',
        name: 'Machine with a report',
        snapshot: {
          measured_at: time,
          received_at: time,
          categories: [{ key: 'state', path: '/state', bytes: 0, complete: true }],
          filesystems: [],
        },
      },
    ],
  });
  const app = createSSRApp(StoragePanel);
  app.use(VueQueryPlugin, { queryClient: client });
  const html = await renderToString(app);
  client.clear();
  expect(html).toContain(notice);
  expect(html).toContain('Total measured');
});
