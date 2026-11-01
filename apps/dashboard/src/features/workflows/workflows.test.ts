import { describe, expect, it, vi } from 'vitest';
import { createRenderer, h, nextTick, ssrContextKey, type Component } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import WorkflowsView from './WorkflowsView.vue';
import WorkflowDetail from './WorkflowDetail.vue';
import { keys } from '../../api/keys';
import { byDomain } from '../../lib/workflows';
import type { WorkflowSummary } from '../../api/types';

const summary = (slug: string, name: string, domain: string, tags: string[]): WorkflowSummary => ({
  slug,
  name,
  domain,
  field: 'f',
  subfield: 's',
  description: `${name} description`,
  tags,
  authors: [{ name: 'Ada', organization: 'Lab' }],
  experience: 'expert',
  expert_hours: 4,
  agent_budget_sec: 7200,
  cpus: 2,
  memory_mb: 4096,
});
const fixtures = [
  summary('b', 'Zeta flow', 'earth-sciences', ['ice']),
  summary('a', 'Alpha flow', 'earth-sciences', ['ocean']),
  summary('c', 'Cell flow', 'life-sciences', ['cells']),
];

describe('byDomain', () => {
  it('groups, sorts and searches', () => {
    const groups = byDomain(fixtures, '');
    expect(groups.map((g) => g.label)).toEqual(['Earth sciences', 'Life sciences']);
    expect(groups[0]!.workflows.map((x) => x.name)).toEqual(['Alpha flow', 'Zeta flow']);
    expect(groups[0]!.emoji).toBe('🌍');
    const found = byDomain(fixtures, 'ICE');
    expect(found).toHaveLength(1);
    expect(found[0]!.workflows.map((x) => x.slug)).toEqual(['b']);
  });
});

type Node = {
  tag: string;
  text: string;
  props: Record<string, unknown>;
  children: Node[];
  parent: Node | null;
  querySelectorAll(): Node[];
  addEventListener(): void;
  removeEventListener(): void;
};
const node = (tag: string, text = ''): Node => ({
  tag,
  text,
  props: {},
  children: [],
  parent: null,
  querySelectorAll: () => [],
  addEventListener() {},
  removeEventListener() {},
});
const textOf = (n: Node): string => n.text + n.children.map(textOf).join('');
const all = (n: Node): Node[] => [n, ...n.children.flatMap(all)];
const { createApp } = createRenderer<Node, Node>({
  createElement: (tag) => node(tag),
  createText: (text) => node('#text', text),
  createComment: () => node('#comment'),
  setText: (n, text) => (n.text = text),
  setElementText: (n, text) => {
    n.children = [];
    n.text = text;
  },
  insert(child, parent, anchor) {
    child.parent?.children.splice(child.parent.children.indexOf(child), 1);
    child.parent = parent;
    const i = anchor ? parent.children.indexOf(anchor) : -1;
    if (i < 0) parent.children.push(child);
    else parent.children.splice(i, 0, child);
  },
  remove(child) {
    child.parent?.children.splice(child.parent.children.indexOf(child), 1);
    child.parent = null;
  },
  parentNode: (n) => n.parent,
  nextSibling: (n) =>
    n.parent ? (n.parent.children[n.parent.children.indexOf(n) + 1] ?? null) : null,
  patchProp: (n, key, _prev, next) => (n.props[key] = next),
});

async function mount(component: Component, props: Record<string, unknown>) {
  const g = globalThis as { document?: unknown };
  g.document ??= { addEventListener() {}, removeEventListener() {} };
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  });
  queryClient.setQueryData(keys.workflows, { release: '1.2', workflows: fixtures });
  queryClient.setQueryData(keys.workflow('b'), {
    ...fixtures[0],
    readme: '# Readme',
    instruction: 'Do it',
    files: [
      { path: 'instruction.md', size: 5, embedded: true },
      { path: 'tests/test.sh', size: 900, embedded: false },
      { path: 'solution/solve.sh', size: 12, embedded: true },
    ],
  });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:p(.*)*', component }],
  });
  await router.push('/');
  const app = createApp({ render: () => h(component, props) });
  app.use(VueQueryPlugin, { queryClient });
  app.use(router);
  app.provide(ssrContextKey, {});
  const root = node('root');
  app.mount(root);
  await nextTick();
  const click = async (label: RegExp) => {
    const b = all(root).find((n) => n.tag === 'button' && label.test(textOf(n).trim()));
    expect(b, textOf(root)).toBeTruthy();
    (b!.props.onClick as () => void)();
    await nextTick();
  };
  return { text: () => textOf(root), click };
}

describe('Workflows pages', () => {
  it('the library lists workflows by field', async () => {
    const m = await mount(WorkflowsView, {});
    expect(m.text()).toContain('Earth sciences');
    expect(m.text()).toContain('Zeta flow');
    expect(m.text()).toContain('From Terminal-Bench-Science 1.2');
  });

  it('the Files tab shows tests and marks unembedded files', async () => {
    vi.stubGlobal('fetch', vi.fn());
    const m = await mount(WorkflowDetail, { slug: 'b' });
    expect(m.text()).toContain('Workflows');
    await m.click(/^Files$/);
    expect(m.text()).toContain('tests');
    expect(m.text()).not.toContain('test.sh');
    await m.click(/tests$/);
    expect(m.text()).toContain('test.sh');
    await m.click(/^test\.sh$/);
    expect(m.text()).toContain('Preview unavailable');
    expect(m.text()).toContain('900');
    expect(fetch).not.toHaveBeenCalled();
    vi.unstubAllGlobals();
  });

  it('previews any embedded file as text', async () => {
    const fetchMock = vi.fn(async () => new Response('echo solved'));
    vi.stubGlobal('fetch', fetchMock);
    const m = await mount(WorkflowDetail, { slug: 'b' });
    await m.click(/^Files$/);
    await m.click(/solution$/);
    await m.click(/^solve\.sh$/);
    await new Promise((r) => setTimeout(r, 0));
    await nextTick();
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(String((fetchMock.mock.calls[0] as unknown[])[0])).toContain('solution%2Fsolve.sh');
    expect(m.text()).toContain('echo solved');
    vi.unstubAllGlobals();
  });
});
