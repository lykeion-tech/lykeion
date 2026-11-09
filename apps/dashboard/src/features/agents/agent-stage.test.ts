import { afterEach, describe, expect, it, vi } from 'vitest';
import { createRenderer, h, nextTick, ref, ssrContextKey, type Component } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import AgentsView from './AgentsView.vue';
import AgentDetail from './AgentDetail.vue';
import AssignPicker from '../../ui/AssignPicker.vue';
import { workspaceId } from '../../api/session';
import { keys } from '../../api/keys';
import type { AgentDefinition } from '../../api/types';

const request = vi.hoisted(() => vi.fn());
vi.mock('../coresearch/Aristotle.vue', () => ({ default: { render: () => null } }));
vi.mock('../../api/client', () => ({
  api: request,
  ApiError: class extends Error {},
}));

// A flat stand-in for the select: its options are always rendered as buttons.
vi.mock('../../ui/CompactSelect.vue', async () => {
  const { h } = await import('vue');
  return {
    default: {
      props: ['modelValue', 'options', 'label', 'width'],
      emits: ['update:modelValue'],
      setup: (
        props: { label: string; options: { value: string; label: string }[] },
        { emit }: { emit: (e: 'update:modelValue', v: string) => void },
      ) =>
        () =>
          h(
            'div',
            { 'aria-label': props.label },
            props.options.map((o) =>
              h(
                'button',
                { role: 'option', 'aria-label': props.label, onClick: () => emit('update:modelValue', o.value) },
                o.label,
              ),
            ),
          ),
    },
  };
});

type TNode = {
  tag: string;
  text: string;
  props: Record<string, unknown>;
  children: TNode[];
  parent: TNode | null;
};
const body = { tag: 'body', text: '', props: {}, children: [], parent: null, focus() {} } as TNode;
function node(tag: string, text = ''): TNode & Record<string, unknown> {
  return {
    tag,
    text,
    props: {},
    children: [],
    parent: null,
    get options() {
      return this.children.filter((n: TNode) => n.tag === 'option');
    },
    addEventListener() {},
    removeEventListener() {},
    querySelector: () => body,
    contains: () => true,
    focus() {},
    showModal() {},
    getRootNode: () => null,
    getBoundingClientRect: () => ({ left: 0, top: 0, right: 0 }),
    closest: () => null,
  };
}
const textOf = (n: TNode): string => n.text + n.children.map(textOf).join('');
const all = (n: TNode): TNode[] => [n, ...n.children.flatMap(all)];
const { createApp: createTreeApp } = createRenderer<TNode, TNode>({
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
  querySelector: () => body,
});

const agent = (extra: Partial<AgentDefinition>): AgentDefinition =>
  ({
    id: 'a',
    name: 'Agent',
    description: '',
    instructions: '',
    harness: 'claude',
    model: '',
    visibility: 'workspace',
    owner_id: 'u1',
    source: 'custom',
    built_in_key: '',
    avatar_url: '',
    archived_at: null,
    updated_at: '2026-11-01T10:00:00Z',
    stage: 'hypotheses',
    laboratory: '',
    runtime_id: null,
    ...extra,
  }) as unknown as AgentDefinition;
const runtime = (id: string, name: string, extra: Record<string, unknown> = {}) => ({
  id,
  owner_id: 'u1',
  harness: 'claude',
  device_name: name,
  custom_name: null,
  status: 'online',
  visibility: 'private',
  shared_with: [],
  models: [],
  os: 'linux',
  ...extra,
});
let runtimeRows = [runtime('r1', 'Bench'), runtime('r2', 'Bay')];
let memberRows = [{ user_id: 'u1', name: 'Owner', email: 'o@lab.test' }];

const apps: { unmount(): void }[] = [];
afterEach(() => {
  apps.splice(0).forEach((a) => a.unmount());
  workspaceId.value = '';
  body.children = [];
  vi.unstubAllGlobals();
  request.mockReset();
  runtimeRows = [runtime('r1', 'Bench'), runtime('r2', 'Bay')];
  memberRows = [{ user_id: 'u1', name: 'Owner', email: 'o@lab.test' }];
});

