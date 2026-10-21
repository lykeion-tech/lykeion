import { afterEach, describe, expect, it } from 'vitest';
import { createRenderer, createSSRApp, h, nextTick, ref, ssrContextKey, type Component } from 'vue';
import { renderToString } from '@vue/server-renderer';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import AssignPicker from '../apps/dashboard/src/components/AssignPicker.vue';
import TeamDetail from '../apps/dashboard/src/views/TeamDetail.vue';
import TeamsView from '../apps/dashboard/src/views/TeamsView.vue';
import { workspaceId } from '../apps/dashboard/src/api/session';
import { keys } from '../apps/dashboard/src/api/keys';
import type { Picked, Range } from '../apps/dashboard/src/lib/studies';

const member = (name: string) => ({
  name,
  role: `${name} role`,
  agent: { models: { claude: 'sonnet', codex: 'gpt-5' }, instructions: '' },
});
const manifest = (name: string, stage: string) => ({
  name,
  description: `${name} description`,
  domain: '',
  stage,
  leader: member('lead'),
  auditor: member('auditor'),
  members: [member('scout')],
  criteria: [],
  steps: 12,
});
const storeTeam = (id: string, name: string, stage: string, source: string, added = false) => ({
  id,
  created_at: '2026-10-21T10:00:00Z',
  name,
  source,
  domain: '',
  stage,
  manifest: manifest(name, stage),
  added,
});

function client() {
  workspaceId.value = 'w';
  const c = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  });
  c.setQueryData(keys.me, { id: 'u1' });
  c.setQueryData(keys.agents('w'), [
    {
      id: 'a1',
      name: 'Solo agent',
      visibility: 'workspace',
      owner_id: 'u1',
      archived_at: null,
    },
  ]);
  c.setQueryData(keys.members('w'), [{ user_id: 'u2', name: 'Colleague', email: 'c@lab.test' }]);
  c.setQueryData(keys.teams('w', '', ''), [
    storeTeam('t1', 'Hypotheses Team', 'hypotheses', 'shipped'),
    storeTeam('t2', 'My Team', 'grounding', 'uploaded', true),
  ]);
  c.setQueryData(keys.team('w', 't1'), {
    id: 't1',
    manifest: manifest('Hypotheses Team', 'hypotheses'),
  });
  c.setQueryData(keys.team('w', 't2'), { id: 't2', manifest: manifest('My Team', 'grounding') });
  return c;
}

afterEach(() => {
  workspaceId.value = '';
});

async function ssr(component: Component, props: Record<string, unknown>, path: string) {
  const queryClient = client();
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/teams', component: TeamsView },
      { path: '/teams/:id', component: TeamDetail },
      { path: '/agents', component: TeamsView },
    ],
  });
  await router.push(path);
  await router.isReady();
  const app = createSSRApp({ render: () => h(component, props) });
  app.use(VueQueryPlugin, { queryClient });
  app.use(router);
  try {
    return await renderToString(app);
  } finally {
    queryClient.clear();
  }
}

// A DOM-free renderer: enough of a tree to mount a component, find a button by
// its text and call its click handler.
type Node = {
  tag: string;
  text: string;
  props: Record<string, unknown>;
  children: Node[];
  parent: Node | null;
  addEventListener(): void;
  removeEventListener(): void;
  querySelector(): null;
  contains(): boolean;
  focus(): void;
};
function node(tag: string, text = ''): Node {
  return {
    tag,
    text,
    props: {},
    children: [],
    parent: null,
    addEventListener() {},
    removeEventListener() {},
    querySelector: () => null,
    contains: () => true,
    focus() {},
  };
}
const textOf = (n: Node): string => n.text + n.children.map(textOf).join('');
const all = (n: Node): Node[] => [n, ...n.children.flatMap(all)];
const { createApp: createTreeApp } = createRenderer<Node, Node>({
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

async function mount(component: Component, props: () => Record<string, unknown>, path = '/') {
  const g = globalThis as { document?: unknown };
  g.document ??= { addEventListener() {}, removeEventListener() {} };
  const queryClient = client();
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:p(.*)*', component: TeamsView }],
  });
  await router.push(path);
  const app = createTreeApp({ render: () => h(component, props()) });
  app.use(VueQueryPlugin, { queryClient });
  app.use(router);
  // Vitest compiles components for SSR, and they register with an SSR context.
  app.provide(ssrContextKey, {});
  const root = node('root');
  app.mount(root);
  await nextTick();
  const button = (label: RegExp) =>
    all(root).find((n) => n.tag === 'button' && label.test(textOf(n)));
  const click = async (label: RegExp) => {
    const b = button(label);
    expect(b, textOf(root)).toBeTruthy();
    (b!.props.onClick as () => void)();
    await nextTick();
  };
  return { text: () => textOf(root), button, click, unmount: () => app.unmount() };
}

