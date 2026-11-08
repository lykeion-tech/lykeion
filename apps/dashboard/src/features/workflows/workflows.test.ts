import { describe, expect, it, vi } from 'vitest';
import { createRenderer, h, nextTick, ssrContextKey, type Component } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import WorkflowsView from './WorkflowsView.vue';
import WorkflowDetail from './WorkflowDetail.vue';
import CreateStudy from '../studies/CreateStudy.vue';
import StudyDetail from '../studies/StudyDetail.vue';
import { workspaceId } from '../../api/session';
import { keys } from '../../api/keys';
import { byDomain, stripComments } from '../../lib/workflows';
import { renderMarkdown } from '../../lib/markdown';

it('wraps Workflow source lines naturally while preserving explicit breaks and code', () => {
  const result = renderMarkdown(
    'First source line\ncontinues here.\n\nHard break  \nnext line.\n\n```\na\nb\n```',
    false,
  );
  expect(result.html).toContain('First source line\ncontinues here.');
  expect(result.html).toContain('Hard break<br>\nnext line.');
  expect(result.codes).toEqual(['a\nb\n']);
  expect(renderMarkdown('one\ntwo').html).toContain('one<br>\ntwo');
});
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

describe('stripComments', () => {
  it('removes single and multi-line HTML comments', () => {
    expect(stripComments('a<!-- x -->b\n<!--\nm\n-->c')).toBe('ab\nc');
  });
});

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
  querySelector(): null;
  contains(): boolean;
  focus(): void;
  showModal(): void;
  getRootNode(): null;
  getBoundingClientRect(): { right: number };
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
  querySelector: () => null,
  contains: () => true,
  focus() {},
  showModal() {},
  getRootNode: () => null,
  getBoundingClientRect: () => ({ right: 0 }),
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
      { path: 'results.csv', size: 21, embedded: true },
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
    expect(m.text()).not.toContain('From Terminal-Bench-Science');
  });

  it('the Files tab shows tests and marks unembedded files', async () => {
    vi.stubGlobal('fetch', vi.fn());
    const m = await mount(WorkflowDetail, { slug: 'b' });
    expect(m.text()).not.toContain('Workflows');
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

  it('renders an embedded CSV as a table', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('name,value\nalpha,42\n')));
    const m = await mount(WorkflowDetail, { slug: 'b' });
    try {
      await m.click(/^Files$/);
      await m.click(/^results\.csv$/);
      await new Promise((r) => setTimeout(r, 0));
      await nextTick();
      expect(m.text()).toContain('alpha');
      expect(m.text()).toContain('42');
      expect(m.text()).not.toContain('name,value');
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('does not show a late preview after selecting another file', async () => {
    let resolveFirst!: (response: Response) => void;
    const first = new Promise<Response>((resolve) => { resolveFirst = resolve; });
    vi.stubGlobal('fetch', vi.fn((url: RequestInfo | URL) =>
      String(url).includes('results.csv') ? first : Promise.resolve(new Response('echo current')),
    ));
    const m = await mount(WorkflowDetail, { slug: 'b' });
    try {
      await m.click(/^Files$/);
      await m.click(/^results\.csv$/);
      await m.click(/solution$/);
      await m.click(/^solve\.sh$/);
      await new Promise((r) => setTimeout(r, 0));
      resolveFirst(new Response('name,value\nstale,42\n'));
      await new Promise((r) => setTimeout(r, 0));
      await nextTick();
      expect(m.text()).toContain('echo current');
      expect(m.text()).not.toContain('stale');
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

describe('Starting a study with a Workflow', () => {
  const seed = (c: QueryClient, extra: Record<string, unknown> = {}) => {
    c.setQueryData(keys.me, { id: 'u1' });
    c.setQueryData(keys.workspace('w'), { name: 'Lab' });
    c.setQueryData(keys.cards('w'), []);
    c.setQueryData(keys.runtimes('w'), []);
    c.setQueryData(keys.researches('w'), []);
    c.setQueryData(keys.teams('w', '', ''), []);
    c.setQueryData(keys.agents('w'), [
      { id: 'a1', name: 'Solo agent', visibility: 'workspace', owner_id: 'u1', archived_at: null, stage: 'experimentation' },
    ]);
    c.setQueryData(keys.members('w'), []);
    for (const key of [keys.comments, keys.events, keys.runs, keys.ratings])
      c.setQueryData(key('w', 'c'), []);
    c.setQueryData(keys.records('w', 'c', 'experimentation'), []);
    c.setQueryData(keys.card('w', 'c'), {
      id: 'c',
      number: 1,
      title: 'Study',
      question: 'Question',
      owner_id: 'u1',
      board_column: 'experimentation',
      state: 'working',
      flag: '',
      ending: '',
      block_kind: '',
      hold_text: '',
      scope_from: 'experimentation',
      scope_to: 'experimentation',
      round: 1,
      redos: 0,
      teams: {},
      off_label: [],
      attachments: [],
      services: [],
      assignee_type: 'agent',
      assignee_id: 'a1',
      created_at: '2026-10-25T10:00:00Z',
      updated_at: '2026-10-25T10:00:00Z',
      ...extra,
    });
  };
  async function mountStudy(
    component: Component,
    props: Record<string, unknown>,
    extra = {},
    library = true,
  ) {
    workspaceId.value = 'w';
    const g = globalThis as { document?: unknown };
    g.document ??= { addEventListener() {}, removeEventListener() {} };
    vi.stubGlobal('window', { innerWidth: 1512, addEventListener() {}, removeEventListener() {} });
    vi.stubGlobal('Document', class {});
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        disconnect() {}
      },
    );
    vi.stubGlobal('ShadowRoot', class {});
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
    });
    seed(queryClient, extra);
    if (library) queryClient.setQueryData(keys.workflows, { release: '1.2', workflows: fixtures });
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
      const b = all(root).find(
        (n) => n.tag === 'button' && label.test(String(n.props['aria-label'] ?? textOf(n).trim())),
      );
      expect(b, textOf(root)).toBeTruthy();
      (b!.props.onClick as () => void)();
      await nextTick();
    };
    const fill = async (label: string, value: string) => {
      const field = all(root).find((n) => n.props['aria-label'] === label);
      expect(field, label).toBeTruthy();
      (field!.props['onUpdate:modelValue'] as (value: string) => void)(value);
      await nextTick();
    };
    const submit = async () => {
      const form = all(root).find((n) => n.tag === 'form');
      await (form!.props.onSubmit as (event: unknown) => Promise<void>)({ preventDefault() {} });
      await nextTick();
    };
    // Picks one file; the upload endpoint is stubbed to accept it.
    const attach = async () => {
      vi.stubGlobal(
        'XMLHttpRequest',
        class {
          upload = {};
          status = 201;
          responseText = '{"id":"f1","name":"data.csv","size_bytes":3}';
          onload: () => void = () => {};
          open() {}
          setRequestHeader() {}
          send() {
            this.onload();
          }
        },
      );
      const input = all(root).find((n) => n.tag === 'input' && n.props.type === 'file');
      expect(input).toBeTruthy();
      const target = { files: [new File(['a'], 'data.csv')], value: 'x' };
      await (input!.props.onChange as (e: unknown) => Promise<void>)({ target });
      await nextTick();
    };
    return {
      text: () => textOf(root),
      click,
      fill,
      attach,
      submit,
      unmount: () => {
        app.unmount();
        queryClient.clear();
        workspaceId.value = '';
        vi.unstubAllGlobals();
      },
    };
  }
  const posting = () =>
    vi.fn(
      async (_path: RequestInfo | URL, init?: RequestInit) =>
        new Response(init?.method === 'POST' ? '{}' : '[]', {
          status: init?.method === 'POST' ? 201 : 200,
        }),
    );

  it('a workflow study sends its workflow and no Grounding criteria', async () => {
    const fetchMock = posting();
    vi.stubGlobal('fetch', fetchMock);
    const t = await mountStudy(CreateStudy, { workflow: 'b' });
    try {
      expect(t.text()).toContain('Workflow: Zeta flow');
      expect(t.text()).not.toContain('Grounding criteria');
      await t.click(/^Assignees$/);
      await t.click(/Solo agent/);
      await t.fill('Describe your study', 'Question');
      await t.attach();
      await t.submit();
      const posted = fetchMock.mock.calls.find((call) => call[0] === '/api/cards');
      const body = JSON.parse(String(posted![1]?.body));
      expect(body.workflow).toBe('b');
      expect(body.attachment_ids).toEqual(['f1']);
      expect(body.assignee).toEqual({ type: 'agent', id: 'a1' });
      expect(body.scope).toEqual({ from: 'experimentation', to: 'experimentation' });
      expect(body).not.toHaveProperty('grounding_criteria');
    } finally {
      t.unmount();
    }
  });

  it('removing the workflow leaves Grounding criteria absent', async () => {
    vi.stubGlobal('fetch', posting());
    const t = await mountStudy(CreateStudy, { workflow: 'b' });
    try {
      expect(t.text()).not.toContain('Grounding criteria');
      await t.click(/^Remove workflow$/);
      expect(t.text()).not.toContain('Workflow: Zeta flow');
      expect(t.text()).not.toContain('Grounding criteria');
    } finally {
      t.unmount();
    }
  });

  it('a study links its workflow by name and release', async () => {
    vi.stubGlobal('fetch', posting());
    const t = await mountStudy(
      StudyDetail,
      { id: 'c' },
      { workflow: 'b', workflow_release: 'v0.1.0' },
    );
    try {
      await t.click(/^Properties$/);
      expect(t.text()).toContain('Zeta flow · v0.1.0');
      expect(t.text()).not.toContain('Workflow: Zeta');
    } finally {
      t.unmount();
    }
  });

  it('does not call a workflow unavailable while the library loads', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise(() => {})),
    );
    const t = await mountStudy(StudyDetail, { id: 'c' }, { workflow: 'gone' }, false);
    try {
      await t.click(/^Properties$/);
      expect(t.text()).not.toContain('no longer available');
    } finally {
      t.unmount();
    }
  });

  it('a dropped workflow is named as unavailable', async () => {
    vi.stubGlobal('fetch', posting());
    const t = await mountStudy(StudyDetail, { id: 'c' }, { workflow: 'gone' });
    try {
      await t.click(/^Properties$/);
      expect(t.text()).toContain('This workflow is no longer available');
    } finally {
      t.unmount();
    }
  });
});
