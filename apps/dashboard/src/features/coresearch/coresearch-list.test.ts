import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createMemoryHistory, createRouter } from 'vue-router';
import { readFileSync } from 'node:fs';
import CoresearchList from './CoresearchList.vue';
import { ownOnlineClis } from './api';
import { invalidationsFor } from '../../api/realtime';
import type { LykEvent } from '../../api/types';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
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
async function mount(projects = [project('Older'), project('Newest', 'member', '2026-02-01')]) {
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
  for (const p of projects) client.setQueryData(keys.coresearchChats('lab', p.id), []);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push('/coresearch');
  const app = createTreeApp({ render: () => h(CoresearchList) });
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
    expect(textOf((await mount([])).root)).toContain(
      'Nothing yet — projects you start from New land here.',
    );
  });
  it('sidebar keeps Coresearch in the top toggle, out of the Laboratory group', () => {
    const app = readFileSync('apps/dashboard/src/App.vue', 'utf8');
    const laboratory = app.slice(
      app.indexOf("name: 'Laboratory'"),
      app.indexOf("name: 'IT Department'"),
    );
    expect(laboratory).toMatch(/id: 'studies'[\s\S]*id: 'projects'[\s\S]*id: 'colleagues'/);
    expect(laboratory).not.toContain("id: 'coresearch'");
    const brand = readFileSync('apps/dashboard/src/ui/SidebarBrand.vue', 'utf8');
    expect(brand).toMatch(/id: 'coresearch'[\s\S]*id: 'decisions'/);
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
        keys.coresearchConversations('lab'),
        keys.researchCoresearchAll('lab'),
        ['ws', 'lab', 'coresearch', 'p'],
      ]);
      expect(invalidationsFor('other', event)).toEqual([]);
    }
  });
  it('withholds providers before identity resolves', () => {
    expect(ownOnlineClis([], undefined)).toEqual([]);
  });
});
