import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createMemoryHistory, createRouter } from 'vue-router';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import AristotleChat from './AristotleChat.vue';
import AristotleComposer from './AristotleComposer.vue';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import {
  conversationKey,
  holdComposerContinuation,
  takeComposerContinuation,
  clearComposerContinuation,
  newComposerDraft,
} from './aristotle';
import { authenticationLost, authenticationRestored } from '../../api/auth-loss';
vi.mock('./Aristotle.vue', () => ({
  default: { render: () => null },
}));
const cleanups: (() => void)[] = [];
afterEach(() => {
  clearComposerContinuation();
  cleanups.splice(0).forEach((f) => f());
  vi.unstubAllGlobals();
});
const welcome = {
  id: 'welcome',
  project_id: 'private',
  chat_id: 'chat',
  agent_id: 'aristotle',
  owner_id: 'me',
  workspace_id: 'lab',
  is_welcome: true,
  title: 'Welcome',
  snapshot_harness: null,
  snapshot_model: null,
};
const models = [
  { value: 'cli-default', label: 'Default (recommended)', description: 'CLI default' },
  { value: 'cli-large', label: 'Large', description: 'More capacity' },
];
async function mount({
  draft = true,
  onWelcome = false,
  role = 'owner',
  choices = models,
  harness = 'claude',
} = {}) {
  vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal('document', {
    activeElement: null,
    addEventListener() {},
    removeEventListener() {},
  });
  vi.stubGlobal('localStorage', { getItem: () => null, setItem() {} });
  workspaceId.value = 'lab';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  });
  const agent = {
    id: 'aristotle',
    name: 'Aristotle',
    harness,
    model: '',
    conversation_starters: [],
  };
  client.setQueryData(keys.me, { id: 'me' });
  client.setQueryData(keys.agents('lab'), [
    {
      ...agent,
      owner_id: 'me',
      visibility: 'private',
      built_in_key: 'aristotle',
      archived_at: null,
    },
  ]);
  client.setQueryData(keys.workspaces, [{ id: 'lab', role }]);
  client.setQueryData(keys.runtimes('lab'), [
    { owner_id: 'me', status: 'online', harness, models: choices },
    {
      owner_id: 'other',
      status: 'online',
      harness: 'claude',
      models: [{ value: 'private', label: 'Other member' }],
    },
  ]);
  client.setQueryData(['ws', 'lab', 'aristotle', 'me', 'bootstrap'], {
    agent,
    conversation: welcome,
  });
  client.setQueryData(keys.agent('lab', 'aristotle'), agent);
  client.setQueryData(conversationKey('lab', 'me'), [
    {
      ...welcome,
      is_welcome: draft || onWelcome,
      snapshot_model: draft || onWelcome ? null : 'frozen-model',
    },
  ]);
  client.setQueryData(keys.coresearchMessages('lab', 'private', 'chat'), []);
  client.setQueryData(keys.skills('lab'), [
    {
      id: 'skill',
      name: 'Research',
      description: 'Find evidence',
      policy_enabled: true,
      policy_revision: 2,
    },
  ]);
  client.setQueryData(keys.connectors('lab'), [
    {
      name: 'openalex',
      purpose: 'Research papers',
      policy_enabled: true,
      enabled: true,
      revision: 3,
    },
  ]);
  client.setQueryData(keys.workspaceMCP('lab'), []);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push(draft ? '/coresearch/new' : '/coresearch/conversations/welcome');
  const app = createTreeApp({ render: () => h(AristotleChat) });
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
  const label = (s: string) => all(root).find((n) => n.props['aria-label'] === s)!;
  const click = async (s: string) => {
    const n = all(root).find(
      (n) => n.tag === 'button' && (n.props['aria-label'] === s || textOf(n).trim() === s),
    )!;
    expect(n, s).toBeDefined();
    await (n.props.onClick as () => unknown)();
    await settle();
  };
  const edit = async (value: string) => {
    (all(root).find((n) => n.tag === 'textarea')!.props.onInput as (e: unknown) => void)({
      target: { value, selectionStart: value.length },
    });
    await settle();
  };
  return { root, router, client, settle, label, click, edit };
}
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
describe('Cowrite composer', () => {
  const held = () => ({
    scope: 'me:lab',
    path: '/coresearch/new',
    position: 2,
    body: 'Paper',
    mode: 'cowrite' as const,
    template: 'neurips',
    harness: 'claude',
    runtime: 'own',
    model: 'cli-large',
    attachments: [{ upload_id: 'held', name: 'figure.svg' }],
    pending: { body: 'Paper', target: '', request_id: 'receipt', payload: 'same' },
  });
  it('consumes a Settings continuation once with exact upload and receipt identities', () => {
    const value = held();
    holdComposerContinuation(value);
    value.attachments[0]!.name = 'changed.svg';
    expect(takeComposerContinuation('me:lab', '/coresearch/new', 2)).toEqual(held());
    expect(takeComposerContinuation('me:lab', '/coresearch/new', 2)).toBeNull();
  });
  it.each([
    ['other:lab', '/coresearch/new', 2],
    ['me:other', '/coresearch/new', 2],
    ['me:lab', '/coresearch/conversations/welcome', 2],
    ['me:lab', '/coresearch/new', 3],
  ] as const)(
    'refuses a continuation at a different scope, route or history position: %s %s %s',
    (scope, path, position) => {
      holdComposerContinuation(held());
      expect(takeComposerContinuation(scope, path, position)).toBeNull();
      expect(takeComposerContinuation('me:lab', '/coresearch/new', 2)).toBeNull();
    },
  );
  it.each([
    clearComposerContinuation,
    newComposerDraft,
    authenticationLost,
    authenticationRestored,
  ])('clears a continuation on explicit discard, New, or authentication transitions', (clear) => {
    holdComposerContinuation(held());
    clear();
    expect(takeComposerContinuation('me:lab', '/coresearch/new', 2)).toBeNull();
  });
  it('clears a held Settings continuation when the workspace or person changes', async () => {
    const m = await mount();
    holdComposerContinuation(held());
    workspaceId.value = 'other';
    expect(takeComposerContinuation('me:lab', '/coresearch/new', 2)).toBeNull();
    workspaceId.value = 'lab';
    await m.settle();
    holdComposerContinuation(held());
    m.client.setQueryData(keys.me, { id: 'other-person' });
    await m.settle();
    expect(takeComposerContinuation('me:lab', '/coresearch/new', 2)).toBeNull();
  });
  it('requires a description before starting a paper', async () => {
    const m = await mount();
    await m.click('Cowrite');
    expect(m.label('Start paper').props.disabled).toBe(true);
    await m.edit('A description');
    expect(m.label('Start paper').props.disabled).toBe(false);
  });
  it('offers the pinned draft mode strip, Cowrite placeholder and server templates', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        response([
          { id: 'blank', label: 'Blank' },
          { id: 'neurips', label: 'NeurIPS' },
        ]),
      ),
    );
    const m = await mount();
    expect(m.label('Mode')).toBeDefined();
    expect(all(m.root).filter((n) => n.tag === 'textarea')).toHaveLength(1);
    expect(
      all(m.root).find((n) => String(n.props.class).includes('aristotle-composer'))!.parent?.props
        .class,
    ).toBe('aristotle-scroll');
    expect(all(m.root).find((n) => n.tag === 'textarea')!.props.placeholder).toBe(
      'What are you running? — / for skills',
    );
    await m.click('Cowrite');
    await m.click('Add');
    await m.click('Template');
    expect(all(m.root).find((n) => n.tag === 'textarea')!.props.placeholder).toBe(
      'What is the paper about?',
    );
    expect(m.label('Template')).toBeDefined();
    expect(textOf(m.root)).toContain('NeurIPS');
  });
  it('posts the chosen model, template and mode and navigates Cowrite acceptance', async () => {
    const fetch = vi.fn((_url, init) =>
      Promise.resolve(
        response(
          init.method === 'GET'
            ? [{ id: 'neurips', label: 'NeurIPS' }]
            : {
                conversation: {
                  ...welcome,
                  id: 'accepted',
                  project_id: 'paper',
                  chat_id: 'paper-chat',
                  mode: 'cowrite',
                },
                run: { id: 'run' },
              },
          init.method === 'GET' ? 200 : 201,
        ),
      ),
    );
    vi.stubGlobal('fetch', fetch);
    const m = await mount();
    await m.click('Cowrite');
    await m.click('Add');
    await m.click('Template');
    await m.click('NeurIPS');
    await m.edit('A new paper');
    await m.click('Start paper');
    const sent = fetch.mock.calls.find((c) => c[1].method === 'POST')!;
    expect(sent[0]).toBe('/api/coresearch/conversations');
    expect(JSON.parse(sent[1].body)).toMatchObject({
      body: 'A new paper',
      mode: 'cowrite',
      template: 'neurips',
    });
    expect(m.router.currentRoute.value.path).toBe('/coresearch/paper/paper-chat');
  });
  it('turns the welcome conversation into a session in the chosen mode', async () => {
    const fetch = vi.fn((_url, init) =>
      Promise.resolve(
        response(
          init.method === 'GET'
            ? [{ id: 'neurips', label: 'NeurIPS' }]
            : {
                conversation: {
                  ...welcome,
                  id: 'welcome',
                  is_welcome: false,
                  mode: 'cowrite',
                },
                run: { id: 'run' },
              },
          init.method === 'GET' ? 200 : 201,
        ),
      ),
    );
    vi.stubGlobal('fetch', fetch);
    const m = await mount({ draft: false, onWelcome: true });
    expect(m.label('Mode')).toBeDefined();
    await m.click('Cowrite');
    await m.edit('A new paper');
    await m.click('Start paper');
    const sent = fetch.mock.calls.find((c) => c[1].method === 'POST')!;
    expect(sent[0]).toBe('/api/coresearch/conversations/welcome/turns');
    expect(JSON.parse(sent[1].body)).toMatchObject({ body: 'A new paper', mode: 'cowrite' });
    expect(m.router.currentRoute.value.path).toBe('/coresearch/private/chat');
  });
  it.each([
    { catalogue: 'absent', choices: [] },
    { catalogue: 'available', choices: models },
  ])('shows the recommended default in both modes with the catalogue $catalogue', async ({ choices }) => {
    const m = await mount({ choices });
    const picker = () => m.label('CLI and model: Claude Code · Default (recommended)');
    expect(textOf(picker())).toBe('Default (recommended)');
    await m.click('Cowrite');
    expect(textOf(picker())).toBe('Default (recommended)');
    expect(textOf(m.root)).not.toContain('Other member');
  });
  it('shows Codex in both modes while keeping the selected model in the menu', async () => {
    const m = await mount({ harness: 'codex' });
    await m.click('CLI and model: Codex · Default (recommended)');
    await m.click('Large — More capacity');
    const picker = () => m.label('CLI and model: Codex · Large');
    expect(textOf(picker())).toBe('Codex');
    await m.click('Cowrite');
    expect(textOf(picker())).toBe('Codex');
    await m.click('CLI and model: Codex · Large');
    expect(m.label('Large — More capacity').props['aria-pressed']).toBe(true);
  });
  it('hides mode in a conversation and disables its frozen snapshot model', async () => {
    const m = await mount({ draft: false });
    expect(m.label('Mode')).toBeUndefined();
    expect(m.label('CLI and model: Claude Code · frozen-model').props.disabled).toBe(true);
    await m.click('Add');
    expect(textOf(m.root)).not.toContain('Template');
  });
  it('sends selected capabilities with the current message without changing workspace policy', async () => {
    const fetch = vi.fn().mockRejectedValue(new Error('offline'));
    vi.stubGlobal('fetch', fetch);
    const m = await mount({ role: 'member' });
    await m.click('Add');
    await m.click('Skills');
    await m.click('Research');
    expect(all(m.root).find((n) => n.tag === 'textarea')!.props.value).toBe('');
    expect(m.label('Remove Research')).toBeDefined();
    await m.edit('Analyze this');
    await m.click('Send');
    const post = fetch.mock.calls.find((c) => c[1]?.method === 'POST')!;
    expect(JSON.parse(post[1].body)).toMatchObject({
      body: 'Analyze this',
      capabilities: { skills: ['Research'], connectors: [] },
    });
    expect(fetch.mock.calls.some((c) => c[1]?.method === 'PUT')).toBe(false);
  });
  it('changes retry identity when a connector selection changes', async () => {
    const fetch = vi.fn().mockRejectedValue(new Error('offline'));
    vi.stubGlobal('fetch', fetch);
    const m = await mount();
    await m.edit('Find papers');
    await m.click('Send');
    await m.click('Add');
    await m.click('Connectors');
    await m.click('openalex');
    await m.click('Send');
    const posts = fetch.mock.calls.filter((c) => c[1]?.method === 'POST');
    const first = JSON.parse(posts[0]![1].body),
      second = JSON.parse(posts[1]![1].body);
    expect(first.request_id).not.toBe(second.request_id);
    expect(second.capabilities.connectors).toEqual(['openalex']);
    await m.click('Remove openalex');
    await m.click('Send');
    const last = JSON.parse(fetch.mock.calls.filter((c) => c[1]?.method === 'POST')[2]![1].body);
    expect(last.capabilities).toBeUndefined();
  });
  it('includes formerly disabled skills in slash suggestions', async () => {
    const m = await mount();
    m.client.setQueryData(keys.skills('lab'), [
      { id: 'yes', name: 'Research', policy_enabled: true },
      { id: 'no', name: 'Disabled', policy_enabled: false },
    ]);
    await m.edit('/');
    expect(textOf(m.root)).toContain('Disabled');
    const option = all(m.root).find(
      (n) => n.props.role === 'option' && textOf(n).trim() === 'Research',
    )!;
    (option.props.onMousedown as (e: unknown) => void)({ preventDefault() {} });
    await m.settle();
    expect(all(m.root).find((n) => n.tag === 'textarea')!.props.value).toBe('');
    expect(m.label('Remove Research')).toBeDefined();
    await m.edit('Analyze this');
    expect(m.label('Remove Research')).toBeDefined();
  });
  it('preserves surrounding text when selecting a slash suggestion', async () => {
    const m = await mount();
    (all(m.root).find((n) => n.tag === 'textarea')!.props.onInput as (e: unknown) => void)({
      target: { value: 'Before /Res after', selectionStart: 11 },
    });
    await m.settle();
    const option = all(m.root).find(
      (n) => n.props.role === 'option' && textOf(n).trim() === 'Research',
    )!;
    expect(option).toBeDefined();
    (option.props.onMousedown as (e: unknown) => void)({ preventDefault() {} });
    await m.settle();
    expect(all(m.root).find((n) => n.tag === 'textarea')!.props.value).toBe('Before  after');
    expect(m.label('Remove Research')).toBeDefined();
  });
  it('removes only the skill chip, preserving message text', async () => {
    const m = await mount();
    await m.click('Add');
    await m.click('Skills');
    await m.click('Research');
    await m.edit('Please discuss /Research later');
    await m.click('Remove Research');
    expect(all(m.root).find((n) => n.tag === 'textarea')!.props.value).toBe(
      'Please discuss /Research later',
    );
  });
  it('selects a connector without changing workspace policy', async () => {
    const fetch = vi.fn().mockRejectedValue(new Error('offline'));
    vi.stubGlobal('fetch', fetch);
    const m = await mount({ role: 'member' });
    await m.click('Add');
    await m.click('Connectors');
    await m.click('openalex');
    await m.edit('Find papers');
    await m.click('Send');
    const post = fetch.mock.calls.find((c) => c[1]?.method === 'POST')!;
    expect(JSON.parse(post[1].body).capabilities).toEqual({ skills: [], connectors: ['openalex'] });
    expect(fetch.mock.calls.some((c) => c[1]?.method === 'PUT')).toBe(false);
  });
  it('selects a workspace connector for only the submitted message', async () => {
    const fetch = vi.fn().mockRejectedValue(new Error('offline'));
    vi.stubGlobal('fetch', fetch);
    const m = await mount();
    const id = '1439a232-45e6-4abe-ae67-6b9152fb9c60';
    m.client.setQueryData(keys.workspaceMCP('lab'), [{ id, name: 'Lab MCP', transport: 'url' }]);
    await m.click('Add');
    await m.click('Connectors');
    await m.click('Lab MCP');
    expect(m.label('Remove Lab MCP')).toBeDefined();
    await m.edit('Search this');
    await m.click('Send');
    const post = fetch.mock.calls.find((c) => c[1]?.method === 'POST')!;
    expect(JSON.parse(post[1].body).capabilities.workspace_connectors).toEqual([id]);
  });
  it('clears a selected connector when switching conversations', async () => {
    vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} });
    vi.stubGlobal('document', { addEventListener() {}, removeEventListener() {} });
    const client = new QueryClient();
    const capabilities = ref({ skills: [] as string[], connectors: ['openalex'] });
    const app = createTreeApp({
      render: () =>
        h(AristotleComposer, {
          modelValue: 'Question',
          busy: false,
          initialCapabilities: capabilities.value,
        }),
    });
    app.use(VueQueryPlugin, { queryClient: client });
    const root = node('root');
    app.mount(root);
    cleanups.push(() => {
      app.unmount();
      client.clear();
    });
    expect(all(root).some((n) => n.props['aria-label'] === 'Remove openalex')).toBe(true);
    capabilities.value = { skills: [], connectors: [] };
    await nextTick();
    expect(all(root).some((n) => n.props['aria-label'] === 'Remove openalex')).toBe(false);
  });
  it('keeps the manage links available', async () => {
    const m = await mount();
    await m.click('Add');
    await m.click('Connectors');
    await m.click('Manage connectors');
    expect(m.router.currentRoute.value.fullPath).toBe('/settings?tab=connectors');
  });
  it('retains the exact request id for unchanged retries', async () => {
    const fetch = vi.fn().mockRejectedValue(new Error('offline'));
    vi.stubGlobal('fetch', fetch);
    const m = await mount();
    await m.edit('Question');
    await m.click('Send');
    await m.click('Send');
    const body = (index: number) => JSON.parse(fetch.mock.calls[index]![1].body);
    expect(body(0).request_id).toBe(body(1).request_id);
    await m.click('Send');
    expect(body(2).request_id).toBe(body(1).request_id);
  });
  it('keeps a failed acceptance request id through the Manage Settings roundtrip', async () => {
    const fetch = vi.fn().mockRejectedValue(new Error('offline'));
    vi.stubGlobal('fetch', fetch);
    const m = await mount();
    await m.edit('Retain this request');
    await m.click('Add');
    await m.click('Connectors');
    await m.click('openalex');
    await m.click('Send');
    const first = JSON.parse(fetch.mock.calls[0]![1].body);
    await m.click('Add');
    await m.click('Skills');
    await m.click('Manage skills');
    expect(m.router.currentRoute.value.fullPath).toBe('/settings?tab=skills');
    m.client.setQueryData(keys.runtimes('lab'), [
      { owner_id: 'me', harness: 'claude', status: 'online', models: [...models].reverse() },
    ]);
    m.router.back();
    await m.settle();
    await m.settle();
    expect(all(m.root).find((n) => n.tag === 'textarea')!.props.value).toBe('Retain this request');
    expect(m.label('Remove openalex')).toBeDefined();
    await m.click('Send');
    expect(JSON.parse(fetch.mock.calls[1]![1].body)).toEqual(first);
  });
  it('uploads files before accepting a turn, blocks while uploading and retains a refused attachment', async () => {
    let finishUpload!: (response: Response) => void;
    const fetch = vi.fn((url, _init) =>
      String(url).endsWith('/uploads')
        ? new Promise<Response>((resolve) => {
            finishUpload = resolve;
          })
        : Promise.resolve(response({ error: 'figures/notes.tex already exists' }, 409)),
    );
    vi.stubGlobal('fetch', fetch);
    const m = await mount();
    await m.edit('Use this file');
    const file = new File(['paper text'], 'notes.tex', { type: 'text/plain' });
    (m.label('Files').props.onChange as (e: unknown) => void)({
      target: { files: [file], value: 'notes.tex' },
    });
    await m.settle();
    expect(fetch.mock.calls).toHaveLength(1);
    expect(fetch.mock.calls[0]![0]).toBe('/api/coresearch/uploads');
    expect((fetch.mock.calls[0]![1].body as FormData).get('file')).toBeInstanceOf(File);
    expect(m.label('Send').props.disabled).toBe(true);
    await m.click('Send');
    expect(fetch.mock.calls).toHaveLength(1);
    finishUpload(response({ upload_id: 'upload-1' }, 201));
    await m.settle();
    await m.settle();
    expect(m.label('Send').props.disabled).toBe(false);
    await m.click('Send');
    expect(JSON.parse(fetch.mock.calls[1]![1].body).attachments).toEqual([
      { upload_id: 'upload-1', name: 'notes.tex' },
    ]);
    expect(textOf(m.root)).toContain('figures/notes.tex already exists');
    expect(m.label('Remove notes.tex')).toBeDefined();
    expect(m.router.currentRoute.value.path).toBe('/coresearch/new');
  });
  it('names a refused upload and keeps uploading the other selected files', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(response({ error: 'Text files can be at most 1 MiB' }, 413))
      .mockResolvedValueOnce(response({ upload_id: 'small-upload' }, 201));
    vi.stubGlobal('fetch', fetch);
    const m = await mount();
    (m.label('Files').props.onChange as (e: unknown) => void)({
      target: {
        files: [new File(['large'], 'large.tex'), new File(['small'], 'small.tex')],
        value: '',
      },
    });
    await m.settle();
    await m.settle();
    expect(textOf(m.root)).toContain('large.tex — Text files can be at most 1 MiB');
    expect(m.label('Remove small.tex')).toBeDefined();
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});
