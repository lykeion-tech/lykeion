import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import OnboardingView from './OnboardingView.vue';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';

const calls = vi.hoisted(() => [] as { method: string; path: string; body: unknown }[]);
vi.mock('lottie-web/build/player/lottie_light', () => ({
  default: { loadAnimation: () => ({ destroy() {} }) },
}));
vi.mock('../../api/client', () => ({
  api: vi.fn(async (method: string, path: string, body: unknown) => {
    calls.push({ method, path, body });
    return method === 'POST' ? { id: 'new-lab', name: 'x', slug: 'x' } : [];
  }),
}));
vi.mock('../lab-chart/LabChartScene.vue', () => ({
  default: {
    props: { chart: Object, preview: Boolean, height: String, me: String },
    setup: (p: { chart: { rooms: unknown[] }; preview: boolean }) => () =>
      h('lab-scene', { 'data-preview': p.preview, 'data-rooms': p.chart.rooms.length }),
  },
}));

const lab = {
  id: 'built-in',
  name: 'Built-in Lykeion',
  rooms: [{ stage: 'grounding', team: 'Grounding', agents: [{ name: 'leader', role: 'leader' }] }],
};
const cleanups: (() => void)[] = [];
afterEach(() => {
  cleanups.splice(0).forEach((fn) => fn());
  vi.unstubAllGlobals();
  calls.length = 0;
});

async function mount(query: string, existing = false) {
  vi.stubGlobal('document', { baseURI: 'http://localhost/' });
  vi.stubGlobal('Document', class {});
  vi.stubGlobal('ShadowRoot', class {});
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  });
  queryClient.setQueryData(keys.me, { id: 'u1', name: 'Ada' });
  queryClient.setQueryData(
    keys.workspaces,
    existing ? [{ id: 'w1', name: 'Home', slug: 'home' }] : [],
  );
  queryClient.setQueryData(keys.laboratories, [lab]);
  queryClient.setQueryData(keys.runtimes(''), []);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:p(.*)*', component: { render: () => null } }],
  });
  await router.push('/onboarding' + query);
  const app = createTreeApp({ render: () => h(OnboardingView) });
  app.use(VueQueryPlugin, { queryClient });
  app.use(router);
  const root = node('root');
  app.mount(root);
  cleanups.push(() => {
    app.unmount();
    queryClient.clear();
    workspaceId.value = '';
  });
  await nextTick();
  await nextTick();
  return { root, router };
}
const scene = (root: ReturnType<typeof node>) => all(root).find((n) => n.tag === 'lab-scene');

describe('onboarding Laboratory step', () => {
  it('lists the steps with Laboratory second', async () => {
    const { root } = await mount('');
    const t = textOf(root);
    expect(t.indexOf('Laboratory')).toBeGreaterThan(t.indexOf('Welcome'));
    expect(t.indexOf('Laboratory')).toBeLessThan(t.indexOf('Skills'));
    expect(t).not.toContain('Workspace');
  });

  it.each(['workspace', 'laboratory'])('opens the step for ?step=%s', async (step) => {
    const { root } = await mount(`?step=${step}`);
    expect(textOf(root)).toContain('Create your laboratory.');
  });

  it('renders the preview after the card prefix field when creating a new one', async () => {
    const { root } = await mount('?step=laboratory');
    const nodes = all(root);
    const prefix = nodes.findIndex((n) => n.props['aria-label'] === 'Card prefix');
    const s = nodes.findIndex((n) => n.tag === 'lab-scene');
    expect(prefix).toBeGreaterThan(-1);
    expect(s).toBeGreaterThan(prefix);
    expect(nodes[s]!.props['data-preview']).toBe(true);
    expect(nodes[s]!.props['data-rooms']).toBe(1);
  });

  it('offers the available laboratories above the preview, the built-in one preselected', async () => {
    const { root } = await mount('?step=laboratory');
    const nodes = all(root);
    const radio = nodes.findIndex((n) => n.tag === 'input' && n.props.name === 'laboratory');
    expect(radio).toBeGreaterThan(-1);
    expect(nodes[radio]!.props['aria-label']).toBe('Built-in Lykeion');
    expect((nodes[radio] as unknown as { checked: boolean }).checked).toBe(true);
    expect(textOf(root)).toContain('Built-in Lykeion');
    expect(radio).toBeLessThan(nodes.findIndex((n) => n.tag === 'lab-scene'));
  });

  it('lays the laboratory row out like the Create a new workspace row', async () => {
    const { root } = await mount('?step=laboratory');
    const nodes = all(root);
    const labelOf = (name: string) =>
      nodes.find(
        (n) => n.tag === 'label' && all(n).some((c) => c.tag === 'input' && c.props.name === name),
      )!;
    const lab = labelOf('laboratory');
    const create = labelOf('workspace');
    const cls = (n: ReturnType<typeof node>) => String(n.props.class ?? '');
    expect(cls(lab)).toBe('choice-head');
    expect(cls(lab)).toBe(cls(create));
    const kids = (n: ReturnType<typeof node>) =>
      n.children.filter((c) => typeof c !== 'string').map((c) => `${c.tag}.${cls(c)}`);
    expect(kids(lab)).toEqual(kids(create));
    // Both radios sit in the same card, so the one `.choice input[type='radio']` rule styles them.
    const card = nodes.find((n) => n.tag === 'div' && cls(n).split(' ').includes('choice'))!;
    expect(all(card)).toContain(lab);
    expect(all(card)).toContain(create);
    expect(all(card).filter((n) => cls(n).split(' ').includes('choice'))).toEqual([card]);
  });

  it('renders no preview for an existing workspace', async () => {
    const { root } = await mount('', true);
    const start = all(root).find((n) => n.tag === 'button' && textOf(n).includes('Set up your lab'))!;
    (start.props.onClick as () => void)();
    await nextTick();
    expect(textOf(root)).toContain('Continue with Home');
    expect(scene(root)).toBeUndefined();
  });

  it('creates with the built-in laboratory', async () => {
    const { root } = await mount('?step=laboratory');
    const random = all(root).find((n) => n.tag === 'button' && textOf(n).includes('Random'))!;
    (random.props.onClick as () => void)();
    await nextTick();
    const create = all(root).find((n) => n.tag === 'button' && textOf(n).includes('Create workspace'))!;
    (create.props.onClick as () => void)();
    await vi.waitFor(() => expect(calls[0]).toBeDefined());
    expect(calls[0]).toMatchObject({
      method: 'POST',
      path: '/api/workspaces',
      body: { laboratory: 'built-in' },
    });
    expect(calls[0]!.body).toMatchObject({ name: expect.any(String), slug: expect.any(String) });
  });
});
