import { readFileSync } from 'node:fs';
import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import LabChartView from './LabChartView.vue';
import { workspaceId } from '../../api/session';
import { keys } from '../../api/keys';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';

const request = vi.hoisted(() => vi.fn());
vi.mock('../../api/client', () => ({ api: request, ApiError: class extends Error {} }));
vi.mock('vue-router', async (importOriginal) => {
  const original = await importOriginal<typeof import('vue-router')>();
  return { ...original, createWebHashHistory: original.createMemoryHistory };
});
vi.mock('lottie-web/build/player/lottie_light', () => ({
  default: { loadAnimation: () => ({ destroy() {} }) },
}));
vi.mock('./LabChartScene.vue', async () => {
  const { h } = await import('vue');
  return { default: { props: ['chart', 'me'], setup: () => () => h('div', 'scene') } };
});

const apps: { unmount(): void }[] = [];
afterEach(() => {
  apps.splice(0).forEach((a) => a.unmount());
  workspaceId.value = '';
  request.mockReset();
});

const settle = async () => {
  for (let i = 0; i < 4; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await nextTick();
  }
};

async function mount() {
  workspaceId.value = 'w';
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(keys.me, { id: 'u1' });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: LabChartView }],
  });
  await router.push('/');
  const app = createTreeApp({ render: () => h(LabChartView) });
  app.use(VueQueryPlugin, { queryClient: client });
  app.use(router);
  const root = node('root');
  app.mount(root);
  apps.push(app);
  return root;
}

it('shows loading, then the server error with a retry, then the chart', async () => {
  let fail!: (e: Error) => void;
  let chart: Promise<unknown> = new Promise((_, reject) => (fail = reject));
  request.mockImplementation((_m: string, path: string) =>
    path === '/api/lab-chart' ? chart : Promise.resolve({ id: 'u1' }),
  );
  const root = await mount();
  await nextTick();
  expect(textOf(root)).toContain('Loading the Lab Chart…');
  fail(new Error('the lab chart is unavailable'));
  await settle();
  expect(textOf(root)).toContain('the lab chart is unavailable');
  expect(textOf(root)).not.toContain('scene');
  chart = Promise.resolve({ rooms: [], people: [], ticker: null });
  const retry = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Retry');
  (retry!.props.onClick as () => void)();
  await settle();
  expect(request.mock.calls.filter((c) => c[1] === '/api/lab-chart')).toHaveLength(2);
  expect(textOf(root)).toContain('scene');
  expect(textOf(root)).not.toContain('unavailable');
});

it('takes the shell page header, as Agents does, titled Lab Chart', async () => {
  const { router } = await import('../../router');
  const lab = router.resolve('/lab-chart').meta;
  const agents = router.resolve('/agents').meta;
  expect(lab).toEqual({ page: 'lab-chart', title: 'Lab Chart' });
  expect(Object.keys(lab).sort()).toEqual(Object.keys(agents).sort());
  // The shell draws its header for every page whose route does not bring its own.
  const app = readFileSync('apps/dashboard/src/App.vue', 'utf8');
  expect(app).toMatch(
    /<header\s+v-if="current !== 'settings' && !route\.meta\.ownHeader"\s+class="page-header"/,
  );
  expect(app).toContain("(route.meta.title ?? 'Studies')");
});
