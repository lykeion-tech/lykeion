import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createMemoryHistory, createRouter } from 'vue-router';
import { readFileSync } from 'node:fs';
import CoresearchList from '../apps/dashboard/src/views/CoresearchList.vue';
import CoresearchStarter from '../apps/dashboard/src/coresearch/CoresearchStarter.vue';
import { ownOnlineClis, titleFromPrompt } from '../apps/dashboard/src/coresearch/api';
import { invalidationsFor } from '../apps/dashboard/src/api/realtime';
import type { LykEvent } from '../apps/dashboard/src/api/types';
import { keys } from '../apps/dashboard/src/api/keys';
import { workspaceId } from '../apps/dashboard/src/api/session';
import { all, createTreeApp, node, textOf } from './daemon-tree';
vi.hoisted(() => {
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { getItem: () => null, setItem() {}, removeItem() {} },
  });
});
const cleanups: (() => void)[] = [];
afterEach(() => {
  cleanups.splice(0).forEach((fn) => fn());
  vi.unstubAllGlobals();
});
const project = (id: string, role = 'owner', updated_at = '2026-01-01') => ({
  id,
  name: id,
  role,
  created_by: 'me',
  updated_at,
  archived_at: null,
});
async function mount(
  starter = false,
  projects = [project('Older'), project('Newest', 'member', '2026-02-01')],
  runtimes: unknown[] = [],
) {
  vi.stubGlobal('document', { activeElement: null });
  vi.stubGlobal('Document', class Document {});
  vi.stubGlobal('ShadowRoot', class ShadowRoot {});
  workspaceId.value = 'lab';
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity, gcTime: Infinity, enabled: false },
    },
  });
  client.setQueryData(keys.me, { id: 'me', name: 'Ada' });
  client.setQueryData(keys.members('lab'), [{ user_id: 'me', name: 'Ada' }]);
  client.setQueryData(keys.coresearchProjects('lab'), projects);
  client.setQueryData(keys.runtimes('lab'), runtimes);
  for (const p of projects) client.setQueryData(keys.coresearchChats('lab', p.id), []);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push('/coresearch');
  const app = createTreeApp({ render: () => h(starter ? CoresearchStarter : CoresearchList) });
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
describe('Coresearch foundation', () => {
  it('rows come newest first', async () => {
    const { root } = await mount();
    expect(
      all(root)
        .filter((n) => n.props['data-project'])
        .map((n) => n.props['data-project']),
    ).toEqual(['Newest', 'Older']);
  });
  it('archive hidden for non-owners, even the creator', async () => {
    const { root } = await mount();
    expect(all(root).some((n) => n.props['aria-label'] === 'Archive Older')).toBe(true);
    expect(all(root).some((n) => n.props['aria-label'] === 'Archive Newest')).toBe(false);
  });
  it('keeps the source Filter unavailable with an honest explanation', async () => {
    const { root } = await mount();
    const filter = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Filter')!;
    expect(filter.props.disabled).not.toBeUndefined();
    expect(filter.props.title).toBe('Coresearch currently supports Experiment only');
    expect(textOf(root)).toContain('Coresearch currently supports Experiment only');
  });
  it('empty state text', async () => {
    expect(textOf((await mount(false, [])).root)).toContain(
      'Nothing yet — projects you start from New land here.',
    );
  });
  it('starter disables send without own online runtime', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const { root, settle } = await mount(
      true,
      [],
      [
        { owner_id: 'other', status: 'online', harness: 'claude' },
        { owner_id: 'me', status: 'offline', harness: 'codex' },
      ],
    );
    (
      all(root).find((n) => n.tag === 'textarea')!.props['onUpdate:modelValue'] as (
        s: string,
      ) => void
    )('A valid research question');
    await settle();
    const send = all(root).find((n) => n.props['aria-label'] === 'Send')!;
    expect(send.props.disabled).toBe(true);
    await (send.props.onClick as () => Promise<void>)();
    expect(fetch).not.toHaveBeenCalled();
    expect(textOf(root)).toContain('No machine of yours is connected to this lab.');
  });
  it("starter offers only the caller's own CLIs, deduplicated and supported", async () => {
    const { root } = await mount(
      true,
      [],
      [
        { owner_id: 'other', status: 'online', harness: 'codex' },
        { owner_id: 'me', status: 'online', harness: 'claude' },
        { owner_id: 'me', status: 'online', harness: 'claude' },
        { owner_id: 'me', status: 'online', harness: 'cursor' },
      ],
    );
    expect(
      all(root)
        .filter((n) => n.props['data-cli'])
        .map((n) => n.props['data-cli']),
    ).toEqual(['claude']);
  });
  it('sidebar lists Coresearch third under Laboratory', () => {
    const source = readFileSync('apps/dashboard/src/App.vue', 'utf8');
    expect(
      source.slice(source.indexOf("name: 'Laboratory'"), source.indexOf("name: 'IT Department'")),
    ).toMatch(/id: 'studies'[\s\S]*id: 'projects'[\s\S]*id: 'coresearch'[\s\S]*id: 'colleagues'/);
  });
});

