import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createMemoryHistory, createRouter } from 'vue-router';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import AristotleChat from './AristotleChat.vue';
import AgentDetail from '../agents/AgentDetail.vue';
import ConversationRail from './ConversationRail.vue';
import SidebarFooter from '../../ui/SidebarFooter.vue';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { conversationKey } from './aristotle';
vi.mock('./Aristotle.vue', () => ({
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
async function mount(
  path = '/coresearch/new',
  online = true,
  settings = false,
  cacheAgent = true,
  rail = false,
  cacheMessages = true,
) {
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
  client.setQueryData(keys.agents('lab'), [{ ...agent, source: 'built_in' }]);
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
  if (cacheAgent) client.setQueryData(keys.agent('lab', 'aristotle'), agent);
  client.setQueryData(conversationKey('lab', 'me'), [welcome]);
  if (cacheMessages)
    client.setQueryData(keys.coresearchMessages('lab', 'private-project', 'chat'), []);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push(path);
  const app = createTreeApp({
    render: () =>
      rail
        ? h(ConversationRail, null, { footer: () => h(SidebarFooter, { userName: 'Lab', workspaceName: 'Lab' }) })
        : settings
          ? h(AgentDetail, { id: router.currentRoute.value.path.split('/')[2] || 'aristotle' })
          : router.currentRoute.value.path.startsWith('/coresearch')
            ? h(AristotleChat)
            : null,
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
  (byLabel(root, 'Message the agent').props.onInput as (e: unknown) => void)({ target: { value } });
}
const response = (body: unknown, status = 201) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
describe('Aristotle UI', () => {
  it('shows agent availability from the current researcher’s runtime health', async () => {
    const { root, client, settle } = await mount('/agents/aristotle', true, true);
    const status = () => all(root).find((n) => String(n.props.class).includes('agent-status'))!;
    expect(textOf(status())).toBe('Active');
    client.setQueryData(keys.runtimes('lab'), [
      { owner_id: 'me', status: 'online', harness: 'claude', unhealthy_reason: 'sign_in' },
    ]);
    await settle();
    expect(textOf(status())).toBe('Inactive');
    expect(String(status().props.class)).toContain('unavailable');
    client.setQueryData(keys.agent('lab', 'aristotle'), { ...agent, archived_at: '2026-01-01' });
    await settle();
    expect(textOf(status())).toBe('Archived');
  });
  it('keeps the agent inspector visible when switching detail tabs', async () => {
    const { root, settle } = await mount('/agents/aristotle', true, true);
    const inspector = all(root).find((n) => n.props.class === 'agent-inspector')!;
    expect(inspector).toBeTruthy();
    const breadcrumb = all(root).find((n) => n.props['aria-label'] === 'Agent navigation')!;
    expect(textOf(breadcrumb)).toContain('Agents');
    expect(textOf(breadcrumb)).toContain('Aristotle');
    expect(all(breadcrumb).find((n) => n.tag === 'a')?.props.href).toBe('/agents');
    expect(textOf(inspector)).toContain('Aristotle');
    expect(textOf(inspector)).toContain('Harness default');
    expect(textOf(root)).not.toContain('Duplicate');
    const work = all(root).find((n) => n.props.role === 'tab' && textOf(n).trim() === 'Work')!;
    (work.props.onClick as () => void)();
    await settle();
    expect(textOf(all(root).find((n) => n.props.class === 'agent-inspector')!)).toContain(
      'Aristotle',
    );
    expect(all(root).some((n) => n.props['aria-label'] === 'Search studies')).toBe(true);
  });
  it('places Capabilities second and offers Customize', async () => {
    const { root, settle } = await mount('/agents/aristotle', true, true);
    const tabs = all(root)
      .filter((n) => n.props.role === 'tab')
      .map((n) => textOf(n).trim());
    expect(tabs).toEqual(['Overview', 'Capabilities', 'Work', 'Settings']);
    const capabilities = all(root).find(
      (n) => n.props.role === 'tab' && textOf(n).trim() === 'Capabilities',
    )!;
    (capabilities.props.onClick as () => void)();
    await settle();
    expect(all(root).some((n) => n.tag === 'button' && textOf(n).trim() === 'Customize')).toBe(
      true,
    );
  });
  it('uses the conversation agent avatar in the rail', async () => {
    const { root, client, settle } = await mount('/coresearch/new', true, false, true, true);
    client.setQueryData(keys.agents('lab'), [
      { ...agent, id: 'custom', built_in_key: '', name: 'Analyst', avatar_url: 'emoji:🧪' },
    ]);
    client.setQueryData(conversationKey('lab', 'me'), [
      { ...welcome, id: 'custom-chat', agent_id: 'custom', title: 'Analysis' },
    ]);
    await settle();
    const row = all(root).find((n) => n.tag === 'a' && textOf(n).includes('Analysis'))!;
    expect(textOf(row)).toContain('🧪');
    expect(all(row).some((n) => n.props.class === 'chat-agent-avatar')).toBe(true);
  });
  it('starts a new conversation with the chosen agent and its starters', async () => {
    const fetch = vi.fn().mockRejectedValue(new Error('offline'));
    vi.stubGlobal('fetch', fetch);
    const { root, client, settle } = await mount('/coresearch/new');
    const custom = {
      ...agent,
      id: 'custom',
      built_in_key: '',
      name: 'Custom analyst',
      instructions: 'Analyze carefully',
      model: 'agent-model',
      conversation_starters: [{ label: 'Custom start', prompt: 'Exact custom prompt' }],
    };
    client.setQueryData(keys.runtimes('lab'), [
      {
        owner_id: 'me',
        status: 'online',
        harness: 'claude',
        models: [{ value: 'other-model', label: 'Other model' }],
      },
    ]);
    client.setQueryData(keys.agents('lab'), [
      { ...agent, source: 'built_in' },
      { ...custom, source: 'custom' },
    ]);
    await settle();
    (byLabel(root, 'Choose agent').props.onClick as () => void)();
    await settle();
    const option = all(root).find(
      (n) => n.tag === 'button' && textOf(n).trim() === 'Custom analyst',
    )!;
    (option.props.onClick as () => void)();
    await settle();
    expect(textOf(root)).toContain('Chat with Custom analyst');
    expect(textOf(root)).toContain('Custom start');
    const starter = all(root).find(
      (n) => n.tag === 'button' && textOf(n).trim() === 'Custom start',
    )!;
    await (starter.props.onClick as () => Promise<void>)();
    await settle();
    const sent = fetch.mock.calls.find((call) =>
      String(call[0]).endsWith('/api/coresearch/conversations'),
    )!;
    expect(JSON.parse(sent[1].body)).toMatchObject({
      agent_id: 'custom',
      body: 'Exact custom prompt',
    });
    expect(JSON.parse(sent[1].body)).not.toHaveProperty('model');
  });
  it.each(['custom', 'built_in'])(
    'allows %s agents to edit and save conversation starters',
    async (source) => {
      const fetch = vi.fn(async (_url: string, _init: { body: string }) => response(agent, 200));
      vi.stubGlobal('fetch', fetch);
      const { root, client, settle } = await mount('/agents/aristotle?tab=settings', true, true);
      client.setQueryData(keys.agent('lab', 'aristotle'), {
        ...agent,
        built_in_key: '',
        name: 'Agent',
      });
      client.setQueryData(keys.agents('lab'), [{ ...agent, source }]);
      await settle();
      expect(textOf(root)).toContain('Conversation starters');
      const add = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Add starter')!;
      (add.props.onClick as () => void)();
      await settle();
      (byLabel(root, 'Starter 1 label').props['onUpdate:modelValue'] as (s: string) => void)(
        'Investigate',
      );
      (byLabel(root, 'Starter 1 prompt').props['onUpdate:modelValue'] as (s: string) => void)(
        'Investigate this evidence',
      );
      await settle();
      const form = all(root).find(
        (n) => n.tag === 'form' && textOf(n).includes('Conversation starters'),
      )!;
      await (form.props.onSubmit as (event: unknown) => Promise<void>)({ preventDefault() {} });
      expect(JSON.parse(fetch.mock.calls[0]![1].body).conversation_starters).toEqual([
        { label: 'Investigate', prompt: 'Investigate this evidence' },
      ]);
    },
  );
  it('shows the runtime-reported model name and falls back when it is unknown', async () => {
    const { root, client, settle } = await mount('/agents/aristotle', true, true);
    client.setQueryData(keys.agent('lab', 'aristotle'), { ...agent, model: 'sonnet' });
    client.setQueryData(keys.runtimes('lab'), [
      {
        owner_id: 'me',
        status: 'online',
        harness: 'claude',
        models: [{ value: 'sonnet', label: 'Sonnet 5.5' }],
      },
    ]);
    await settle();
    expect(textOf(root).match(/Sonnet 5\.5/g)).toHaveLength(1);
    client.setQueryData(keys.runtimes('lab'), [
      {
        owner_id: 'me',
        status: 'online',
        harness: 'claude',
        models: [{ value: 'sonnet', label: 'Sonnet 5.5' }],
      },
      {
        owner_id: 'me',
        status: 'online',
        harness: 'claude',
        models: [{ value: 'sonnet', label: 'Sonnet 4.6' }],
      },
    ]);
    await settle();
    expect(textOf(root).match(/sonnet/g)).toHaveLength(1);
    client.setQueryData(keys.runtimes('lab'), []);
    await settle();
    expect(textOf(root).match(/sonnet/g)).toHaveLength(1);
  });
  it('only offers deletion for custom agents', async () => {
    const { root, client, settle } = await mount('/agents/aristotle', true, true);
    const teamAgent = { ...agent, built_in_key: '', name: 'Team agent' };
    client.setQueryData(keys.agent('lab', 'aristotle'), teamAgent);
    client.setQueryData(keys.agents('lab'), [{ ...teamAgent, source: 'built_in' }]);
    await settle();
    const deleteButton = () =>
      all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Delete agent');
    expect(deleteButton()).toBeUndefined();
    client.setQueryData(keys.agents('lab'), [{ ...teamAgent, source: 'custom' }]);
    await settle();
    expect(deleteButton()).toBeTruthy();
  });
  it('offers only available CLIs when the agent preference is offline', async () => {
    const fetch = vi.fn().mockRejectedValue(new Error('offline'));
    vi.stubGlobal('fetch', fetch);
    const { root, client, settle } = await mount();
    client.setQueryData(keys.runtimes('lab'), [
      { id: 'c', owner_id: 'me', status: 'online', harness: 'claude', unhealthy_reason: 'sign_in', models: [] },
      { id: 'x', owner_id: 'me', status: 'online', harness: 'codex', models: [] },
    ]);
    await settle();
    const picker = byLabel(root, 'CLI and model: Codex · Default');
    expect(picker).toBeTruthy();
    await (picker.props.onClick as () => Promise<void>)();
    await settle();
    expect(byLabel(root, 'Codex')).toBeTruthy();
    expect(byLabel(root, 'Claude Code')).toBeUndefined();
    edit(root, 'A question');
    await settle();
    await (byLabel(root, 'Send').props.onClick as () => Promise<void>)();
    expect(JSON.parse(fetch.mock.calls[0]![1].body).harness).toBe('codex');
  });
  it('allows model selection for the available CLI', async () => {
    const { root, client, settle } = await mount();
    client.setQueryData(keys.agent('lab', 'aristotle'), { ...agent, model: 'sonnet' });
    client.setQueryData(keys.runtimes('lab'), [
      {
        owner_id: 'me',
        status: 'online',
        harness: 'claude',
        models: [
          { value: 'sonnet', label: 'Sonnet' },
          { value: 'opus', label: 'Opus' },
        ],
      },
    ]);
    await settle();
    const indicator = byLabel(root, 'CLI and model: Claude Code · Sonnet');
    expect(indicator).toBeTruthy();
    await (indicator.props.onClick as () => Promise<void>)();
    await settle();
    expect(byLabel(root, 'CLI')).toBeTruthy();
    (byLabel(root, 'Opus').props.onClick as () => void)();
    await settle();
    expect(textOf(byLabel(root, 'CLI and model: Claude Code · Opus'))).toContain('Opus');
  });
  it('shows uncached welcome history errors and restores history on explicit retry', async () => {
    let resolveHistory!: (value: Response) => void;
    const fetch = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            resolveHistory = resolve;
          }),
      )
      .mockResolvedValueOnce(
        response([{ id: 'old-message', kind: 'comment', body: 'Existing research history' }], 200),
      );
    vi.stubGlobal('fetch', fetch);
    const { root, settle } = await mount(
      '/coresearch/conversations/welcome',
      true,
      false,
      true,
      false,
      false,
    );
    expect.soft(textOf(root)).toContain('Loading conversation…');
    expect.soft(textOf(root)).not.toContain('Welcome to Coresearch');
    edit(root, 'Keep my draft');
    await settle();
    expect.soft(byLabel(root, 'Send').props.disabled).toBe(true);
    await (byLabel(root, 'Send').props.onClick as () => Promise<void>)();
    expect(fetch).toHaveBeenCalledTimes(1);
    resolveHistory(response({ error: 'History unavailable' }, 500));
    await settle();
    await settle();
    expect(textOf(root)).toContain('History unavailable');
    expect(textOf(root)).not.toContain('Welcome to Coresearch');
    expect(byLabel(root, 'Message the agent').props.value).toBe('Keep my draft');
    await (byLabel(root, 'Send').props.onClick as () => Promise<void>)();
    expect(fetch).toHaveBeenCalledTimes(1);
    const retry = all(root).find((n) => n.tag === 'button' && textOf(n) === 'Retry')!;
    await (retry.props.onClick as () => Promise<void>)();
    await settle();
    const historyCalls = fetch.mock.calls.filter((c) => String(c[0]).endsWith('/messages'));
    expect(historyCalls).toHaveLength(2);
    expect(all(root).some((n) => n.props.innerHTML === 'Existing research history')).toBe(true);
    expect(textOf(root)).not.toContain('History unavailable');
    expect(textOf(root)).not.toContain('Welcome to Coresearch');
    expect(byLabel(root, 'Message the agent').props.value).toBe('Keep my draft');
    expect(byLabel(root, 'Send').props.disabled).toBe(false);
  });

  it('keeps the conversation rail independent from Projects and their query', async () => {
    const fetch = vi.fn().mockResolvedValue(response([], 200));
    vi.stubGlobal('fetch', fetch);
    const { root, client } = await mount('/coresearch/new', true, false, true, true);
    expect.soft(fetch).not.toHaveBeenCalled();
    expect.soft(client.getQueryState(keys.coresearchProjects('lab'))).toBeUndefined();
    expect.soft(byLabel(root, 'Chat')?.tag).toBe('aside');
    expect.soft(textOf(root)).not.toContain('Projects');
    const links = all(root).filter((n) => n.tag === 'a');
    expect(links.map((n) => n.props.href)).toEqual([
      '/coresearch/new',
      '/coresearch/conversations/welcome',
    ]);
    expect(textOf(root)).not.toContain('Chat');
    expect(byLabel(root, 'New conversation').props.href).toBe('/coresearch/new');
    expect(textOf(root)).toContain('Getting started with Aristotle');
    expect(textOf(root)).toContain('Lab');
    expect(byLabel(root, 'Help')).toBeDefined();
  });
  it.each(['experiment', 'cowrite'])(
    'preserves an existing starter alongside a separate %s run',
    async (mode) => {
      const { root, client, settle } = await mount('/coresearch/new', true, false, true, true);
      client.setQueryData(conversationKey('lab', 'me'), [
        welcome,
        { ...welcome, id: 'run', is_welcome: false, mode, title: 'Research run' },
      ]);
      await settle();
      expect(textOf(root)).toContain('Research run');
      expect(textOf(root)).toContain('Getting started with Aristotle');
    },
  );
  it('keeps the starter when only a shared session exists', async () => {
    const { root, client, settle } = await mount('/coresearch/new', true, false, true, true);
    client.setQueryData(conversationKey('lab', 'me'), [
      welcome,
      {
        shared: true,
        mode: 'cowrite',
        project_id: 'shared',
        chat_id: 'chat',
        title: 'Shared paper',
      },
    ]);
    await settle();
    expect(textOf(root)).toContain('Getting started with Aristotle');
    expect(textOf(root)).toContain('Shared paper');
  });
  it('deletes an owned chat and its project after confirmation', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetch);
    vi.stubGlobal('confirm', () => true);
    const { root, settle } = await mount('/coresearch/new', true, false, true, true);
    const remove = byLabel(root, 'Delete Getting started with Aristotle');
    expect(remove).toBeTruthy();
    await (remove.props.onClick as () => Promise<void>)();
    await settle();
    expect(
      fetch.mock.calls.some(
        (c) => c[0] === '/api/coresearch/conversations/welcome' && c[1].method === 'DELETE',
      ),
    ).toBe(true);
  });
  it('does not delete when confirmation is declined and offers no delete for shared sessions', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    vi.stubGlobal('confirm', () => false);
    const { root, client, settle } = await mount('/coresearch/new', true, false, true, true);
    client.setQueryData(conversationKey('lab', 'me'), [
      welcome,
      {
        shared: true,
        mode: 'cowrite',
        project_id: 'shared',
        chat_id: 'chat',
        title: 'Shared paper',
      },
    ]);
    await settle();
    expect(byLabel(root, 'Delete Shared paper')).toBeUndefined();
    await (
      byLabel(root, 'Delete Getting started with Aristotle').props.onClick as () => Promise<void>
    )();
    expect(fetch).not.toHaveBeenCalled();
  });
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
    expect(byLabel(root, 'Message the agent').props.value).toBe('Question');
    await (byLabel(root, 'Send').props.onClick as () => Promise<void>)();
    const bodies = () => fetch.mock.calls.map((c) => JSON.parse(c[1].body));
    expect(bodies()[0].request_id).toBe(bodies()[1].request_id);
    edit(root, 'Edited');
    await settle();
    await (byLabel(root, 'Send').props.onClick as () => Promise<void>)();
    expect(bodies()[2].request_id).not.toBe(bodies()[0].request_id);
  });
  it('blocks unavailable machines without showing the machine notice', async () => {
    const { root, settle } = await mount('/coresearch/new', false);
    edit(root, 'Question');
    await settle();
    expect(byLabel(root, 'Send').props.disabled).toBe(true);
    expect(textOf(root)).not.toContain('No available own machine');
    expect(textOf(root)).toContain('No CLI available');
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
    expect(byLabel(root, 'Send').props.disabled).toBe(true);
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
    client.setQueryData(keys.agents('other-lab'), [{ ...otherAgent, source: 'built_in' }]);
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
    expect(byLabel(root, 'Message the agent').props.value).toBe('');
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
  it('shows and retries a failed live agent GET after successful bootstrap', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(response({ error: 'Profile temporarily unavailable' }, 500))
      .mockResolvedValue(response(agent, 200));
    vi.stubGlobal('fetch', fetch);
    const { root, settle } = await mount('/coresearch/new', true, false, false);
    await settle();
    expect(textOf(root)).toContain('Profile temporarily unavailable');
    expect(textOf(root)).not.toContain('Loading Aristotle');
    const retry = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Try again')!;
    (retry.props.onClick as () => void)();
    await settle();
    await settle();
    expect(textOf(root)).toContain('Chat with Aristotle');
    expect(fetch).toHaveBeenCalledTimes(2);
  });
  it.each(['/coresearch/conversations/welcome', '/agents'])(
    'does not overwrite a newer draft after navigating away to %s and back during a send',
    async (away) => {
      let resolveAcceptance!: (r: Response) => void;
      vi.stubGlobal(
        'fetch',
        vi.fn((_url, init) =>
          init.method === 'GET'
            ? Promise.resolve(
                response(String(_url).endsWith('/conversations') ? [welcome] : [], 200),
              )
            : new Promise<Response>((resolve) => {
                resolveAcceptance = resolve;
              }),
        ),
      );
      const { root, router, settle } = await mount();
      edit(root, 'Original question');
      await settle();
      (byLabel(root, 'Send').props.onClick as () => void)();
      await router.push(away);
      await settle();
      await router.push('/coresearch/new');
      await settle();
      edit(root, 'Newer question');
      await settle();
      const resolveOld = resolveAcceptance;
      (byLabel(root, 'Send').props.onClick as () => void)();
      await settle();
      resolveOld(
        response({ conversation: { ...welcome, id: 'accepted-old' }, run: { id: 'old-run' } }),
      );
      await settle();
      await settle();
      expect(router.currentRoute.value.path).toBe('/coresearch/new');
      expect(byLabel(root, 'Message the agent').props.value).toBe('Newer question');
      expect(byLabel(root, 'Send').props.disabled).toBe(true);
      expect(JSON.parse(storage.get('lykeion.aristotle.accepted:me:lab')!).run).toBe('old-run');
    },
  );
  it('resets agent edit sessions and prevents an old save from clearing a newer save', async () => {
    const resolvers: ((r: Response) => void)[] = [];
    const fetch = vi.fn((url: string) =>
      url.endsWith('/api/agents')
        ? Promise.resolve(response([{ ...agent, source: 'built_in' }], 200))
        : new Promise<Response>((resolve) => resolvers.push(resolve)),
    );
    vi.stubGlobal('fetch', fetch);
    const { root, client, router, settle } = await mount(
      '/agents/aristotle?tab=settings',
      true,
      true,
    );
    const form = () => all(root).find((n) => n.tag === 'form' && textOf(n).includes('General'))!;
    const save = () =>
      all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Save changes')!;
    (form().props.onSubmit as (e: unknown) => void)({ preventDefault() {} });
    await settle();
    expect(save().props.disabled).toBe(true);
    const other = { ...agent, id: 'other-agent', name: 'Second Aristotle' };
    client.setQueryData(keys.agent('lab', 'other-agent'), other);
    await router.push('/agents/other-agent?tab=settings');
    await settle();
    expect(save().props.disabled).toBe(false);
    await router.push('/agents/aristotle?tab=settings');
    await settle();
    (form().props.onSubmit as (e: unknown) => void)({ preventDefault() {} });
    await settle();
    expect(save().props.disabled).toBe(true);
    resolvers[0]!(response(agent));
    await settle();
    expect(save().props.disabled).toBe(true);
    resolvers[1]!(response(agent));
    await settle();
    expect(save().props.disabled).toBe(false);
    expect(byLabel(root, 'Agent name').value).toBe('Aristotle');
  });
  it('shows research and writing welcome cards while using saved starters only in a new draft', async () => {
    const fetch = vi.fn().mockRejectedValue(new Error('offline'));
    vi.stubGlobal('fetch', fetch);
    const { root, client, router, settle } = await mount('/coresearch/conversations/welcome');
    client.setQueryData(keys.agent('lab', 'aristotle'), {
      ...agent,
      conversation_starters: [{ label: 'Custom research action', prompt: 'Custom saved prompt' }],
    });
    await settle();
    const welcomeCards = all(root).filter(
      (n) =>
        n.tag === 'button' &&
        all(n).some(
          (child) =>
            child.props.class === 'research-motif motif-0' ||
            String(child.props.class).includes('research-motif'),
        ),
    );
    expect(welcomeCards).toHaveLength(4);
    expect(textOf(root)).toContain('Write a research paper');
    expect(textOf(root)).toContain('Ground a research question');
    expect(textOf(root)).toContain('Design a reproducible experiment');
    expect(textOf(root)).toContain('Review evidence and findings');
    expect(textOf(root)).not.toContain('Custom research action');
    (welcomeCards[0]!.props.onClick as () => void)();
    await settle();
    expect(JSON.parse(fetch.mock.calls[0]![1].body).body).toBe(
      'Help me turn my research question into a focused, evidence-grounded question. Ask about the topic and constraints first.',
    );
    await router.push('/coresearch/new');
    await settle();
    expect(textOf(root)).toContain('Custom research action');
    expect(textOf(root)).not.toContain('Ground a research question');
    const starterStack = all(root).find((n) => n.props.class === 'starter-stack')!;
    expect(starterStack.children.filter((n) => n.tag === 'button')).toHaveLength(1);
  });
  it('starts the writing starter in Cowrite mode', async () => {
    const fetch = vi.fn().mockRejectedValue(new Error('offline'));
    vi.stubGlobal('fetch', fetch);
    const { root, settle } = await mount('/coresearch/conversations/welcome');
    const card = all(root).find(
      (n) => n.tag === 'button' && textOf(n).includes('Write a research paper'),
    )!;
    (card.props.onClick as () => void)();
    await settle();
    expect(JSON.parse(fetch.mock.calls[0]![1].body)).toMatchObject({
      mode: 'cowrite',
      body: expect.stringContaining('write a research paper'),
    });
  });
});