async function mountPicker(agents = false) {
  const assignee = ref<Picked | null>(null);
  const range = ref<Range>({ from: 'grounding', to: 'reporting' });
  return mount(AssignPicker, () => ({
    modelValue: assignee.value,
    'onUpdate:modelValue': (v: Picked | null) => (assignee.value = v),
    range: range.value,
    'onUpdate:range': (v: Range) => (range.value = v),
    agents,
  }));
}

describe('AssignPicker', () => {
  it('offers only Teams and Team sets', async () => {
    const p = await mountPicker();
    try {
      await p.click(/Built-in Teams/);
      const menu = p.text();
      expect(menu).toContain('Teams');
      expect(menu).toContain('Hypotheses Team');
      expect(menu).toContain('My Team');
      expect(menu).toContain('Team sets');
      expect(menu).not.toContain('Unassigned');
      expect(menu).not.toContain('Members');
      expect(menu).not.toContain('Colleague');
      expect(menu).not.toContain('Agents');
      expect(menu).not.toContain('Solo agent');
    } finally {
      p.unmount();
    }
  });

  it('still offers agents to an Autoresearch', async () => {
    const p = await mountPicker(true);
    try {
      await p.click(/Unassigned/);
      expect(p.text()).toContain('Solo agent');
    } finally {
      p.unmount();
    }
  });

  it('shows the Stages pill when nothing is picked', async () => {
    const p = await mountPicker();
    try {
      expect(p.button(/Grounding → Reporting/)).toBeTruthy();
    } finally {
      p.unmount();
    }
  });
});

describe('TeamDetail', () => {
  it('shows Duplicate this Team and no edit for a built-in Team', async () => {
    const t = await mount(TeamDetail, () => ({ id: 't1' }), '/teams/t1');
    try {
      expect(t.text()).toContain('Hypotheses Team');
      expect(t.text()).toContain('Duplicate this Team');
      expect(t.text()).not.toContain('Add Team');
      expect(t.text()).not.toMatch(/Delete/);
      await t.click(/^Agents$/);
      expect(t.text()).toContain('scout');
      expect(t.text()).not.toContain('Edit skills');
    } finally {
      t.unmount();
    }
  });

  it('keeps editing for a researcher’s own Team', async () => {
    const t = await mount(TeamDetail, () => ({ id: 't2' }), '/teams/t2');
    try {
      expect(t.text()).not.toContain('Duplicate this Team');
      await t.click(/^Agents$/);
      expect(t.text()).toContain('Edit skills');
    } finally {
      t.unmount();
    }
  });
});

describe('no versions', () => {
  it('no "Version" text renders in TeamsView or TeamDetail', async () => {
    for (const html of [
      await ssr(TeamsView, {}, '/teams'),
      await ssr(TeamDetail, { id: 't1' }, '/teams/t1'),
      await ssr(TeamDetail, { id: 't2' }, '/teams/t2'),
    ]) {
      expect(html).toContain('Team');
      expect(html).not.toMatch(/version/i);
    }
  });
});
