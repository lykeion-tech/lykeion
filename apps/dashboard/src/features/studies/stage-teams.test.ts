import { afterEach, describe, expect, it, vi } from 'vitest';
import { createRenderer, createSSRApp, h, nextTick, ref, ssrContextKey, type Component } from 'vue';
import { renderToString } from '@vue/server-renderer';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import AssignPicker from '../../ui/AssignPicker.vue';
import TeamDetail from '../teams/TeamDetail.vue';
import TeamsView from '../teams/TeamsView.vue';
import AgentsView from '../agents/AgentsView.vue';
import CreateStudy from './CreateStudy.vue';
import StudyDetail from './StudyDetail.vue';
import { workspaceId } from '../../api/session';
import { keys } from '../../api/keys';
import type { Picked, Range } from '../../lib/studies';

vi.mock('../coresearch/Aristotle.vue', () => ({ default: { render: () => null } }));

vi.mock('../../ui/CompactSelect.vue', () => ({
  default: {
    props: ['modelValue', 'options', 'label'],
    emits: ['update:modelValue'],
    render(this: any) {
      return h(
        'select',
        {
          'aria-label': this.label,
          value: this.modelValue,
          onChange: (event: any) => this.$emit('update:modelValue', event.target.value),
        },
        this.options.map((option: any) =>
          h('option', { value: option.value, disabled: option.disabled }, option.label),
        ),
      );
    },
  },
}));

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
    {
      id: 'a2',
      name: 'Aristotle',
      visibility: 'workspace',
      owner_id: 'u1',
      archived_at: null,
      built_in_key: 'aristotle',
    },
    {
      id: 'a3',
      name: 'Team copy',
      visibility: 'workspace',
      owner_id: 'u1',
      archived_at: null,
      team_member: true,
    },
    {
      id: 'a4',
      name: 'Colleague private',
      visibility: 'private',
      owner_id: 'u2',
      archived_at: null,
    },
    {
      id: 'a5',
      name: 'Retired agent',
      visibility: 'workspace',
      owner_id: 'u1',
      archived_at: '2026-10-20T10:00:00Z',
    },
  ]);
  c.setQueryData(keys.members('w'), [{ user_id: 'u2', name: 'Colleague', email: 'c@lab.test' }]);
  c.setQueryData(keys.teams('w', '', ''), [
    storeTeam('t1', 'Hypotheses Team', 'hypotheses', 'shipped'),
    storeTeam('t2', 'My Team', 'grounding', 'uploaded', true),
  ]);
  c.setQueryData(keys.team('w', 't1'), {
    id: 't1',
    uploaded_by: null,
    visibility: 'workspace',
    manifest: manifest('Hypotheses Team', 'hypotheses'),
  });
  c.setQueryData(keys.team('w', 't2'), {
    id: 't2',
    uploaded_by: 'u1',
    visibility: 'private',
    manifest: manifest('My Team', 'grounding'),
  });
  return c;
}