async function mount(
  component: Component,
  props: Record<string, unknown>,
  agents: AgentDefinition[],
  role = 'owner',
  path = '/',
) {
  workspaceId.value = 'w';
  vi.stubGlobal('window', {
    innerWidth: 1512,
    innerHeight: 900,
    addEventListener() {},
    removeEventListener() {},
  });
  vi.stubGlobal('Document', class {});
  vi.stubGlobal('ShadowRoot', class {});
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  const g = globalThis as { document?: unknown };
  g.document ??= {
    addEventListener() {},
    removeEventListener() {},
    getElementById: () => null,
  };
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  });
  client.setQueryData(keys.me, { id: 'u1', email: 'o@lab.test' });
  client.setQueryData(keys.workspaces, [{ id: 'w', name: 'Lab', role }]);
  client.setQueryData(keys.agents('w'), agents);
  for (const a of agents) client.setQueryData(keys.agent('w', a.id), a);
  client.setQueryData(keys.runtimes('w'), runtimeRows);
  client.setQueryData(keys.members('w'), memberRows);
  client.setQueryData(keys.workspace('w'), { name: 'Lab', display_prefix: 'L' });
  client.setQueryData(keys.cards('w'), []);
  client.setQueryData(keys.teams('w', '', ''), [
    { id: 't1', name: 'Grounders', source: 'shipped', added: true, manifest: {} },
  ]);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:p(.*)*', component: AgentsView }],
  });
  await router.push(path);
  const model = ref<unknown>(null);
  const range = ref({ from: 'hypotheses', to: 'hypotheses' });
  const app = createTreeApp({
    render: () =>
      h(component, {
        ...props,
        ...(component === AssignPicker
          ? {
              modelValue: model.value,
              'onUpdate:modelValue': (v: unknown) => (model.value = v),
              range: range.value,
              'onUpdate:range': (v: { from: string; to: string }) => (range.value = v),
            }
          : {}),
      }),
  });
  app.use(VueQueryPlugin, { queryClient: client });
  app.use(router);
  app.provide(ssrContextKey, {});
  const root: TNode = node('root');
  app.mount(root);
  apps.push(app);
  await new Promise((r) => setTimeout(r, 0));
  await nextTick();
  const find = (tag: string, test: (n: TNode) => boolean) =>
    [...all(root), ...all(body)].find((n) => n.tag === tag && test(n));
  const button = (label: RegExp) =>
    find(
      'button',
      (n) => label.test(textOf(n).trim()) || label.test(String(n.props['aria-label'] ?? '')),
    );
  const click = async (label: RegExp) => {
    const b = button(label);
    expect(b, textOf(root)).toBeTruthy();
    (b!.props.onClick as (e?: unknown) => void)({ preventDefault() {}, stopPropagation() {} });
    await nextTick();
    await nextTick();
  };
  return {
    root,
    text: () => textOf(root) + textOf(body),
    find,
    button,
    click,
    model,
    range,
    options: () =>
      [...all(root), ...all(body)]
        .filter((n) => n.props.role === 'option')
        .map((n) => textOf(n).trim()),
  };
}

