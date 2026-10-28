import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createMemoryHistory, createRouter } from 'vue-router';
import { all, createTreeApp, node, textOf } from './daemon-tree';
import AristotleChat from '../apps/dashboard/src/coresearch/AristotleChat.vue';
import AgentDetail from '../apps/dashboard/src/views/AgentDetail.vue';
import RailFabs from '../apps/dashboard/src/coresearch/RailFabs.vue';
import { keys } from '../apps/dashboard/src/api/keys';
import { workspaceId } from '../apps/dashboard/src/api/session';
import { conversationKey } from '../apps/dashboard/src/coresearch/aristotle';
vi.mock('../apps/dashboard/src/components/Aristotle.vue', () => ({
  default: { render: () => null },
}));
const storage = vi.hoisted(() => new Map<string, string>());
vi.hoisted(() =>
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (k: string) => storage.get(k) ?? null,
      setItem: (k: string, v: string) => storage.set(k, v),
      removeItem: (k: string) => storage.delete(k),
    },
  }),
);
const cleanups: (() => void)[] = [];
afterEach(() => {
  cleanups.splice(0).forEach((f) => f());
  vi.unstubAllGlobals();
  storage.clear();
});
const agent = {
  id: 'aristotle',
  workspace_id: 'lab',
  owner_id: 'me',
  built_in_key: 'aristotle',
  name: 'Aristotle',
  description: 'Research together',
  instructions: '',
  harness: 'claude',
  model: '',
  visibility: 'private',
  archived_at: null,
  conversation_starters: [],
};
const welcome = {
  id: 'welcome',
  project_id: 'private-project',
  chat_id: 'chat',
  agent_id: 'aristotle',
  owner_id: 'me',
  workspace_id: 'lab',
  is_welcome: true,
  title: 'Getting started with Aristotle',
  preview: '',
  snapshot_harness: null,
  snapshot_model: null,
  snapshot_instructions: null,
};
async function mount(path = '/coresearch/new', online = true, settings = false) {
  vi.stubGlobal('document', {
    activeElement: null,
    addEventListener() {},
    removeEventListener() {},
  });
  vi.stubGlobal('Document', class {});
  vi.stubGlobal('ShadowRoot', class {});
  workspaceId.value = 'lab';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  });
  client.setQueryData(keys.me, { id: 'me', name: 'Ada' });
  client.setQueryData(keys.workspaces, [{ id: 'lab', role: 'owner' }]);
  client.setQueryData(keys.workspace('lab'), {});
  client.setQueryData(keys.cards('lab'), []);
  client.setQueryData(keys.members('lab'), []);
  client.setQueryData(
    keys.runtimes('lab'),
    online ? [{ owner_id: 'me', status: 'online', harness: 'claude' }] : [],
  );
  client.setQueryData(['ws', 'lab', 'aristotle', 'me', 'bootstrap'], {
    agent,
    conversation: welcome,
  });
  client.setQueryData(keys.agent('lab', 'aristotle'), agent);
  client.setQueryData(conversationKey('lab', 'me'), [welcome]);
  client.setQueryData(keys.coresearchMessages('lab', 'private-project', 'chat'), []);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push(path);
  const app = createTreeApp({
    render: () => (settings ? h(AgentDetail, { id: 'aristotle' }) : h(AristotleChat)),
  });
  app.use(VueQueryPlugin, { queryClient: client });
  app.use(router);
  const root = node('root');
  app.mount(root);
  const settle = async () => {
    await new Promise((r) => setTimeout(r, 0));
    await nextTick();
  };
  await settle();
  cleanups.push(() => {
    app.unmount();
    client.clear();
  });
  return { root, client, router, settle };
}
function byLabel(root: ReturnType<typeof node>, label: string) {
  return all(root).find((n) => n.props['aria-label'] === label)!;
}
function edit(root: ReturnType<typeof node>, value: string) {
  (byLabel(root, 'Message Aristotle').props.onInput as (e: unknown) => void)({ target: { value } });
}
const response = (body: unknown, status = 201) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
describe('Aristotle UI', () => {
  it('keeps a new draft unsaved and exposes the saved starter settings link', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const { root } = await mount();
    expect(textOf(root)).toContain('Chat with Aristotle');
    expect(all(root).filter((n) => n.tag === 'header')).toHaveLength(0);
    expect(all(root).find((n) => textOf(n) === 'Customize starters')?.props.href).toBe(
      '/agents/aristotle?tab=settings',
    );
    expect(fetch).not.toHaveBeenCalled();
  });
  it('retains draft and request UUID after transport failure; edited content uses a new UUID', async () => {
    const fetch = vi.fn().mockRejectedValue(new Error('network lost'));
    vi.stubGlobal('fetch', fetch);
    const { root, settle } = await mount();
    edit(root, 'Question');
    await settle();
    await (byLabel(root, 'Send').props.onClick as () => Promise<void>)();
    await settle();
    expect(byLabel(root, 'Message Aristotle').props.value).toBe('Question');
    await (byLabel(root, 'Send').props.onClick as () => Promise<void>)();
    const bodies = () => fetch.mock.calls.map((c) => JSON.parse(c[1].body));
    expect(bodies()[0].request_id).toBe(bodies()[1].request_id);
    edit(root, 'Edited');
    await settle();
    await (byLabel(root, 'Send').props.onClick as () => Promise<void>)();
    expect(bodies()[2].request_id).not.toBe(bodies()[0].request_id);
  });
  it('blocks unavailable machines while retaining settings access', async () => {
    const { root, settle } = await mount('/coresearch/new', false);
    edit(root, 'Question');
    await settle();
    expect(byLabel(root, 'Send').props.disabled).toBe(true);
    expect(textOf(root)).toContain('No available own machine with claude');
    expect(all(root).some((n) => n.props.href === '/settings?tab=machines')).toBe(true);
  });
  it('restores last valid conversation and falls back from a stale ID', async () => {
    storage.set('lykeion.aristotle.last:me:lab', 'deleted');
    const { router } = await mount('/coresearch');
    expect(router.currentRoute.value.path).toBe('/coresearch/conversations/welcome');
  });
  it('honors the existing conversation harness snapshot over edited live agent settings', async () => {
    const { root, client, settle } = await mount('/coresearch/conversations/welcome');
    client.setQueryData(conversationKey('lab', 'me'), [
      { ...welcome, snapshot_harness: 'codex', snapshot_model: 'frozen' },
    ]);
    await settle();
    expect(textOf(root)).toContain('No available own machine with codex');
  });
  it('opens query settings on reload and saves starter label/prompt through the real agent patch', async () => {
    const fetch = vi
      .fn()
      .mockImplementation((_url, init) => response({ ...agent, ...JSON.parse(init.body) }));
    vi.stubGlobal('fetch', fetch);
    const { root, settle, client } = await mount('/agents/aristotle?tab=settings', true, true);
    expect(textOf(root)).toContain('Conversation starters');
    const add = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Add starter')!;
    (add.props.onClick as () => void)();
    await settle();
    (byLabel(root, 'Starter 1 label').props['onUpdate:modelValue'] as (s: string) => void)(
      'My question',
    );
    (byLabel(root, 'Starter 1 prompt').props['onUpdate:modelValue'] as (s: string) => void)(
      'Explore my evidence',
    );
    await settle();
    const form = all(root).find(
      (n) => n.tag === 'form' && textOf(n).includes('Conversation starters'),
    )!;
    await (form.props.onSubmit as (e: unknown) => Promise<void>)({ preventDefault() {} });
    expect(JSON.parse(fetch.mock.calls[0]![1].body).conversation_starters).toEqual([
      { label: 'My question', prompt: 'Explore my evidence' },
    ]);
    expect(
      client.getQueryData<{ conversation_starters: unknown[] }>(keys.agent('lab', 'aristotle'))
        ?.conversation_starters,
    ).toHaveLength(1);
  });
  it('preserves accepted IDs without navigating or leaking state after a lab switch', async () => {
    let resolveAcceptance!: (r: Response) => void;
    const fetch = vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          resolveAcceptance = resolve;
        }),
    );
    vi.stubGlobal('fetch', fetch);
    const { root, client, router, settle } = await mount();
    edit(root, 'Old lab question');
    await settle();
    (byLabel(root, 'Send').props.onClick as () => void)();
    const otherAgent = {
      ...agent,
      id: 'other-agent',
      workspace_id: 'other-lab',
      name: 'Other Aristotle',
    };
    client.setQueryData(['ws', 'other-lab', 'aristotle', 'me', 'bootstrap'], {
      agent: otherAgent,
      conversation: { ...welcome, id: 'other-welcome', workspace_id: 'other-lab' },
    });
    client.setQueryData(keys.agent('other-lab', 'other-agent'), otherAgent);
    client.setQueryData(conversationKey('other-lab', 'me'), []);
    client.setQueryData(keys.runtimes('other-lab'), []);
    workspaceId.value = 'other-lab';
    await settle();
    resolveAcceptance(
      response({
        conversation: { ...welcome, id: 'accepted', is_welcome: false },
        run: { id: 'run-accepted' },
      }),
    );
    await settle();
    await settle();
    expect(router.currentRoute.value.path).toBe('/coresearch/new');
    expect(byLabel(root, 'Message Aristotle').props.value).toBe('');
    expect(textOf(root)).toContain('Other Aristotle');
    expect(JSON.parse(storage.get('lykeion.aristotle.accepted:me:lab')!)).toEqual({
      conversation: 'accepted',
      project: 'private-project',
      chat: 'chat',
      run: 'run-accepted',
    });
  });
  it('sends the saved starter prompt, not its display label', async () => {
    const fetch = vi.fn().mockRejectedValue(new Error('offline'));
    vi.stubGlobal('fetch', fetch);
    const { root, client, settle } = await mount();
    client.setQueryData(keys.agent('lab', 'aristotle'), {
      ...agent,
      conversation_starters: [{ label: 'Custom label', prompt: 'Exact saved research prompt' }],
    });
    await settle();
    const button = all(root).find(
      (n) => n.tag === 'button' && textOf(n).trim() === 'Custom label',
    )!;
    (button.props.onClick as () => void)();
    await settle();
    expect(JSON.parse(fetch.mock.calls[0]![1].body).body).toBe('Exact saved research prompt');
  });
  it('provides mutually exclusive rail controls with accessible labels', () => {
    const change = vi.fn();
    const app = createTreeApp({ render: () => h(RailFabs, { view: 'context', onChange: change }) });
    const root = node('root');
    app.mount(root);
    expect(byLabel(root, 'Show conversations').props['aria-pressed']).toBe(true);
    expect(byLabel(root, 'Show navigation').props['aria-pressed']).toBe(false);
    (byLabel(root, 'Show navigation').props.onClick as () => void)();
    expect(change).toHaveBeenCalledWith('nav');
    app.unmount();
  });
});
