import { afterEach, describe, expect, it, vi } from 'vitest';
import { createRenderer, h, nextTick, ref, ssrContextKey, type Component } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import MachinesView from '../apps/dashboard/src/views/MachinesView.vue';
import SettingsView from '../apps/dashboard/src/views/SettingsView.vue';
import CreateTask from '../apps/dashboard/src/views/CreateTask.vue';
import TaskDetail from '../apps/dashboard/src/views/TaskDetail.vue';
import AgentRunDialog from '../apps/dashboard/src/components/AgentRunDialog.vue';
import { workspaceId } from '../apps/dashboard/src/api/session';
import { keys } from '../apps/dashboard/src/api/keys';
import type { AgentRun, Runtime } from '../apps/dashboard/src/api/types';
const runtime = (extra: Record<string, unknown> = {}) => ({
  id: 'r1',
  daemon_id: 'd1',
  device_name: 'Bench',
  owner_id: 'u1',
  harness: 'claude',
  status: 'offline',
  last_seen_at: '2026-10-25T10:00:00Z',
  active_runs: 0,
  bases: {},
  os: 'linux',
  visibility: 'private',
  shared_with: [],
  ...extra,
});
function seedClient() {
  workspaceId.value = 'w';
  const c = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  });
  c.setQueryData(keys.me, { id: 'u1', email: 'owner@lab.test' });
  c.setQueryData(keys.runtimes('w'), [runtime()]);
  c.setQueryData(keys.members('w'), [
    { user_id: 'u1', name: 'Owner' },
    { user_id: 'u2', name: 'Ada', email: 'ada@lab.test' },
  ]);
  c.setQueryData(keys.workspace('w'), { name: 'Lab' });
  for (const key of [keys.cards, keys.agents, keys.researches]) c.setQueryData(key('w'), []);
  c.setQueryData(keys.teams('w', '', ''), []);
  for (const key of [keys.comments, keys.events, keys.runs, keys.ratings])
    c.setQueryData(key('w', 'c'), []);
  c.setQueryData(keys.records('w', 'c', 'grounding'), []);
  c.setQueryData(keys.card('w', 'c'), {
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
    machine: 'd1',
    machine_label: 'Bench',
    machine_notice: "This study's machine is no longer shared with you; choose another.",
  });
  return c;
}
afterEach(() => {
  workspaceId.value = '';
  vi.unstubAllGlobals();
});
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
  openSharing = true,
) {
  const g = globalThis as { document?: unknown };
  g.document ??= { addEventListener() {}, removeEventListener() {} };
  vi.stubGlobal('window', { innerWidth: 1512, addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal('Document', class {});
  vi.stubGlobal('ShadowRoot', class {});
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  const queryClient = seedClient();
  seed?.(queryClient);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:p(.*)*', component: CreateTask }],
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
    all(root).find(
      (n) =>
        n.tag === 'button' &&
        (label.test(textOf(n).trim()) || label.test(String(n.props['aria-label'] ?? ''))),
    );
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
  const submit = async () => {
    const form = all(root).find((n) => n.tag === 'form');
    await (form!.props.onSubmit as (event: unknown) => Promise<void>)({ preventDefault() {} });
    await nextTick();
  };
  if (openSharing && component === MachinesView && !button(/^Details$/)?.props.disabled)
    await click(/^Details$/);
  return {
    text: () => textOf(root),
    all: () => all(root),
    queryClient,
    button,
    click,
    input,
    fill,
    submit,
    unmount: () => {
      app.unmount();
      queryClient.clear();
    },
  };
}

const platform = 'Shared runs require Linux. On a Mac, register a Linux VM as a separate machine.';
const signIn =
  "Colleagues' agents can read this CLI's sign-in. Share only with people you trust. After you stop sharing, sign out of the CLI and sign in again to cut off any copy.";
const files =
  "This machine's owner can read colleagues' study files stored here. Shared runs are isolated from the owner's private files and other runs' processes.";
const revoke =
  "These colleagues' studies run on this machine. Their waiting and running runs will be cancelled.";
const bodies = (fetch: ReturnType<typeof vi.fn>) =>
  fetch.mock.calls
    .filter((c) => c[1]?.method === 'PUT' || c[1]?.method === 'POST' || c[1]?.method === 'PATCH')
    .map((c) => ({
      path: c[0],
      body: JSON.parse(c[1].body),
      workspace: c[1].headers['X-Workspace-ID'],
    }));
function fetching() {
  const f = vi.fn(async (_p, init) => new Response(init?.method === 'GET' ? '[]' : '{}'));
  vi.stubGlobal('fetch', f);
  return f;
}
async function change(t: Awaited<ReturnType<typeof mount>>, label: string, value: unknown) {
  await t.fill(label, value as string);
}

describe('Shared machines', () => {
  it('links Settings machine rows to their details without an actions menu', async () => {
    fetching();
    const t = await mount(SettingsView, () => ({ section: 'machines' }));
    try {
      const link = t.all().find((n) => n.tag === 'a' && n.props.class === 'machine-link');
      expect(link?.props.href).toBe('/settings?tab=machines&machine=d1');
      expect(textOf(link!)).not.toContain('daemon');
      expect(t.all().find((n) => n.props['aria-label'] === 'Machine actions')).toBeUndefined();
      expect(t.button(/^Add a machine$/)).toBeTruthy();
      await t.click(/^Add a machine$/);
      expect(t.all().find((n) => n.tag === 'dialog')?.props['aria-label']).toBe('Add a machine');
    } finally {
      t.unmount();
    }
  });
  it('places Machines and Updates after Storage in Personal settings', async () => {
    fetching();
    const t = await mount(SettingsView, () => ({ section: 'updates' }));
    try {
      const nav = t.all().find((n) => n.tag === 'nav' && n.props['aria-label'] === 'Settings')!;
      const personal = nav.children.find((n) => n.tag === 'section')!;
      expect(
        all(personal)
          .filter((n) => n.tag === 'button')
          .map(textOf),
      ).toEqual(['Profile', 'Preferences', 'API Tokens', 'Storage', 'Machines', 'Updates']);
      expect(t.text()).toContain('Current version');
    } finally {
      t.unmount();
    }
  });
  it('renders machines within Settings without row actions or detail idle timestamps', async () => {
    fetching();
    const t = await mount(SettingsView, () => ({ section: 'machines', machineId: 'd1' }));
    try {
      expect(t.text()).toContain('Bench');
      expect(t.text()).toContain('Runtimes');
      expect(t.text()).not.toContain('All idle');
      expect(t.all().find((n) => n.tag === 'time')).toBeUndefined();
      expect(t.all().find((n) => n.props['aria-label'] === 'Machine breadcrumb')).toBeUndefined();
      expect(t.all().find((n) => n.props['aria-label'] === 'Machine actions')).toBeUndefined();
    } finally {
      t.unmount();
    }
  });
  it('can retry saving after the runtime refresh fails', async () => {
    const f = vi.fn(async (_path: string, init?: RequestInit) =>
      init?.method === 'GET'
        ? new Response('{"error":"Refresh failed"}', { status: 500 })
        : new Response('{}'),
    );
    vi.stubGlobal('fetch', f);
    const t = await mount(MachinesView, () => ({ machineId: 'd1' }));
    try {
      await change(t, 'Share Claude Code', ['r1']);
      await change(t, 'Share with Ada', ['u2']);
      await t.click(/^Save sharing$/);
      await vi.waitFor(() => expect(t.button(/^Save sharing$/)?.props.disabled).toBe(false));
      await t.click(/^Save sharing$/);
      await vi.waitFor(() => expect(bodies(f)).toHaveLength(2));
    } finally {
      t.unmount();
    }
  });
  it('returns to an unshared draft after saving and stopping sharing', async () => {
    let rows = [runtime()];
    const f = vi.fn(async (path: string, init?: RequestInit) => {
      if (init?.method === 'PUT') rows = [{ ...rows[0]!, ...JSON.parse(String(init.body)) }];
      return new Response(
        init?.method === 'GET' ? JSON.stringify(path === '/api/runtimes' ? rows : []) : '{}',
      );
    });
    vi.stubGlobal('fetch', f);
    const t = await mount(MachinesView, () => ({ machineId: 'd1' }));
    try {
      await change(t, 'Share Claude Code', ['r1']);
      await change(t, 'Share with Ada', ['u2']);
      await t.click(/^Save sharing$/);
      await vi.waitFor(() => expect(t.button(/^Stop sharing$/)?.props.disabled).toBe(false));
      await t.click(/^Stop sharing$/);
      await vi.waitFor(() => expect(t.button(/^Save sharing$/)?.props.disabled).toBe(false));
      const count = bodies(f).length;
      await t.click(/^Save sharing$/);
      expect(bodies(f)).toHaveLength(count);
      expect(rows[0]?.shared_with).toEqual([]);
    } finally {
      t.unmount();
    }
  });
  it.each(['colleagues', 'runtimes'])(
    'stops sharing when all selected %s are cleared',
    async (field) => {
      const f = fetching();
      const t = await mount(
        MachinesView,
        () => ({ machineId: 'd1' }),
        '/',
        (c) => c.setQueryData(keys.runtimes('w'), [runtime({ shared_with: ['u2'] })]),
      );
      try {
        await change(t, 'Share', 'workspace');
        expect(t.button(/^Save sharing$/)).toBeTruthy();
        expect(t.button(/^Stop sharing$/)).toBeUndefined();
        await change(t, 'Share', 'chosen');
        await change(t, field === 'colleagues' ? 'Share with Ada' : 'Share Claude Code', []);
        expect(t.button(/^Stop sharing$/)).toBeTruthy();
        await t.click(/^Stop sharing$/);
        await vi.waitFor(() =>
          expect(bodies(f)[0]?.body).toEqual({ visibility: 'private', shared_with: [] }),
        );
      } finally {
        t.unmount();
      }
    },
  );
  it('offers two audiences and only switches to Stop sharing for saved grants', async () => {
    const f = fetching();
    const t = await mount(MachinesView, () => ({ machineId: 'd1' }));
    try {
      expect(
        t
          .all()
          .filter((n) => n.tag === 'option')
          .map(textOf),
      ).toEqual(['Choose colleagues', 'Everyone in the workspace']);
      expect(t.button(/^Save sharing$/)).toBeTruthy();
      expect(t.button(/^Stop sharing$/)).toBeUndefined();
      await change(t, 'Share Claude Code', ['r1']);
      await change(t, 'Share with Ada', ['u2']);
      expect(t.button(/^Save sharing$/)).toBeTruthy();
      expect(t.button(/^Stop sharing$/)).toBeUndefined();
      t.queryClient.setQueryData(keys.runtimes('w'), [runtime({ shared_with: ['u2'] })]);
      await nextTick();
      expect(t.button(/^Stop sharing$/)).toBeTruthy();
      await t.click(/^Stop sharing$/);
      await vi.waitFor(() =>
        expect(bodies(f)[0]?.body).toEqual({ visibility: 'private', shared_with: [] }),
      );
    } finally {
      t.unmount();
    }
  });
  it('keeps the dialog open until a multi-CLI save completes', async () => {
    let finish!: (response: Response) => void;
    const f = vi.fn((path: string, init?: RequestInit) =>
      path === '/api/runtimes/r1/sharing' && init?.method === 'PUT'
        ? new Promise<Response>((resolve) => {
            finish = resolve;
          })
        : Promise.resolve(
            new Response(
              path === '/api/runtimes'
                ? JSON.stringify([
                    runtime({ visibility: 'workspace' }),
                    runtime({ id: 'r2', harness: 'codex', visibility: 'workspace' }),
                  ])
                : init?.method === 'GET'
                  ? '[]'
                  : '{}',
            ),
          ),
    );
    vi.stubGlobal('fetch', f);
    const t = await mount(
      MachinesView,
      () => ({ machineId: 'd1' }),
      '/',
      (c) =>
        c.setQueryData(keys.runtimes('w'), [runtime(), runtime({ id: 'r2', harness: 'codex' })]),
    );
    try {
      await change(t, 'Share', 'workspace');
      await change(t, 'Share Claude Code', ['r1', 'r2']);
      await t.click(/^Save sharing$/);
      await vi.waitFor(() => expect(bodies(f)).toHaveLength(1));
      await t.click(/^Close$/);
      expect(t.button(/^Saving…$/)).toBeTruthy();
      finish(new Response('{}'));
      await vi.waitFor(() => expect(bodies(f)).toHaveLength(2));
      await vi.waitFor(() => expect(t.button(/^Stop sharing$/)?.props.disabled).toBe(false));
      await t.click(/^Close$/);
      expect(t.button(/^Stop sharing$/)).toBeUndefined();
    } finally {
      t.unmount();
    }
  });
  it('opens sharing from Colleagues Details without inline sharing or usage requests', async () => {
    const f = fetching();
    const t = await mount(MachinesView, () => ({ machineId: 'd1' }), '/', undefined, false);
    try {
      expect(
        t
          .all()
          .filter((n) => n.tag === 'th')
          .map(textOf),
      ).toEqual(['Runtime', 'Health', 'Active runs', 'CLI', 'Colleagues']);
      expect(t.text()).not.toContain('Choose who can run agents');
      expect(t.text()).not.toContain('Colleagues’ runs');
      expect(f.mock.calls.some(([path]) => String(path).includes('/colleagues'))).toBe(false);
      await t.click(/^Details$/);
      expect(t.text()).not.toContain('Choose who can run agents on this machine.');
      expect(
        t
          .all()
          .filter((n) => n.tag === 'h2')
          .map(textOf),
      ).not.toContain('Shared machines');
      expect(t.all().find((n) => n.tag === 'dialog')?.props['aria-label']).toBe('Details');
      expect(t.text()).not.toContain('Adapter capabilities');
      expect(t.button(/^Save sharing$/)).toBeTruthy();
      await t.click(/^Close$/);
      expect(t.button(/^Save sharing$/)).toBeUndefined();
      await t.click(/^Details$/);
      expect(t.button(/^Save sharing$/)).toBeTruthy();
      expect(bodies(f)).toHaveLength(0);
    } finally {
      t.unmount();
    }
  });
  it.each([
    [
      runtime({ visibility: 'workspace' }),
      runtime({ id: 'r2', harness: 'codex', shared_with: ['u2'] }),
    ],
    [
      runtime({ shared_with: ['u2'] }),
      runtime({ id: 'r2', harness: 'codex', shared_with: ['u3'] }),
    ],
  ])('preserves mixed grants on open and only clears the unchecked CLI', async (first, second) => {
    const f = fetching();
    const t = await mount(
      MachinesView,
      () => ({ machineId: 'd1' }),
      '/',
      (c) => c.setQueryData(keys.runtimes('w'), [first, second]),
    );
    try {
      expect(t.text()).toContain('Different permissions per CLI');
      expect(t.button(/^Stop sharing$/)).toBeTruthy();
      expect(bodies(f)).toHaveLength(0);
      await change(t, 'Share Claude Code', ['r2']);
      await t.click(/^Save sharing$/);
      await vi.waitFor(() =>
        expect(bodies(f)).toEqual([
          {
            path: '/api/runtimes/r1/sharing',
            body: { visibility: 'private', shared_with: [] },
            workspace: 'w',
          },
        ]),
      );
    } finally {
      t.unmount();
    }
  });
  it('requires deliberate harmonization again after a partial save and refresh', async () => {
    let rows = [
      runtime({ shared_with: ['u2'] }),
      runtime({ id: 'r2', harness: 'codex', shared_with: ['u3'] }),
    ];
    let fail = true;
    const f = vi.fn(async (path, init) => {
      if (init?.method === 'GET')
        return new Response(JSON.stringify(path === '/api/runtimes' ? rows : []));
      if (path === '/api/runtimes/r2/sharing' && fail)
        return new Response(JSON.stringify({ error: 'Try again' }), { status: 500 });
      if (init?.method === 'PUT')
        rows = rows.map((r) =>
          path === `/api/runtimes/${r.id}/sharing` ? { ...r, ...JSON.parse(init.body) } : r,
        );
      return new Response('{}');
    });
    vi.stubGlobal('fetch', f);
    const t = await mount(
      MachinesView,
      () => ({ machineId: 'd1' }),
      '/',
      (c) => c.setQueryData(keys.runtimes('w'), rows),
    );
    try {
      await change(t, 'Share', 'workspace');
      await t.click(/^Save sharing$/);
      await vi.waitFor(() => expect(t.text()).toContain('Try again'));
      t.queryClient.setQueryData(keys.runtimes('w'), [...rows]);
      await nextTick();
      await nextTick();
      expect(t.text()).toContain('Different permissions per CLI');
      const before = bodies(f).length;
      expect(t.button(/^Stop sharing$/)).toBeTruthy();
      expect(bodies(f)).toHaveLength(before);
      fail = false;
      await change(t, 'Share', 'workspace');
      await t.click(/^Save sharing$/);
      await vi.waitFor(() => expect(bodies(f).length).toBe(before + 1));
      expect(bodies(f).at(-1)?.path).toBe('/api/runtimes/r2/sharing');
    } finally {
      t.unmount();
    }
  });
  it('shows the study machine label and notice but no editor to nonowners', async () => {
    const f = fetching();
    const t = await mount(
      TaskDetail,
      () => ({ id: 'c' }),
      '/',
      (c) => c.setQueryData(keys.me, { id: 'u2' }),
    );
    try {
      expect(t.text()).not.toContain('Runs on: Bench');
      await t.click(/^Properties$/);
      expect(t.text()).toContain('Runs on: Bench');
      expect(t.text()).toContain('no longer shared');
      expect(t.all().some((n) => n.props['aria-label'] === 'Machine')).toBe(false);
      expect(bodies(f)).toHaveLength(0);
    } finally {
      t.unmount();
    }
  });
  it.each([
    ['u2', 'Ada'],
    ['former', 'Former member'],
    [null, 'Owner'],
  ])('shows recorded or current owner for a live runtime (%s)', async (owner, label) => {
    fetching();
    const t = await mount(AgentRunDialog, () => ({
      runs: [
        {
          id: 'run',
          assignment: '',
          board_column: 'grounding',
          runtime_id: 'r1',
          machine_name: 'Old bench',
          machine_owner_id: owner,
          role: 'leader',
          harness: 'claude',
          attempt: 1,
          max_attempts: 1,
          status: 'completed',
        } as AgentRun,
      ],
      runtime: () => runtime() as unknown as Runtime,
      studyId: 'c',
      modelValue: 'run',
    }));
    try {
      await t.click(/^Environment$/);
      expect(t.text()).toContain(`Bench (${label})`);
    } finally {
      t.unmount();
    }
  });
  it('hides owner sharing and usage from colleagues', async () => {
    fetching();
    const t = await mount(
      MachinesView,
      () => ({ machineId: 'd1' }),
      '/',
      (c) => c.setQueryData(keys.me, { id: 'u2' }),
    );
    try {
      expect(t.text()).not.toContain('Share');
      expect(t.text()).not.toContain('Colleagues’ runs');
    } finally {
      t.unmount();
    }
  });
  it('shares checked CLIs with everyone without a colleague list and renders both exact warnings', async () => {
    const f = fetching();
    const t = await mount(MachinesView, () => ({ machineId: 'd1' }));
    try {
      await change(t, 'Share', 'workspace');
      await change(t, 'Share Claude Code', ['r1']);
      expect(t.all().find((n) => n.props['aria-label'] === 'Share with Ada')?.props.disabled).toBe(
        true,
      );
      expect(t.text()).toContain(signIn);
      expect(t.text()).toContain(files);
      await t.click(/^Save sharing$/);
      await vi.waitFor(() =>
        expect(bodies(f)).toContainEqual({
          path: '/api/runtimes/r1/sharing',
          body: { visibility: 'workspace', shared_with: [] },
          workspace: 'w',
        }),
      );
    } finally {
      t.unmount();
    }
  });
  it.each(['darwin', ''])(
    'blocks enabling on %s but lets an existing share be cleared',
    async (os) => {
      const f = fetching();
      const t = await mount(
        MachinesView,
        () => ({ machineId: 'd1' }),
        '/',
        (c) => c.setQueryData(keys.runtimes('w'), [runtime({ os, visibility: 'workspace' })]),
      );
      try {
        expect(t.text()).toContain(platform);
        expect(
          t.all().find((n) => n.tag === 'option' && n.props.value === 'workspace')?.props.disabled,
        ).toBe(true);
        await t.click(/^Stop sharing$/);
        await vi.waitFor(() =>
          expect(bodies(f)[0]?.body).toEqual({ visibility: 'private', shared_with: [] }),
        );
      } finally {
        t.unmount();
      }
    },
  );
  it.each(['darwin', ''])(
    'prevents adding colleagues to an existing private share on %s and confirms clearing',
    async (os) => {
      let puts = 0;
      const f = vi.fn(async (_path, init) => {
        if (init?.method === 'PUT' && ++puts === 1)
          return new Response(
            JSON.stringify({
              affected: [
                {
                  run_id: 'existing',
                  card_id: 'c',
                  study_title: 'Existing study',
                  colleague: 'Ada',
                },
              ],
            }),
            { status: 409 },
          );
        return new Response(init?.method === 'GET' ? '[]' : '{}');
      });
      vi.stubGlobal('fetch', f);
      const t = await mount(
        MachinesView,
        () => ({ machineId: 'd1' }),
        '/',
        (c) => {
          c.setQueryData(keys.runtimes('w'), [
            runtime({ os, visibility: 'private', shared_with: ['u2'] }),
          ]);
          c.setQueryData(keys.members('w'), [
            { user_id: 'u1', name: 'Owner' },
            { user_id: 'u2', name: 'Ada' },
            { user_id: 'u3', name: 'Bea' },
          ]);
        },
      );
      try {
        expect(t.text()).toContain(platform);
        expect(t.text()).toContain(signIn);
        expect(t.text()).toContain(files);
        expect(t.input('Share with Bea')?.props.disabled).toBe(true);
        expect(t.button(/^Stop sharing$/)?.props.disabled).toBe(false);
        expect(bodies(f)).toHaveLength(0);
        await t.click(/^Stop sharing$/);
        await vi.waitFor(() => expect(t.text()).toContain('Existing study'));
        expect(t.text()).toContain(revoke);
        expect(bodies(f)[0]?.body).toEqual({ visibility: 'private', shared_with: [] });
        await t.click(/^Confirm stop sharing$/);
        await vi.waitFor(() =>
          expect(bodies(f)[1]?.body).toEqual({
            visibility: 'private',
            shared_with: [],
            confirm: ['existing'],
          }),
        );
      } finally {
        t.unmount();
      }
    },
  );
  it('replaces a refreshed revoke list and confirms exactly the latest run ids', async () => {
    let put = 0;
    const f = vi.fn(async (_p, init) =>
      init?.method === 'PUT' && ++put < 3
        ? new Response(
            JSON.stringify({
              affected: [
                {
                  run_id: put === 1 ? 'old' : 'fresh',
                  card_id: 'c',
                  study_title: put === 1 ? 'Old study' : 'New study',
                  colleague: 'Ada',
                },
              ],
            }),
            { status: 409 },
          )
        : new Response('{}'),
    );
    vi.stubGlobal('fetch', f);
    const t = await mount(
      MachinesView,
      () => ({ machineId: 'd1' }),
      '/',
      (c) => c.setQueryData(keys.runtimes('w'), [runtime({ visibility: 'workspace' })]),
    );
    try {
      await t.click(/^Stop sharing$/);
      await vi.waitFor(() => expect(t.text()).toContain('Old study'));
      expect(t.text()).toContain(revoke);
      expect(t.text()).toContain('Ada');
      await t.click(/^Confirm stop sharing$/);
      await vi.waitFor(() => expect(t.text()).toContain('New study'));
      expect(t.text()).not.toContain('Old study');
      await t.click(/^Confirm stop sharing$/);
      await vi.waitFor(() => expect(bodies(f).at(-1)?.body.confirm).toEqual(['fresh']));
      expect(bodies(f)[1]?.body.confirm).toEqual(['old']);
    } finally {
      t.unmount();
    }
  });
  it('offers owner machines on any OS and eligible offline Linux VMs by their own id', async () => {
    fetching();
    const t = await mount(
      CreateTask,
      () => ({}),
      '/',
      (c) =>
        c.setQueryData(keys.runtimes('w'), [
          runtime({ os: 'darwin' }),
          runtime({
            id: 'mac',
            daemon_id: 'host',
            owner_id: 'u2',
            device_name: 'Ada Mac',
            os: 'darwin',
            visibility: 'workspace',
          }),
          runtime({
            id: 'vm',
            daemon_id: 'vm-id',
            owner_id: 'u2',
            device_name: 'Linux VM',
            visibility: 'workspace',
          }),
          runtime({
            id: 'unknown',
            daemon_id: 'unknown',
            owner_id: 'u2',
            os: '',
            visibility: 'workspace',
          }),
          runtime({
            id: 'chosen',
            daemon_id: 'chosen',
            owner_id: 'u2',
            device_name: 'Chosen Linux',
            shared_with: ['u1'],
          }),
          runtime({ id: 'no', daemon_id: 'no', owner_id: 'u2', device_name: 'Private Linux' }),
        ]),
    );
    try {
      expect(t.text()).toContain('My machines');
      expect(t.text()).toContain('Bench');
      expect(t.text()).toContain('Linux VM (Ada)');
      expect(t.text()).toContain('Chosen Linux (Ada)');
      expect(t.text()).not.toContain('Ada Mac');
      expect(t.text()).not.toContain('Private Linux');
      expect(t.all().some((n) => n.tag === 'option' && n.props.value === 'vm-id')).toBe(true);
    } finally {
      t.unmount();
    }
  });
  it.each(['', 'd1'])('posts a machine only when chosen (%s)', async (machine) => {
    const f = fetching();
    const t = await mount(CreateTask, () => ({}));
    try {
      await t.fill('Describe your study', 'Question');
      await t.click(/^Pre-registered · 0$/);
      await t.fill('Result name 1', 'Accurate');
      await t.fill('Result condition 1', 'accuracy >= 0.9');
      if (machine) await change(t, 'Machine', machine);
      await t.submit();
      const body = bodies(f).find((c) => c.path === '/api/cards')!.body;
      if (machine) expect(body.machine).toBe(machine);
      else expect(body).not.toHaveProperty('machine');
    } finally {
      t.unmount();
    }
  });
  it('shows the study machine notice and PATCHes a selection including My machines null', async () => {
    const f = fetching();
    const t = await mount(TaskDetail, () => ({ id: 'c' }));
    try {
      expect(t.text()).not.toContain('Runs on: Bench');
      await t.click(/^Properties$/);
      expect(t.text()).toContain('Runs on: Bench');
      expect(t.text()).toContain(
        "This study's machine is no longer shared with you; choose another.",
      );
      await change(t, 'Machine', '');
      await vi.waitFor(() =>
        expect(bodies(f)).toContainEqual({
          path: '/api/cards/c/machine',
          body: { machine: null },
          workspace: 'w',
        }),
      );
    } finally {
      t.unmount();
    }
  });
  it('shows a deleted runtime snapshot and owner in the run environment', async () => {
    fetching();
    const t = await mount(AgentRunDialog, () => ({
      runs: [
        {
          id: 'run',
          assignment: '',
          board_column: 'grounding',
          runtime_id: null,
          machine_name: 'Old bench',
          machine_owner_id: 'u2',
          role: 'leader',
          harness: 'claude',
          attempt: 0,
          max_attempts: 1,
          status: 'completed',
        } as AgentRun,
      ],
      runtime: () => undefined,
      studyId: 'c',
      modelValue: 'run',
    }));
    try {
      await t.click(/^Environment$/);
      expect(t.text()).toContain('Old bench (Ada)');
    } finally {
      t.unmount();
    }
  });
  it('saves chosen colleagues per checked CLI and clears an unchecked CLI', async () => {
    const f = fetching();
    const t = await mount(
      MachinesView,
      () => ({ machineId: 'd1' }),
      '/',
      (c) =>
        c.setQueryData(keys.runtimes('w'), [
          runtime(),
          runtime({ id: 'r2', harness: 'codex', visibility: 'workspace' }),
        ]),
    );
    try {
      await change(t, 'Share', 'chosen');
      await change(t, 'Share Claude Code', ['r1']);
      await change(t, 'Share with Ada', ['u2']);
      await t.click(/^Save sharing$/);
      await vi.waitFor(() => expect(bodies(f)).toHaveLength(2));
      expect(bodies(f).map((c) => c.body)).toEqual([
        { visibility: 'private', shared_with: ['u2'] },
        { visibility: 'private', shared_with: [] },
      ]);
    } finally {
      t.unmount();
    }
  });
  it('does not send a queued CLI update or revive a dialog after switching machines', async () => {
    let finish!: (r: Response) => void;
    const f = vi.fn((_p, init) =>
      init?.method === 'PUT'
        ? new Promise<Response>((resolve) => {
            finish = resolve;
          })
        : Promise.resolve(new Response('[]')),
    );
    vi.stubGlobal('fetch', f);
    const id = ref('d1');
    const t = await mount(
      MachinesView,
      () => ({ machineId: id.value }),
      '/',
      (c) =>
        c.setQueryData(keys.runtimes('w'), [
          runtime(),
          runtime({ id: 'r2', harness: 'codex' }),
          runtime({ id: 'other', daemon_id: 'other', device_name: 'Other' }),
        ]),
    );
    try {
      await change(t, 'Share', 'workspace');
      await change(t, 'Share Claude Code', ['r1', 'r2']);
      await t.click(/^Save sharing$/);
      await vi.waitFor(() => expect(bodies(f)).toHaveLength(1));
      id.value = 'other';
      await nextTick();
      finish(
        new Response(
          JSON.stringify({
            affected: [{ run_id: 'stale', study_title: 'Stale study', colleague: 'Ada' }],
          }),
          { status: 409 },
        ),
      );
      await nextTick();
      await nextTick();
      expect(t.text()).not.toContain('Stale study');
      expect(bodies(f)).toHaveLength(1);
    } finally {
      t.unmount();
    }
  });
  it('fences a queued save across workspace switches even when returning to the same machine', async () => {
    let finish!: (r: Response) => void;
    const f = vi.fn((_p, init) =>
      init?.method === 'PUT'
        ? new Promise<Response>((resolve) => {
            finish = resolve;
          })
        : Promise.resolve(new Response('[]')),
    );
    vi.stubGlobal('fetch', f);
    const t = await mount(
      MachinesView,
      () => ({ machineId: 'd1' }),
      '/',
      (c) =>
        c.setQueryData(keys.runtimes('w'), [runtime(), runtime({ id: 'r2', harness: 'codex' })]),
    );
    try {
      await change(t, 'Share', 'workspace');
      await change(t, 'Share Claude Code', ['r1', 'r2']);
      await t.click(/^Save sharing$/);
      await vi.waitFor(() => expect(bodies(f)).toHaveLength(1));
      workspaceId.value = 'other';
      await nextTick();
      workspaceId.value = 'w';
      await nextTick();
      finish(new Response('{}'));
      await nextTick();
      await nextTick();
      expect(bodies(f)).toHaveLength(1);
    } finally {
      t.unmount();
    }
  });
  it('renders the server count for all active runtime runs', async () => {
    const f = fetching();
    const t = await mount(
      MachinesView,
      () => ({ machineId: 'd1' }),
      '/',
      (c) => c.setQueryData(keys.runtimes('w'), [runtime({ active_runs: 3 })]),
      false,
    );
    try {
      const row = t
        .all()
        .find((n) => n.tag === 'tbody')!
        .children.find((n) => n.tag === 'tr')!;
      expect(textOf(row.children[2]!)).toBe('3');
      expect(t.text()).not.toContain('Colleagues’ runs');
      expect(f.mock.calls.some(([path]) => String(path).includes('/colleagues'))).toBe(false);
    } finally {
      t.unmount();
    }
  });
  it('keeps a sharing draft when machine health refreshes', async () => {
    const f = fetching();
    const t = await mount(MachinesView, () => ({ machineId: 'd1' }));
    try {
      await change(t, 'Share', 'chosen');
      await change(t, 'Share Claude Code', ['r1']);
      await change(t, 'Share with Ada', ['u2']);
      t.queryClient.setQueryData(keys.runtimes('w'), [
        runtime({ status: 'online', last_seen_at: '2026-10-26T10:00:00Z' }),
      ]);
      await nextTick();
      await t.click(/^Save sharing$/);
      await vi.waitFor(() =>
        expect(bodies(f)[0]?.body).toEqual({ visibility: 'private', shared_with: ['u2'] }),
      );
    } finally {
      t.unmount();
    }
  });
});