describe('Coresearch API wiring', () => {
  it('takes the newest chat CLI and hides stale archived rows', async () => {
    const t = await mount();
    t.client.setQueryData(keys.coresearchChats('lab', 'Older'), [
      { cli: 'claude', created_at: '2025-01-01' },
      { cli: 'codex', created_at: '2026-01-01' },
    ]);
    t.client.setQueryData(keys.coresearchProjects('lab'), [
      project('Older'),
      { ...project('Archived'), archived_at: '2026-01-02' },
    ]);
    await t.settle();
    expect(textOf(t.root)).toContain('Codex');
    expect(textOf(t.root)).not.toContain('Claude Code');
    expect(textOf(t.root)).not.toContain('Archived');
  });
  it('sends project then CLI-only chat then body and navigates after acceptance', async () => {
    const calls: { method: string; path: string; body: unknown }[] = [];
    vi.stubGlobal('fetch', async (path: string, options: RequestInit) => {
      calls.push({ method: options.method!, path, body: JSON.parse(String(options.body)) });
      return new Response(JSON.stringify({ id: calls.length === 1 ? 'project' : 'chat' }), {
        status: 201,
      });
    });
    const t = await mount(true, [], [{ owner_id: 'me', status: 'online', harness: 'codex' }]);
    const input = all(t.root).find((n) => n.tag === 'textarea')!;
    (input.props['onUpdate:modelValue'] as (s: string) => void)('  Inspect orbital residuals  ');
    await t.settle();
    const send = all(t.root).find((n) => n.props['aria-label'] === 'Send')!;
    expect(send.props.disabled).toBe(false);
    await (send.props.onClick as () => Promise<void>)();
    expect(calls).toEqual([
      {
        method: 'POST',
        path: '/api/coresearch/projects',
        body: { name: 'Inspect orbital residuals' },
      },
      { method: 'POST', path: '/api/coresearch/projects/project/chats', body: { cli: 'codex' } },
      {
        method: 'POST',
        path: '/api/coresearch/projects/project/chats/chat/turns',
        body: { body: 'Inspect orbital residuals' },
      },
    ]);
    expect(t.router.currentRoute.value.path).toBe('/coresearch/project/chat');
  });
  it('captures the chosen provider before project creation awaits a response', async () => {
    let release!: () => void;
    const pending = new Promise<void>((resolve) => {
      release = resolve;
    });
    const chats: unknown[] = [];
    vi.stubGlobal('fetch', async (path: string, options: RequestInit) => {
      if (path === '/api/coresearch/projects') {
        await pending;
        return new Response(JSON.stringify({ id: 'project' }), { status: 201 });
      }
      if (path.endsWith('/chats')) chats.push(JSON.parse(String(options.body)));
      return new Response(JSON.stringify({ id: 'chat' }), { status: 201 });
    });
    const t = await mount(true, [], [{ owner_id: 'me', status: 'online', harness: 'claude' }]);
    (
      all(t.root).find((n) => n.tag === 'textarea')!.props['onUpdate:modelValue'] as (
        s: string,
      ) => void
    )('Keep the selected provider');
    await t.settle();
    const sent = (
      all(t.root).find((n) => n.props['aria-label'] === 'Send')!.props
        .onClick as () => Promise<void>
    )();
    t.client.setQueryData(keys.runtimes('lab'), [
      { owner_id: 'me', status: 'online', harness: 'codex' },
    ]);
    await t.settle();
    release();
    await sent;
    expect(chats).toEqual([{ cli: 'claude' }]);
  });
  it('preserves the draft and existing project/chat when a turn is refused', async () => {
    const calls: string[] = [];
    let fail = true;
    vi.stubGlobal('fetch', async (path: string) => {
      calls.push(path);
      if (path.endsWith('/turns') && fail)
        return new Response(JSON.stringify({ error: 'Try again' }), { status: 409 });
      return new Response(JSON.stringify({ id: path.endsWith('/projects') ? 'project' : 'chat' }), {
        status: 201,
      });
    });
    const t = await mount(true, [], [{ owner_id: 'me', status: 'online', harness: 'claude' }]);
    (
      all(t.root).find((n) => n.tag === 'textarea')!.props['onUpdate:modelValue'] as (
        s: string,
      ) => void
    )('Test retry');
    await t.settle();
    const send = () => all(t.root).find((n) => n.props['aria-label'] === 'Send')!;
    await (send().props.onClick as () => Promise<void>)();
    await t.settle();
    expect(textOf(t.root)).toContain('Try again');
    expect(t.router.currentRoute.value.path).toBe('/coresearch');
    fail = false;
    await (send().props.onClick as () => Promise<void>)();
    expect(calls.filter((p) => p.endsWith('/projects'))).toHaveLength(1);
    expect(calls.filter((p) => p.endsWith('/chats'))).toHaveLength(1);
    expect(calls.filter((p) => p.endsWith('/turns'))).toHaveLength(2);
    expect(t.router.currentRoute.value.path).toBe('/coresearch/project/chat');
  });
  it('refetches every project pane for ID-only events and ignores another workspace', () => {
    for (const type of [
      'project.updated',
      'member.updated',
      'chat.created',
      'chat.updated',
      'message.created',
      'cell.changed',
      'file.saved',
      'run.changed',
      'environment.changed',
    ]) {
      const event = {
        id: 1,
        card_id: null,
        actor_type: 'user',
        actor_id: 'me',
        created_at: '2026-01-01',
        workspace_id: 'lab',
        type: `coresearch.${type}`,
        payload: { project_id: 'p' },
      } as LykEvent;
      expect(invalidationsFor('lab', event)).toEqual([
        keys.coresearchProjects('lab'),
        ['ws', 'lab', 'coresearch', 'p'],
      ]);
      expect(invalidationsFor('other', event)).toEqual([]);
    }
  });
  it('uses the exact source title cut and withholds providers before identity resolves', () => {
    expect(titleFromPrompt('  ' + 'x'.repeat(79) + ' ' + 'tail  ')).toBe('x'.repeat(79));
    expect(ownOnlineClis([], undefined)).toEqual([]);
  });
});

it('loads the scoped full stylesheet without a later resetting token import', () => {
  for (const file of [
    'apps/dashboard/src/coresearch/CoresearchStarter.vue',
    'apps/dashboard/src/views/CoresearchList.vue',
  ]) {
    expect(readFileSync(file, 'utf8')).not.toContain('l2-tokens.css');
  }
});
