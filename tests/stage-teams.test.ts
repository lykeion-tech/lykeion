import { afterEach, describe, expect, it, vi } from 'vitest';
import { createRenderer, createSSRApp, h, nextTick, ref, ssrContextKey, type Component } from 'vue';
import { renderToString } from '@vue/server-renderer';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import AssignPicker from '../apps/dashboard/src/components/AssignPicker.vue';
import TeamDetail from '../apps/dashboard/src/views/TeamDetail.vue';
import TeamsView from '../apps/dashboard/src/views/TeamsView.vue';
import CreateTask from '../apps/dashboard/src/views/CreateTask.vue';
import TaskDetail from '../apps/dashboard/src/views/TaskDetail.vue';
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
  contains(): boolean;
  focus(): void;
  showModal(): void;
  getRootNode(): null;
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
    contains: () => true,
    focus() {},
    showModal() {},
    getRootNode: () => null,
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

async function mountPicker(agents = false, lastSet: Picked | null = null) {
  const assignee = ref<Picked | null>(null);
  const range = ref<Range>({ from: 'grounding', to: 'validation' });
  const mounted = await mount(AssignPicker, () => ({
    modelValue: assignee.value,
    'onUpdate:modelValue': (v: Picked | null) => (assignee.value = v),
    range: range.value,
    'onUpdate:range': (v: Range) => (range.value = v),
    agents,
    lastSet,
  }));
  return { ...mounted, assignee, range };
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

  it('shows the Stages pill when nothing is picked', async () => {
    const p = await mountPicker();
    try {
      expect(p.button(/Grounding → Validation/)).toBeTruthy();
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

  it('prefills the duplicate with the Team’s name and (duplicate)', async () => {
    const t = await mount(TeamDetail, () => ({ id: 't1' }), '/teams/t1');
    try {
      await t.click(/Duplicate this Team/);
      expect(t.input('Name of the duplicate')?.value).toBe('Hypotheses Team (duplicate)');
      expect(t.text()).not.toMatch(/copy/i);
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

async function preregister(t: { fill: (label: string, value: string) => Promise<void> }) {
  await t.fill('Result name 1', 'Accurate');
  await t.fill('Result condition 1', 'accuracy >= 0.9');
}

describe('no Follows from', () => {
  it('has no Follows from in the new-study window', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { status: 200 })));
    const t = await mount(CreateTask, () => ({}), '/', seedStudy);
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
    const t = await mount(CreateTask, () => ({}), '/', seedStudy);
    try {
      await t.fill('Describe your study', 'Question');
      await preregister(t);
      await t.submit();
      expect(t.text()).toMatch(/An active study already asks this: \S*4/);
      expect(t.text()).not.toContain('Follows from');
      expect(t.button(/follow/i)).toBeUndefined();
    } finally {
      t.unmount();
    }
  });

  it('renders a study page without follows_from', async () => {
    const t = await mount(TaskDetail, () => ({ id: 'c' }), '/', (c) => seedStudy(c));
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
    const t = await mount(CreateTask, () => ({}), '/', seedStudy);
    try {
      expect(t.text()).toContain(
        'Up to 100 files, 5 GB each, 10 GB in all; archives are unpacked for the agents',
      );
      expect(t.text()).not.toContain('Study variables');
      expect(t.button(/^Add variable$/)).toBeUndefined();
      await t.fill('Describe your study', 'Question');
      await preregister(t);
      await t.submit();
      const posted = fetch.mock.calls.find((call) => call[0] === '/api/cards');
      expect(JSON.parse(String(posted![1]?.body))).not.toHaveProperty('env');
    } finally {
      t.unmount();
    }
  });

  it('has no Study variables on the study page', async () => {
    const t = await mount(
      TaskDetail,
      () => ({ id: 'c' }),
      '/',
      (c) =>
        seedStudy(c, {
          env: { DATASET: 'study.csv' },
          attachments: [{ id: 'a', name: 'data.csv', size_bytes: 3 }],
        }),
    );
    try {
      expect(t.text()).toContain('Attached files');
      expect(t.text()).not.toContain('Study variables');
      expect(t.text()).not.toContain('DATASET');
    } finally {
      t.unmount();
    }
  });

  it.each([undefined, []])('omits the services note when services are %s', async (services) => {
    const t = await mount(
      TaskDetail,
      () => ({ id: 'c' }),
      '/',
      (c) => seedStudy(c, { services }),
    );
    try {
      expect(t.text()).not.toContain('This study has hidden services:');
    } finally {
      t.unmount();
    }
  });

  it('names the hidden services and shows the isolation note', async () => {
    const t = await mount(
      TaskDetail,
      () => ({ id: 'c' }),
      '/',
      (c) =>
        seedStudy(c, {
          services: [{ name: 'grader' }, { name: 'database' }],
        }),
    );
    try {
      expect(t.text()).toContain(
        "This study has hidden services: grader, database. Hidden services can't be fully isolated on every machine; don't use them to keep secrets.",
      );
    } finally {
      t.unmount();
    }
  });
});

describe('pre-registered results', () => {
  const posting = (status = 201, body = '{}') =>
    vi.fn(
      async (_path: RequestInfo | URL, init?: RequestInit) =>
        new Response(init?.method === 'POST' ? body : '[]', {
          status: init?.method === 'POST' ? status : 200,
        }),
    );
  const disabled = (t: Awaited<ReturnType<typeof mount>>) =>
    t.button(/^Create study/)?.props.disabled;

  it('asks for a result when the study runs Experimentation, and posts it', async () => {
    const fetch = posting();
    vi.stubGlobal('fetch', fetch);
    const t = await mount(CreateTask, () => ({}), '/', seedStudy);
    try {
      expect(t.text()).toContain('Pre-registered results');
      expect(t.input('Result name 1')?.value).toBe('');
      expect(t.input('Result name 2')).toBeUndefined();
      await t.fill('Describe your study', 'Question');
      expect(disabled(t)).toBe(true);
      await t.fill('Result name 1', 'Accurate');
      expect(disabled(t)).toBe(true);
      await t.fill('Result condition 1', 'accuracy >= 0.9 and not loss > 1');
      expect(disabled(t)).toBe(false);
      expect(t.text()).toContain('accuracy, loss');
      await t.click(/^Add result$/);
      expect(t.input('Result name 2')?.value).toBe('');
      await t.submit();
      const posted = fetch.mock.calls.find((call) => call[0] === '/api/cards');
      expect(JSON.parse(String(posted![1]?.body)).predicates).toEqual([
        { name: 'Accurate', text: 'accuracy >= 0.9 and not loss > 1' },
      ]);
    } finally {
      t.unmount();
    }
  });

  it('hides the section and sends no results when the scope stops before Experimentation', async () => {
    const fetch = posting();
    vi.stubGlobal('fetch', fetch);
    const t = await mount(CreateTask, () => ({}), '/', seedStudy);
    try {
      await t.click(/Grounding → Validation/);
      await t.change('To stage', 'hypotheses');
      expect(t.text()).not.toContain('Pre-registered results');
      await t.fill('Describe your study', 'Question');
      expect(disabled(t)).toBe(false);
      await t.submit();
      const posted = fetch.mock.calls.find((call) => call[0] === '/api/cards');
      const body = JSON.parse(String(posted![1]?.body));
      expect(body.scope).toEqual({ from: 'grounding', to: 'hypotheses' });
      expect(body.predicates).toBeUndefined();
    } finally {
      t.unmount();
    }
  });

  it('puts the server refusal on the row it names', async () => {
    const message = 'pre-registered result Fast: unexpected token at 5';
    vi.stubGlobal('fetch', posting(400, JSON.stringify({ error: message })));
    const t = await mount(CreateTask, () => ({}), '/', seedStudy);
    try {
      await t.fill('Describe your study', 'Question');
      await preregister(t);
      await t.click(/^Add result$/);
      await t.fill('Result name 2', 'Fast');
      await t.fill('Result condition 2', 'time <');
      await t.submit();
      expect(t.alert('Result 2')).toBe(message);
      expect(t.alert('Result 1')).toBeUndefined();
    } finally {
      t.unmount();
    }
  });
});

describe('pre-registered results for a single Team', () => {
  const posting = () =>
    vi.fn(
      async (_path: RequestInfo | URL, init?: RequestInit) =>
        new Response(init?.method === 'POST' ? '{}' : '[]', {
          status: init?.method === 'POST' ? 201 : 200,
        }),
    );
  const single = (column: string) => () => ({
    assignee: { type: 'team', teams: { [column]: 't1' } },
  });

  it('asks for a result when the lone Team works Experimentation, and sends no scope', async () => {
    const fetch = posting();
    vi.stubGlobal('fetch', fetch);
    const t = await mount(CreateTask, single('experimentation'), '/', seedStudy);
    try {
      expect(t.text()).toContain('Pre-registered results');
      await t.fill('Describe your study', 'Question');
      expect(t.button(/^Create study/)?.props.disabled).toBe(true);
      await t.fill('Result name 1', 'Accurate');
      expect(t.button(/^Create study/)?.props.disabled).toBe(true);
      await t.fill('Result condition 1', 'accuracy >= 0.9');
      expect(t.button(/^Create study/)?.props.disabled).toBe(false);
      await t.submit();
      const posted = fetch.mock.calls.find((call) => call[0] === '/api/cards');
      const body = JSON.parse(String(posted![1]?.body));
      expect(body.scope).toBeUndefined();
      expect(body.predicates).toEqual([{ name: 'Accurate', text: 'accuracy >= 0.9' }]);
    } finally {
      t.unmount();
    }
  });

  it('asks for none when the lone Team works Hypotheses', async () => {
    const fetch = posting();
    vi.stubGlobal('fetch', fetch);
    const t = await mount(CreateTask, single('hypotheses'), '/', seedStudy);
    try {
      expect(t.text()).not.toContain('Pre-registered results');
      await t.fill('Describe your study', 'Question');
      expect(t.button(/^Create study/)?.props.disabled).toBe(false);
      await t.submit();
      const posted = fetch.mock.calls.find((call) => call[0] === '/api/cards');
      const body = JSON.parse(String(posted![1]?.body));
      expect(body.scope).toBeUndefined();
      expect(body.predicates).toBeUndefined();
    } finally {
      t.unmount();
    }
  });
});

describe('pre-registered results on the study page', () => {
  it('shows each result with its binding outcome, value, run and the other runs', async () => {
    const t = await mount(
      TaskDetail,
      () => ({ id: 'c' }),
      '/',
      (c) =>
        seedStudy(c, {
          predicates: [
            { name: 'Accurate', text: 'accuracy >= 0.9' },
            { name: 'Fast', text: 'seconds < 10' },
            { name: 'Small', text: 'params < 1000' },
            { name: 'Later', text: 'loss < 1' },
          ],
          preregistration: [
            {
              name: 'Accurate',
              text: 'accuracy >= 0.9',
              outcome: 'held',
              round: 1,
              run: 'x2',
              values: { accuracy: 0.93 },
              others: [{ run: 'x1', values: { accuracy: 0.4 } }],
              reason: '',
            },
            {
              name: 'Fast',
              text: 'seconds < 10',
              outcome: 'missed',
              round: 1,
              run: 'x2',
              values: { seconds: 12 },
              others: [],
              reason: '',
            },
            {
              name: 'Small',
              text: 'params < 1000',
              outcome: 'unconfirmed',
              round: 0,
              run: '',
              values: {},
              others: [],
              reason: 'no run wrote params',
            },
          ],
        }),
    );
    try {
      const text = t.text();
      expect(text).toContain('Pre-registered results');
      expect(text).toContain('accuracy >= 0.9');
      expect(text).toContain('held');
      expect(text).toContain('accuracy = 0.93 in run x2');
      expect(text).toContain('Other runs wrote: accuracy = 0.4 in run x1');
      expect(text).toContain('missed');
      expect(text).toContain('not confirmed yet: no run wrote params');
      expect(text).toContain('Not decided yet');
    } finally {
      t.unmount();
    }
  });
});