describe('agent stage', () => {
  it('requires a stage in the create form and sends it', async () => {
    request.mockResolvedValue(agent({ id: 'new' }));
    const t = await mount(AgentsView, { create: true }, []);
    const select = t.find('select', (n) => n.props['aria-label'] === 'Agent stage')!;
    expect(select).toBeTruthy();
    expect(select.children.filter((n) => n.tag === 'option').map(textOf)).toEqual([
      'Grounding',
      'Hypotheses',
      'Experimentation',
      'Validation',
    ]);
    const name = t.find('input', (n) => n.props['aria-label'] === 'Agent name')!;
    (name.props['onUpdate:modelValue'] as (v: string) => void)('Reader');
    (select.props['onUpdate:modelValue'] as (v: string) => void)('validation');
    await nextTick();
    const form = t.find('form', () => true)!;
    await (form.props.onSubmit as (e: unknown) => Promise<void>)({ preventDefault() {} });
    const post = request.mock.calls.find((c) => c[0] === 'POST');
    expect(post?.[2]).toMatchObject({ name: 'Reader', stage: 'validation' });
  });

  it('flags an agent with no stage and offers Choose a stage', async () => {
    const t = await mount(AgentsView, {}, [
      agent({ id: 'a', name: 'Old', stage: '' }),
      agent({ id: 'b', name: 'Staged', stage: 'grounding' }),
    ]);
    expect(t.text()).toContain('No stage');
    expect(t.text()).toContain('Choose a stage');
    expect(t.text()).toContain('Grounding');
  });

  it("shows Aristotle as No stage with no way to pick one", async () => {
    const ari = agent({ id: 'x', name: 'Aristotle', stage: '', built_in_key: 'aristotle' });
    const list = await mount(AgentsView, {}, [ari]);
    expect(list.text()).toContain('No stage');
    expect(list.text()).not.toContain('Choose a stage');
    const detail = await mount(AgentDetail, { id: 'x' }, [ari], 'owner', '/x?tab=settings');
    expect(detail.find('select', (n) => n.props['aria-label'] === 'Agent stage')).toBeUndefined();
    expect(detail.text()).toContain('No stage');
  });

  it('shows no edit controls for a laboratory agent', async () => {
    const lab = agent({ id: 'l', name: 'Lab agent', laboratory: 'built-in', owner_id: 'u1' });
    const list = await mount(AgentsView, {}, [lab]);
    expect(list.text()).toContain('Laboratory');
    expect(list.text()).not.toContain('Choose a stage');
    const detail = await mount(AgentDetail, { id: 'l' }, [lab], 'owner', '/x?tab=settings');
    expect(detail.button(/Delete agent/)).toBeUndefined();
    expect(detail.button(/Archive/)).toBeUndefined();
    const stage = detail.find('select', (n) => n.props['aria-label'] === 'Agent stage');
    expect(stage?.props.disabled).toBe(true);
  });

  it('lets only the workspace owner choose a laboratory agent machine', async () => {
    const lab = agent({ id: 'l', laboratory: 'built-in', runtime_id: null });
    request.mockImplementation(async (m: string) =>
      m === 'PUT' ? { ...lab, runtime_id: 'r2' } : [lab],
    );
    const owner = await mount(AgentDetail, { id: 'l' }, [lab], 'owner');
    const select = owner.find('select', (n) => n.props['aria-label'] === 'Machine')!;
    expect(select).toBeTruthy();
    expect(textOf(owner.root)).toContain('Not bound to a machine');
    (select.props.onChange as (e: unknown) => void)({ target: { value: 'r2' } });
    await nextTick();
    expect(request).toHaveBeenCalledWith(
      'PUT',
      '/api/agents/l/runtime',
      { runtime_id: 'r2' },
      undefined,
      { workspace: 'w' },
    );
    const member = await mount(
      AgentDetail,
      { id: 'l' },
      [{ ...lab, runtime_id: 'r1' }],
      'member',
    );
    expect(member.find('select', (n) => n.props['aria-label'] === 'Machine')).toBeUndefined();
    expect(member.text()).toContain('Bench');
  });

  it('offers the owner machines of any CLI that allow the agent owner', async () => {
    runtimeRows = [
      runtime('r1', 'Bench'),
      runtime('r2', 'Codex box', { harness: 'codex' }),
      runtime('r3', 'Colleague private', { owner_id: 'u2' }),
      runtime('r4', 'Colleague shared', { owner_id: 'u2', visibility: 'workspace' }),
      runtime('r5', 'Colleague mac', { owner_id: 'u2', visibility: 'workspace', os: 'darwin' }),
    ];
    const lab = agent({ id: 'l', laboratory: 'built-in', harness: 'claude', runtime_id: 'r2' });
    const t = await mount(AgentDetail, { id: 'l' }, [lab], 'owner');
    const select = t.find('select', (n) => n.props['aria-label'] === 'Machine')!;
    expect(select.props.value).toBe('r2');
    expect(select.children.filter((n) => n.tag === 'option').map((n) => textOf(n).trim())).toEqual([
      'Not bound to a machine',
      'Bench',
      'Codex box',
      'Colleague shared',
    ]);
    expect(t.text()).toContain('Codex');
    expect(t.text()).not.toContain('Claude Code');
  });

  it('names the bound machine and its CLI for a member', async () => {
    runtimeRows = [runtime('r9', 'Lab mac', { owner_id: 'u2', harness: 'codex', os: 'darwin' })];
    const lab = agent({ id: 'l', laboratory: 'built-in', owner_id: 'u2', harness: 'claude', runtime_id: 'r9' });
    const t = await mount(AgentDetail, { id: 'l' }, [lab], 'member');
    expect(t.text()).toContain('Lab mac');
    expect(t.text()).toContain('Codex');
  });

  it("explains that a machine other than Linux runs only its owner's studies", async () => {
    runtimeRows = [runtime('r1', 'Bench'), runtime('r2', 'Desk mac', { os: 'darwin' })];
    const alone = await mount(AgentDetail, { id: 'l' }, [agent({ id: 'l', laboratory: 'built-in', runtime_id: 'r2' })]);
    expect(alone.text()).not.toContain('runs only its owner');
    memberRows = [...memberRows, { user_id: 'u2', name: 'Colleague', email: 'c@lab.test' }];
    const note =
      "Desk mac runs only its owner's studies. Bind this agent to a shared Linux machine to run colleagues' studies.";
    const onMac = await mount(AgentDetail, { id: 'l' }, [agent({ id: 'l', laboratory: 'built-in', runtime_id: 'r2' })]);
    expect(onMac.text().replace(/\s+/g, ' ')).toContain(note);
    const onLinux = await mount(AgentDetail, { id: 'm' }, [agent({ id: 'm', laboratory: 'built-in', runtime_id: 'r1' })]);
    expect(onLinux.text()).not.toContain('runs only its owner');
  });

  it('shows the server refusal for a machine verbatim', async () => {
    const lab = agent({ id: 'l', laboratory: 'built-in' });
    request.mockRejectedValue(new Error("this machine isn't shared with the workspace owner"));
    const t = await mount(AgentDetail, { id: 'l' }, [lab]);
    const select = t.find('select', (n) => n.props['aria-label'] === 'Machine')!;
    (select.props.onChange as (e: unknown) => void)({ target: { value: 'r1' } });
    await new Promise((r) => setTimeout(r, 0));
    await nextTick();
    expect(t.text()).toContain("this machine isn't shared with the workspace owner");
  });

  const roster = [
    agent({ id: 'h', name: 'Hypo Agent', stage: 'hypotheses' }),
    agent({ id: 'g', name: 'Ground Agent', stage: 'grounding' }),
    agent({ id: 'n', name: 'Nostage Agent', stage: '' }),
  ];
  it('offers a one-agent study only the agents of the chosen stage and fixes the scope', async () => {
    const t = await mount(AssignPicker, { oneAgent: true }, roster);
    await t.click(/^Assignee/);
    const labels = t.text();
    expect(labels).toContain('Hypo Agent');
    expect(labels).not.toContain('Ground Agent');
    expect(labels).not.toContain('Nostage Agent');
    await t.click(/Hypo Agent$/);
    expect(t.model.value).toEqual({ type: 'agent', id: 'h' });
    expect(t.range.value).toEqual({ from: 'hypotheses', to: 'hypotheses' });
    expect(t.button(/^Stages:/)?.props.disabled).toBe(true);
  });

  it('starts an Autoresearch agent on its own stage and follows a different agent', async () => {
    const t = await mount(AssignPicker, { agents: true }, roster);
    await t.click(/Unassigned/);
    await t.click(/Hypo Agent$/);
    expect(t.range.value).toEqual({ from: 'hypotheses', to: 'hypotheses' });
    await t.click(/Hypo Agent/);
    await t.click(/Ground Agent$/);
    expect(t.range.value).toEqual({ from: 'grounding', to: 'grounding' });
  });

  it('offers an Autoresearch filler only when one stage is uncovered and it is the agent stage', async () => {
    const t = await mount(AssignPicker, { agents: true }, roster);
    await t.click(/Unassigned/);
    await t.click(/New set/);
    const filler = () =>
      t
        .options()
        .filter((o) => !['No Team', 'Grounders'].includes(o))
        .join(',');
    expect(filler()).toBe('Choose an agent');
    const pick = async (stage: string) => {
      const grounders = [...all(t.root), ...all(body)].filter(
        (n) => n.props.role === 'option' && n.props['aria-label'] === `${stage} Team` && textOf(n) === 'Grounders',
      );
      (grounders[0]!.props.onClick as () => void)();
      await nextTick();
    };
    await pick('Grounding');
    await pick('Experimentation');
    expect(filler()).toBe('Choose an agent');
    await pick('Validation');
    expect(filler()).toBe('Choose an agent,Hypo Agent');
  });
});