afterEach(() => {
  workspaceId.value = '';
  vi.unstubAllGlobals();
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
  querySelectorAll(): Node[];
  contains(): boolean;
  focus(): void;
  showModal(): void;
  getRootNode(): null;
  getBoundingClientRect(): { right: number };
  value?: unknown;
  readonly options: Node[];
  readonly tagName: string;
};
function node(tag: string, text = ''): Node {
  return {
    tag,
    text,
    get options() {
      return this.children.filter((n) => n.tag === 'option');
    },
    get tagName() {
      return tag.toUpperCase();
    },
    props: {},
    children: [],
    parent: null,
    addEventListener() {},
    removeEventListener() {},
    querySelector: () => null,
    querySelectorAll: () => [],
    contains: () => true,
    focus() {},
    showModal() {},
    getRootNode: () => null,
    getBoundingClientRect: () => ({ right: 0 }),
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

async function mount(
  component: Component,
  props: () => Record<string, unknown>,
  path = '/',
  seed?: (client: QueryClient) => void,
) {
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
  const queryClient = client();
  seed?.(queryClient);
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
    all(root).find((n) => n.tag === 'button' && label.test(textOf(n).trim()));
  const click = async (label: RegExp) => {
    const b = button(label);
    expect(b, textOf(root)).toBeTruthy();
    (b!.props.onClick as () => void)();
    await nextTick();
  };
  const input = (label: string) =>
    all(root).find((n) => n.tag === 'input' && n.props['aria-label'] === label);
  const fill = async (label: string, value: string) => {
    const field = all(root).find((n) => n.props['aria-label'] === label);
    expect(field, label).toBeTruthy();
    (field!.props['onUpdate:modelValue'] as (value: string) => void)(value);
    await nextTick();
  };
  const change = async (label: string, value: string) => {
    const field = all(root).find((n) => n.props['aria-label'] === label);
    expect(field, label).toBeTruthy();
    (field!.props.onChange as (event: unknown) => void)({ target: { value } });
    await nextTick();
  };
  const alert = (label: string) => {
    const group = all(root).find((n) => n.props['aria-label'] === label);
    const shown = group && all(group).find((n) => n.props.role === 'alert');
    return shown ? textOf(shown).trim() : undefined;
  };
  const submit = async () => {
    const form = all(root).find((n) => n.tag === 'form');
    await (form!.props.onSubmit as (event: unknown) => Promise<void>)({ preventDefault() {} });
    await nextTick();
  };
  return {
    text: () => textOf(root),
    nodes: () => all(root),
    button,
    click,
    input,
    fill,
    change,
    alert,
    submit,
    unmount: () => {
      app.unmount();
      queryClient.clear();
    },
  };
}

async function mountPicker(agents = false, lastSet: Picked | null = null, oneAgent = false) {
  const assignee = ref<Picked | null>(null);
  const range = ref<Range>({ from: 'grounding', to: 'validation' });
  const mounted = await mount(AssignPicker, () => ({
    modelValue: assignee.value,
    'onUpdate:modelValue': (v: Picked | null) => (assignee.value = v),
    range: range.value,
    'onUpdate:range': (v: Range) => (range.value = v),
    agents,
    lastSet,
    oneAgent,
  }));
  return { ...mounted, assignee, range };
}

describe('AssignPicker', () => {
  it('shows saved Agent and Team icons in the picker', async () => {
    const assignee = ref<Picked | null>(null);
    const range = ref<Range>({ from: 'grounding', to: 'validation' });
    const p = await mount(
      AssignPicker,
      () => ({
        modelValue: assignee.value,
        'onUpdate:modelValue': (value: Picked | null) => (assignee.value = value),
        range: range.value,
        'onUpdate:range': (value: Range) => (range.value = value),
        oneAgent: true,
      }),
      '/',
      (client) => {
        client.setQueryData(keys.agents('w'), [
          {
            id: 'a',
            name: 'Scientist',
            harness: 'claude',
            avatar_url: 'emoji:🧬',
            visibility: 'workspace',
            owner_id: 'u1',
            archived_at: null,
          },
        ]);
        client.setQueryData(keys.teams('w', '', ''), [
          {
            ...storeTeam('t', 'Grounding Team', 'grounding', 'shipped'),
            avatar_url: 'emoji:🔬',
            agent_clis: ['codex'],
          },
        ]);
        client.setQueryData(keys.runtimes('w'), [
          { owner_id: 'u1', harness: 'claude', status: 'online', unhealthy_reason: null },
          { owner_id: 'u1', harness: 'codex', status: 'online', unhealthy_reason: null },
        ]);
      },
    );
    try {
      await p.click(/Built-in Teams/);
      expect(p.text()).toContain('🧬');
      expect(p.text()).toContain('🔬');
      expect(p.nodes().some((n) => n.props['aria-label'] === 'Claude Code')).toBe(true);
      expect(p.nodes().some((n) => n.props['aria-label'] === 'Codex')).toBe(true);
      expect(all(p.button(/Scientist/)!).some((n) => n.props['aria-label'] === 'CLI online')).toBe(
        true,
      );
      expect(
        all(p.button(/Grounding Team/)!).some((n) => n.props['aria-label'] === 'Team CLIs online'),
      ).toBe(true);
      await p.click(/Scientist/);
      expect(all(p.button(/Scientist/)!).some((n) => n.props['aria-label'] === 'CLI online')).toBe(
        true,
      );
    } finally {
      p.unmount();
    }
  });

  it('does not show online status for unhealthy or another member’s runtimes', async () => {
    const assignee = ref<Picked | null>(null);
    const range = ref<Range>({ from: 'grounding', to: 'validation' });
    const p = await mount(
      AssignPicker,
      () => ({
        modelValue: assignee.value,
        'onUpdate:modelValue': (value: Picked | null) => (assignee.value = value),
        range: range.value,
        'onUpdate:range': (value: Range) => (range.value = value),
        oneAgent: true,
      }),
      '/',
      (client) => {
        client.setQueryData(keys.agents('w'), [
          {
            id: 'a',
            name: 'Scientist',
            harness: 'claude',
            visibility: 'workspace',
            owner_id: 'u1',
            archived_at: null,
          },
        ]);
        client.setQueryData(keys.teams('w', '', ''), [
          { ...storeTeam('t', 'Grounding Team', 'grounding', 'shipped'), agent_clis: ['codex'] },
        ]);
        client.setQueryData(keys.runtimes('w'), [
          { owner_id: 'u1', harness: 'claude', status: 'online', unhealthy_reason: 'sign_in' },
          { owner_id: 'u2', harness: 'codex', status: 'online', unhealthy_reason: null },
        ]);
      },
    );
    try {
      await p.click(/Built-in Teams/);
      expect(p.nodes().some((n) => n.props['aria-label'] === 'CLI online')).toBe(false);
      expect(p.nodes().some((n) => n.props['aria-label'] === 'Team CLIs online')).toBe(false);
    } finally {
      p.unmount();
    }
  });

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

  it('keeps a Team set with one Team end to end', async () => {
    const p = await mountPicker(false, { type: 'team', teams: { hypotheses: 't1' } });
    try {
      await p.click(/Built-in Teams/);
      await p.click(/Your last set/);
      expect(p.assignee.value).toEqual({ type: 'team', teams: { hypotheses: 't1' } });
      expect(p.range.value).toEqual({ from: 'grounding', to: 'validation' });
      expect(p.text()).toContain('Your Team set');
    } finally {
      p.unmount();
    }
  });

  it('offers a study one agent the researcher may use', async () => {
    const p = await mountPicker(false, null, true);
    try {
      await p.click(/Built-in Teams/);
      const groups = p
        .nodes()
        .filter((n) => n.props.class === 'menu-group')
        .map((n) => textOf(n));
      expect(groups).toEqual(['Built-in Teams', 'Custom Teams', 'Team sets', 'Agents']);
      const menu = p.text();
      expect(menu.slice(menu.indexOf('Agents'))).toContain('Solo agent');
      for (const visible of ['Aristotle', 'Team copy']) expect(menu).toContain(visible);
      for (const hidden of ['Colleague private', 'Retired agent'])
        expect(menu).not.toContain(hidden);
      expect(menu).not.toContain('Manage agents');
      await p.click(/^SSolo agent$/);
      expect(p.assignee.value).toEqual({ type: 'agent', id: 'a1' });
      expect(p.range.value).toEqual({ from: 'grounding', to: 'validation' });
      expect(p.button(/Stages/)?.props.disabled).toBe(false);
    } finally {
      p.unmount();
    }
  });

  it('shows the Stages pill when nothing is picked', async () => {
    const p = await mountPicker();
    try {
      expect(p.button(/Stages/)).toBeTruthy();
    } finally {
      p.unmount();
    }
  });
});

describe('new agent model picker', () => {
  it('uses the selected harness models and resets the model when the harness changes', async () => {
    const t = await mount(
      AgentsView,
      () => ({ create: true }),
      '/agents/new',
      (c) => {
        c.setQueryData(keys.runtimes('w'), [
          {
            owner_id: 'u1',
            status: 'online',
            harness: 'claude',
            models: [{ value: 'opus', label: 'Opus' }],
          },
          {
            owner_id: 'u1',
            status: 'online',
            harness: 'codex',
            models: [{ value: 'gpt', label: 'GPT' }],
          },
        ]);
      },
    );
    try {
      await t.click(/Start blank/);
      const picker = () =>
        t.nodes().find((n) => String(n.props['aria-label']).startsWith('Model:'))!;
      expect(picker().props['aria-label']).toBe('Model: Default');
      await (picker().props.onClick as () => Promise<void>)();
      await nextTick();
      await t.click(/^Opus$/);
      expect(picker().props['aria-label']).toBe('Model: Opus');
      const harness = t.nodes().find((n) => n.props['aria-label'] === 'Harness: Claude Code')!;
      expect(harness).toBeDefined();
      await (harness.props.onClick as () => Promise<void>)();
      await nextTick();
      await t.click(/^Codex$/);
      expect(picker().props['aria-label']).toBe('Model: Default');
      await (picker().props.onClick as () => Promise<void>)();
      await nextTick();
      expect(t.text()).toContain('GPT');
      expect(t.text()).not.toContain('Opus');
    } finally {
      t.unmount();
    }
  });
});

describe('TeamDetail', () => {
  it('updates the researcher’s built-in Team member CLI', async () => {
    const base = manifest('Hypotheses Team', 'hypotheses');
    const team = {
      ...base,
      leader: {
        ...base.leader,
        agent: { ...base.leader.agent, models: { claude: 'sonnet', codex: 'gpt' } },
      },
    };
    const payload = {
      id: 't1',
      uploaded_by: null,
      visibility: 'workspace',
      manifest: team,
      my_agents: { lead: { id: 'my-leader', name: 'lead', harness: 'claude', model: 'sonnet' } },
    };
    let changed = false;
    const fetch = vi.fn(async (path: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'PATCH') changed = true;
      const result = String(path).startsWith('/api/teams?')
        ? [storeTeam('t1', 'Hypotheses Team', 'hypotheses', 'shipped')]
        : String(path).startsWith('/api/agents') && init?.method !== 'PATCH'
          ? []
          : {
              ...payload,
              my_agents: {
                lead: { ...payload.my_agents.lead, harness: changed ? 'codex' : 'claude' },
              },
            };
      return new Response(JSON.stringify(result), { status: 200 });
    });
    vi.stubGlobal('fetch', fetch);
    const t = await mount(
      TeamDetail,
      () => ({ id: 't1' }),
      '/teams/t1',
      (client) => {
        client.setQueryData(keys.team('w', 't1'), payload);
      },
    );
    try {
      const header = () => t.nodes().find((n) => n.props.class === 'agent-meta')!;
      expect(textOf(header())).toContain('Claude Code');
      expect(textOf(header())).not.toContain('Codex');
      const trigger = t.nodes().find((n) => n.props['aria-label'] === 'CLI: Claude Code');
      expect(trigger).toBeTruthy();
      await (trigger!.props.onClick as () => Promise<void>)();
      await nextTick();
      const codex = t.nodes().find((n) => n.props['aria-label'] === 'Codex');
      expect(codex).toBeTruthy();
      (codex!.props.onClick as () => void)();
      await vi.waitFor(() =>
        expect(
          fetch.mock.calls.some(
            (call: any[]) =>
              call[0] === '/api/agents/my-leader' &&
              JSON.parse(String(call[1]?.body)).harness === 'codex',
          ),
        ).toBe(true),
      );
      await vi.waitFor(() => expect(textOf(header())).toContain('Codex'));
      expect(textOf(header())).not.toContain('Claude Code');
    } finally {
      t.unmount();
    }
  });

  it('lets the Team owner edit the profile and choose workspace access', async () => {
    const fetch = vi.fn(
      async (path: RequestInfo | URL, init?: RequestInit) =>
        new Response(
          init?.method === 'PATCH'
            ? '{"id":"t2"}'
            : String(path).startsWith('/api/teams?')
              ? JSON.stringify([storeTeam('t2', 'My Team', 'grounding', 'uploaded', true)])
              : JSON.stringify({
                  id: 't2',
                  uploaded_by: 'u1',
                  visibility: 'private',
                  manifest: manifest('My Team', 'grounding'),
                }),
          { status: 200 },
        ),
    );
    vi.stubGlobal('fetch', fetch);
    const t = await mount(TeamDetail, () => ({ id: 't2' }), '/teams/t2');
    try {
      await t.click(/^Settings$/);
      await t.fill('Team name', 'Updated Team');
      await t.fill('Team description', 'Updated description');
      await t.submit();
      expect(fetch).toHaveBeenCalledWith(
        '/api/teams/t2',
        expect.objectContaining({
          method: 'PATCH',
          body: JSON.stringify({ name: 'Updated Team', description: 'Updated description' }),
        }),
      );
      await t.click(/^Access$/);
      const workspace = t.nodes().find((n) => n.tag === 'input' && n.props.value === 'workspace')!;
      (workspace.props['onUpdate:modelValue'] as (value: string) => void)('workspace');
      await nextTick();
      await t.submit();
      expect(fetch).toHaveBeenCalledWith(
        '/api/teams/t2',
        expect.objectContaining({
          method: 'PATCH',
          body: JSON.stringify({ visibility: 'workspace' }),
        }),
      );
    } finally {
      t.unmount();
    }
  });
  it('keeps a colleague’s shared Team read-only', async () => {
    const t = await mount(
      TeamDetail,
      () => ({ id: 't2' }),
      '/teams/t2',
      (c) => {
        c.setQueryData(keys.team('w', 't2'), {
          id: 't2',
          uploaded_by: 'u2',
          visibility: 'workspace',
          manifest: manifest('My Team', 'grounding'),
        });
      },
    );
    try {
      expect(t.button(/^Delete Team$/)).toBeUndefined();
      await t.click(/^Settings$/);
      expect(t.nodes().find((n) => n.props['aria-label'] === 'Team name')?.props.readonly).toBe(
        true,
      );
      expect(t.button(/^Save changes$/)).toBeUndefined();
      await t.click(/^Access$/);
      expect(
        t
          .nodes()
          .filter((n) => n.tag === 'input')
          .every((n) => n.props.disabled === true),
      ).toBe(true);
    } finally {
      t.unmount();
    }
  });
  it('keeps scientific settings under General and provides Access settings', async () => {
    const t = await mount(TeamDetail, () => ({ id: 't2' }), '/teams/t2');
    try {
      await t.click(/^Settings$/);
      expect(t.text()).toContain('Details');
      expect(t.text()).toContain('Review criteria');
      expect(t.button(/^General$/)).toBeTruthy();
      expect(t.button(/^Access$/)).toBeTruthy();
      await t.click(/^Access$/);
      expect(t.text()).toContain('Who can use this Team');
    } finally {
      t.unmount();
    }
  });
  it('shows only this Team’s studies in searchable, collapsible work groups', async () => {
    const card = (id: string, title: string, team: string, board_column = 'grounding') => ({
      id,
      title,
      number: id === 'c1' ? 1 : 2,
      question: '',
      board_column,
      state: 'working',
      flag: '',
      ending: '',
      updated_at: '2026-10-25T10:00:00Z',
      teams: { grounding: { pack_id: team } },
    });
    const t = await mount(
      TeamDetail,
      () => ({ id: 't2' }),
      '/teams/t2',
      (c) => {
        c.setQueryData(keys.cards('w'), [
          card('c1', 'Selected study', 't2'),
          card('c2', 'Other study', 't1'),
        ]);
        c.setQueryData(keys.workspace('w'), { display_prefix: 'LAB' });
      },
    );
    try {
      await t.click(/^Work$/);
      expect(t.text()).toContain('Selected study');
      expect(t.text()).toContain('LAB-1');
      expect(t.text()).not.toContain('Other study');
      await t.fill('Search studies', 'missing');
      expect(t.text()).not.toContain('Selected study');
      await t.fill('Search studies', 'LAB-1');
      expect(t.text()).toContain('Selected study');
      await t.click(/^Grounding/);
      expect(t.text()).not.toContain('Selected study');
    } finally {
      t.unmount();
    }
  });
  it('inspects the selected agent in the team canvas', async () => {
    const t = await mount(TeamDetail, () => ({ id: 't2' }), '/teams/t2');
    try {
      expect(
        t.nodes().filter((n) => n.tag === 'button' && n.props['aria-pressed'] !== undefined),
      ).toHaveLength(3);
      expect(
        t
          .nodes()
          .filter((n) => n.props.role === 'tab')
          .map((n) => n.text),
      ).not.toContain('Agents');
      const inspector = () => t.nodes().find((n) => n.props['aria-label'] === 'Agent details')!;
      const textOf = (n: Node): string => n.text + n.children.map(textOf).join('');
      expect(textOf(inspector())).toContain('lead role');
      await t.click(/scout/);
      expect(textOf(inspector())).toContain('scout role');
      expect(textOf(inspector())).toContain('Edit skills');
    } finally {
      t.unmount();
    }
  });
  it('allows only an editable Team to choose an icon and saves it', async () => {
    const fetch = vi.fn(
      async (_path: RequestInfo | URL, init?: RequestInit) =>
        new Response(init?.method === 'PATCH' ? '{"id":"t2","avatar_url":"emoji:🦉"}' : '{}', {
          status: 200,
        }),
    );
    vi.stubGlobal('fetch', fetch);
    const builtIn = await mount(TeamDetail, () => ({ id: 't1' }), '/teams/t1');
    expect(builtIn.nodes().some((n) => n.props['aria-label'] === 'Change avatar')).toBe(false);
    builtIn.unmount();
    const own = await mount(TeamDetail, () => ({ id: 't2' }), '/teams/t2');
    try {
      const edit = own.nodes().find((n) => n.props['aria-label'] === 'Change avatar');
      expect(edit).toBeDefined();
      (edit!.props.onClick as () => void)();
      await nextTick();
      const emoji = own.nodes().find((n) => n.props['aria-label'] === '🦉');
      expect(emoji).toBeDefined();
      (emoji!.props.onClick as () => void)();
      await nextTick();
      expect(fetch).toHaveBeenCalledWith(
        '/api/teams/t2',
        expect.objectContaining({ method: 'PATCH', body: '{"avatar_url":"emoji:🦉"}' }),
      );
    } finally {
      own.unmount();
    }
  });
  it('shows the server refusal when a Team icon cannot be saved', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('{"error":"Team is in use"}', { status: 409 })),
    );
    const own = await mount(TeamDetail, () => ({ id: 't2' }), '/teams/t2');
    try {
      (
        own.nodes().find((n) => n.props['aria-label'] === 'Change avatar')!.props
          .onClick as () => void
      )();
      await nextTick();
      (own.nodes().find((n) => n.props['aria-label'] === '🦉')!.props.onClick as () => void)();
      await new Promise((resolve) => setTimeout(resolve, 0));
      await nextTick();
      expect(own.text()).toContain('Team is in use');
    } finally {
      own.unmount();
    }
  });
  it('shows Duplicate and no edit for a built-in Team', async () => {
    const t = await mount(TeamDetail, () => ({ id: 't1' }), '/teams/t1');
    try {
      expect(t.text()).toContain('Hypotheses Team');
      expect(t.button(/^Duplicate$/)).toBeTruthy();
      expect(t.text()).not.toContain('Add Team');
      expect(t.text()).not.toMatch(/Delete/);
      expect(t.text()).toContain('scout');
      expect(t.text()).not.toContain('Edit skills');
    } finally {
      t.unmount();
    }
  });

  it('prefills the duplicate with the Team’s name and (duplicate)', async () => {
    const t = await mount(TeamDetail, () => ({ id: 't1' }), '/teams/t1');
    try {
      await t.click(/^Duplicate$/);
      expect(t.input('Name of the duplicate')?.value).toBe('Hypotheses Team (duplicate)');
      expect(t.text()).not.toMatch(/copy/i);
    } finally {
      t.unmount();
    }
  });

  it('keeps editing for a researcher’s own Team', async () => {
    const t = await mount(TeamDetail, () => ({ id: 't2' }), '/teams/t2');
    try {
      expect(t.button(/^Duplicate$/)).toBeFalsy();
      await t.click(/scout/);
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

function seedStudy(client: QueryClient, extra: Record<string, unknown> = {}) {
  client.setQueryData(keys.workspace('w'), { name: 'Lab' });
  client.setQueryData(keys.cards('w'), []);
  client.setQueryData(keys.runtimes('w'), []);
  client.setQueryData(keys.researches('w'), []);
  for (const key of [keys.comments, keys.events, keys.runs, keys.ratings])
    client.setQueryData(key('w', 'c'), []);
  client.setQueryData(keys.records('w', 'c', 'grounding'), []);
  client.setQueryData(keys.card('w', 'c'), {
    id: 'c',
    number: 1,
    title: 'Study',
    question: 'Question',
    owner_id: 'u1',
    board_column: 'grounding',
    state: 'working',
    flag: '',
    ending: '',
    block_kind: '',
    hold_text: '',
    scope_from: 'grounding',
    scope_to: 'validation',
    round: 1,
    redos: 0,
    teams: {},
    off_label: [],
    attachments: [],
    services: [],
    created_at: '2026-10-25T10:00:00Z',
    updated_at: '2026-10-25T10:00:00Z',
    ...extra,
  });
}

describe('no Follows from', () => {
  it('has no Follows from in the new-study window', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('[]', { status: 200 })),
    );
    const t = await mount(CreateStudy, () => ({}), '/', seedStudy);
    try {
      expect(t.text()).not.toContain('Follows from');
    } finally {
      t.unmount();
    }
  });

  it('shows a duplicate without a follow button', async () => {
    const fetch = vi.fn(
      async (_path: RequestInfo | URL, init?: RequestInit) =>
        new Response(
          init?.method === 'POST'
            ? '{"error":"an active study already asks this: #4","card_id":"c4","number":4}'
            : '[]',
          { status: init?.method === 'POST' ? 409 : 200 },
        ),
    );
    vi.stubGlobal('fetch', fetch);
    const t = await mount(CreateStudy, () => ({}), '/', seedStudy);
    try {
      await t.fill('Describe your study', 'Question');
      await t.submit();
      expect(t.text()).toMatch(/An active study already asks this: \S*4/);
      expect(t.text()).not.toContain('Follows from');
      expect(t.button(/follow/i)).toBeUndefined();
    } finally {
      t.unmount();
    }
  });

  it('renders a study page without follows_from', async () => {
    const t = await mount(
      StudyDetail,
      () => ({ id: 'c' }),
      '/',
      (c) => seedStudy(c),
    );
    try {
      expect(t.text()).not.toContain('Follows from');
    } finally {
      t.unmount();
    }
  });
});

describe('hidden services, no study variables', () => {
  it('has no Study variables in the new-study window and posts none', async () => {
    const fetch = vi.fn(
      async (_path: RequestInfo | URL, init?: RequestInit) =>
        new Response(init?.method === 'POST' ? '{}' : '[]', {
          status: init?.method === 'POST' ? 201 : 200,
        }),
    );
    vi.stubGlobal('fetch', fetch);
    const t = await mount(CreateStudy, () => ({}), '/', seedStudy);
    try {
      expect(t.text()).toContain(
        'Up to 100 files, 5 GB each, 10 GB in all; archives are unpacked for the agents',
      );
      expect(t.text()).not.toContain('Study variables');
      expect(t.button(/^Add variable$/)).toBeUndefined();
      await t.fill('Describe your study', 'Question');
      await t.submit();
      const posted = fetch.mock.calls.find((call) => call[0] === '/api/cards');
      expect(JSON.parse(String(posted![1]?.body))).not.toHaveProperty('env');
    } finally {
      t.unmount();
    }
  });

  it('has no Study variables on the study page', async () => {
    const t = await mount(
      StudyDetail,
      () => ({ id: 'c' }),
      '/',
      (c) =>
        seedStudy(c, {
          env: { DATASET: 'study.csv' },
          attachments: [{ id: 'a', name: 'data.csv', size_bytes: 3 }],
        }),
    );
    try {
      await t.click(/^Properties$/);
      expect(t.text()).toContain('Attached files');
      expect(t.text()).not.toContain('Study variables');
      expect(t.text()).not.toContain('DATASET');
    } finally {
      t.unmount();
    }
  });

  it.each([undefined, []])('omits the services note when services are %s', async (services) => {
    const t = await mount(
      StudyDetail,
      () => ({ id: 'c' }),
      '/',
      (c) => seedStudy(c, { services }),
    );
    try {
      await t.click(/^Properties$/);
      expect(t.text()).not.toContain('This study has hidden services:');
    } finally {
      t.unmount();
    }
  });

  it('names the hidden services and shows the isolation note', async () => {
    const t = await mount(
      StudyDetail,
      () => ({ id: 'c' }),
      '/',
      (c) =>
        seedStudy(c, {
          services: [{ name: 'grader' }, { name: 'database' }],
        }),
    );
    try {
      await t.click(/^Properties$/);
      expect(t.text()).toContain(
        "This study has hidden services: grader, database. Hidden services can't be fully isolated on every machine; don't use them to keep secrets.",
      );
    } finally {
      t.unmount();
    }
  });
});

describe('the one-row New study window', () => {
  const posting = () =>
    vi.fn(
      async (_path: RequestInfo | URL, init?: RequestInit) =>
        new Response(init?.method === 'POST' ? '{}' : '[]', {
          status: init?.method === 'POST' ? 201 : 200,
        }),
    );
  const soloLine =
    'Solo agent works every stage alone. No auditor checks its work; Jev still scores each stage.';

  it('preserves the manual draft while switching and sends the agent request separately', async () => {
    const fetch = posting();
    vi.stubGlobal('fetch', fetch);
    const t = await mount(CreateStudy, () => ({}), '/', seedStudy);
    try {
      await t.fill('Study title', 'Manual title');
      await t.fill('Describe your study', 'Manual question');
      expect(t.button(/^Switch to Agent$/)?.props.disabled).toBe(true);
      await t.click(/^Assignees$/);
      await t.click(/^SSolo agent$/);
      expect(t.button(/^Switch to Agent$/)?.props.disabled).toBe(false);
      await t.click(/^Switch to Agent$/);
      expect(t.text()).toContain('Create with agent');
      expect(t.text()).not.toContain('Create another');
      expect(
        t.nodes().some((node) => node.props['aria-label'] === 'Created by agent: Solo agent'),
      ).toBe(true);
      expect(t.button(/^Solo agent$/)).toBeUndefined();
      await t.fill('Describe the study for the agent', 'Draft a study about sleep');
      await t.click(/^Switch to Manual$/);
      expect(t.text()).toContain('Switch to Agent');
      expect(t.input('Study title')?.value).toBe('Manual title');
      await t.click(/^Switch to Agent$/);
      await t.submit();
      const posted = fetch.mock.calls.find((call) => call[0] === '/api/study-drafts');
      expect(posted).toBeTruthy();
      expect(JSON.parse(String(posted![1]?.body))).toMatchObject({
        agent_id: 'a1',
        prompt: 'Draft a study about sleep',
      });
      expect(fetch.mock.calls.some((call) => call[0] === '/api/cards')).toBe(false);
    } finally {
      t.unmount();
    }
  });

  it('does not switch to agent mode when a Team is selected', async () => {
    vi.stubGlobal('fetch', posting());
    const t = await mount(CreateStudy, () => ({}), '/', seedStudy);
    try {
      await t.click(/^Assignees$/);
      await t.click(/^Hypotheses Team/);
      expect(t.button(/^Switch to Agent$/)?.props.disabled).toBe(true);
    } finally {
      t.unmount();
    }
  });

  it('holds every chip on one row, in order', async () => {
    vi.stubGlobal('fetch', posting());
    const t = await mount(CreateStudy, () => ({}), '/', seedStudy);
    try {
      const row = t.nodes().find((n) => n.props['aria-label'] === 'Study properties');
      expect(row).toBeTruthy();
      const text = textOf(row!);
      const order = ['Assignees', 'Stages', 'Start date', 'Due date', 'No project'];
      const at = order.map((label) => text.indexOf(label));
      expect(
        at.every((i) => i >= 0),
        text,
      ).toBe(true);
      expect([...at].sort((a, b) => a - b)).toEqual(at);
      for (const gone of ['Study variables', 'Follows from', 'Reporting', 'Pre-registered'])
        expect(t.text()).not.toContain(gone);
    } finally {
      t.unmount();
    }
  });

  it('posts the selected agent for a solo study', async () => {
    const fetch = posting();
    vi.stubGlobal('fetch', fetch);
    const t = await mount(CreateStudy, () => ({}), '/', seedStudy);
    try {
      expect(t.text()).not.toContain(soloLine);
      // The pills carry an avatar letter before the name; menu items don't.
      await t.click(/^Assignees$/);
      await t.click(/^SSolo agent$/);
      expect(t.button(/^CLIs$/)).toBeUndefined();
      expect(t.text()).not.toContain(soloLine);
      await t.click(/SSolo agent/);
      await t.click(/^Hypotheses Team/);
      expect(t.text()).not.toContain(soloLine);
      await t.click(/Hypotheses Team/);
      await t.click(/^SSolo agent$/);
      await t.fill('Describe your study', 'Question');
      await t.submit();
      const posted = fetch.mock.calls.find((call) => call[0] === '/api/cards');
      const body = JSON.parse(String(posted![1]?.body));
      expect(body.assignee).toEqual({ type: 'agent', id: 'a1' });
      expect(body.scope).toEqual({ from: 'grounding', to: 'validation' });
    } finally {
      t.unmount();
    }
  });

  it('creates a Team study without a separate CLI override', async () => {
    const fetch = posting();
    vi.stubGlobal('fetch', fetch);
    const t = await mount(CreateStudy, () => ({}), '/', seedStudy);
    try {
      await t.fill('Describe your study', 'Question');
      await t.submit();
      const posted = fetch.mock.calls.find((call) => call[0] === '/api/cards');
      const body = JSON.parse(String(posted![1]?.body));
      expect(body.study_cli).toBeUndefined();
      expect(body.auditor_cli).toBeUndefined();
      expect(body.machine).toBeUndefined();
    } finally {
      t.unmount();
    }
  });
});

describe('a study worked by one agent', () => {
  it('names the agent with No auditor beside it', async () => {
    const t = await mount(
      StudyDetail,
      () => ({ id: 'c' }),
      '/',
      (c) => seedStudy(c, { assignee_type: 'agent', assignee_id: 'a1', teams: {} }),
    );
    try {
      await t.click(/^Properties$/);
      expect(t.text()).toContain('Solo agent');
      expect(t.text()).toContain('No auditor');
    } finally {
      t.unmount();
    }
  });

  it('shows no such note for a Team', async () => {
    const t = await mount(
      StudyDetail,
      () => ({ id: 'c' }),
      '/',
      (c) =>
        seedStudy(c, {
          assignee_type: 'team',
          assignee_id: null,
          teams: { grounding: { pack_id: 't2', name: 'My Team', stage: 'grounding' } },
        }),
    );
    try {
      await t.click(/^Properties$/);
      expect(t.text()).toContain('My Team');
      expect(t.text()).not.toContain('No auditor');
    } finally {
      t.unmount();
    }
  });
});

describe('study right-pane surfaces', () => {
  it('shows a pending Jev line only for a successfully read review in the active non-legacy round', async () => {
    let failReview!: () => void;
    const pending = new Promise<Response>((resolve) => {
      failReview = () => resolve(Response.json({ error: 'Review unavailable' }, { status: 503 }));
    });
    vi.stubGlobal('fetch', (path: string) =>
      path.endsWith('/review') ? pending : Promise.resolve(Response.json([])),
    );
    let client!: QueryClient;
    const t = await mount(
      StudyDetail,
      () => ({ id: 'c' }),
      '/',
      (c) => {
        client = c;
        seedStudy(c);
        c.setQueryData(keys.runs('w', 'c'), [
          {
            id: 'lead',
            board_column: 'grounding',
            round: 1,
            role: 'leader',
            harness: 'claude',
            status: 'completed',
            created_at: '2026-11-03T10:00:00Z',
            completed_at: '2026-11-03T10:01:00Z',
          },
        ]);
      },
    );
    try {
      expect(t.text()).not.toContain('reviewing…');
      failReview();
      await vi.waitFor(() => expect(t.text()).toContain('Review unavailable'));
      expect(t.text()).not.toContain('reviewing…');
      client.setQueryData([...keys.recordsAll('w', 'c'), 'review'], []);
      await vi.waitFor(() => expect(t.text()).toContain('Jev · Grounding, try 1 · reviewing…'));
      const cardKey = keys.card('w', 'c');
      const working = client.getQueryData<Record<string, unknown>>(cardKey)!;
      for (const change of [
        { state: 'blocked' },
        { state: 'done' },
        { ending: 'stopped' },
        { flag: 'escalated' },
        { legacy_column: 'grounding' },
        { round: 2 },
      ]) {
        client.setQueryData(cardKey, working);
        await vi.waitFor(() => expect(t.text()).toContain('reviewing…'));
        client.setQueryData(cardKey, { ...working, ...change });
        await vi.waitFor(() =>
          expect(t.text(), JSON.stringify(change)).not.toContain('reviewing…'),
        );
      }
    } finally {
      t.unmount();
    }
  });

  it('opens the chooser and shows properties, execution and files one at a time', async () => {
    const t = await mount(StudyDetail, () => ({ id: 'c' }), '/', seedStudy);
    try {
      expect(t.text()).toContain('Open a surface');
      expect(t.nodes().some((n) => n.props['aria-label'] === 'Toggle details')).toBe(false);
      expect(t.text()).not.toContain('Grounding criteria');
      expect(t.text()).not.toContain('Details');
      for (const label of ['Properties', 'Timeline', 'Files']) {
        await t.click(new RegExp(`^${label}$`));
        if (label === 'Properties') {
          expect(t.text()).toContain('Owner');
          expect(t.text()).toContain('Due date');
          expect(t.text()).toContain('Created');
          expect(t.text()).toContain('Updated');
          expect(t.text()).not.toContain('Details');
        }
        if (label === 'Timeline') {
          expect(t.text()).toContain('No runs yet.');
          expect(t.text()).toContain('Spent');
          expect(t.text()).not.toContain('Open timeline');
        }
        expect(t.nodes().some((n) => n.props['aria-label'] === 'Back to surfaces')).toBe(true);
        expect(t.nodes().some((n) => n.props.class === 'surface-title')).toBe(false);
        const back = t.nodes().find((n) => n.props['aria-label'] === 'Back to surfaces')!;
        (back.props.onClick as () => void)();
        await nextTick();
        expect(t.text()).toContain('Open a surface');
      }
      const close = t.nodes().find((n) => n.props['aria-label'] === 'Close pane')!;
      (close.props.onClick as () => void)();
      await nextTick();
      expect(t.nodes().some((n) => n.props['aria-label'] === 'Toggle details')).toBe(true);
    } finally {
      t.unmount();
    }
  });
});

it('lists each saved study output separately with its stage and run provenance', async () => {
  const t = await mount(
    StudyDetail,
    () => ({ id: 'c' }),
    '/',
    (client) => {
      seedStudy(client);
      client.setQueryData(
        [...keys.recordsAll('w', 'c'), 'kind', 'output'],
        [
          {
            id: 'a',
            key: 'o1',
            board_column: 'grounding',
            round: 1,
            role: 'agent',
            agent_run_id: 'run-a',
            created_at: '2026-10-30T10:00:00Z',
            body: { name: 'result.csv', size_bytes: 4 },
          },
          {
            id: 'b',
            key: 'o2',
            board_column: 'validation',
            round: 2,
            role: 'agent',
            agent_run_id: 'run-b',
            created_at: '2026-10-30T11:00:00Z',
            body: { name: 'result.csv', size_bytes: 8 },
          },
        ],
      );
    },
  );
  try {
    await t.click(/^Files$/);
    expect(
      t.nodes().filter((n) => n.tag === 'button' && textOf(n).includes('result.csv')),
    ).toHaveLength(2);
    expect(t.text()).toContain('Grounding');
    expect(t.text()).toContain('Validation');
    expect(t.text()).toContain('run-a');
    expect(t.text()).toContain('run-b');
  } finally {
    t.unmount();
  }
});

describe('a Workflow study', () => {
  it('cannot be created without an attached file', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('[]', { status: 200 })),
    );
    const t = await mount(
      CreateStudy,
      () => ({ workflow: 'mendota-ice-phenology', assignee: { type: 'agent', id: 'a1' } }),
      '/',
      seedStudy,
    );
    try {
      await t.fill('Describe your study', 'Question');
      expect(t.button(/^Create study/)?.props.disabled).toBe(true);
      expect(t.text()).toContain(
        'A Workflow runs its method on your data: attach at least one file.',
      );
    } finally {
      t.unmount();
    }
  });
});
