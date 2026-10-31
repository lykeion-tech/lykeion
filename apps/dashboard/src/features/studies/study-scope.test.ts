import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick, reactive } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createMemoryHistory, createRouter } from 'vue-router';
import TaskBoard from './TaskBoard.vue';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';

const cleanups: (() => void)[] = [];
afterEach(() => {
  cleanups.splice(0).forEach((fn) => fn());
  vi.unstubAllGlobals();
});
const card = (id: string, owner: string, extra = {}) => ({
  id,
  owner_id: owner,
  title: id,
  number: 1,
  workspace_id: 'lab',
  board_column: 'grounding',
  state: 'working',
  flag: '',
  ending: '',
  role: 'grounding',
  queued: false,
  research_id: 'project',
  assignee_type: 'team',
  updated_at: new Date().toISOString(),
  ...extra,
});
async function mount(scope?: 'mine' | 'workspace', projectId?: string, user: string | null = 'me') {
  vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal('KeyboardEvent', class KeyboardEvent {});
  vi.stubGlobal('MouseEvent', class MouseEvent {});
  vi.stubGlobal('document', { addEventListener() {}, removeEventListener() {} });
  const storage = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
  });
  workspaceId.value = 'lab';
  const client = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        staleTime: Infinity,
        gcTime: Infinity,
        enabled: false,
      },
    },
  });
  client.setQueryData(keys.me, user ? { id: user } : null);
  client.setQueryData(keys.cards('lab'), [card('Own study', 'me'), card('Other study', 'other')]);
  client.setQueryData(keys.members('lab'), []);
  client.setQueryData(keys.workspace('lab'), { slug: 'lab' });
  const props = reactive({ scope, projectId });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push(scope === 'mine' ? '/my-studies' : '/studies');
  const app = createTreeApp({ render: () => h(TaskBoard, props) });
  app.use(VueQueryPlugin, { queryClient: client });
  app.use(router);
  const root = node('root');
  app.mount(root);
  cleanups.push(() => {
    app.unmount();
    client.clear();
  });
  const settle = async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
  };
  await settle();
  const button = (text: string) =>
    all(root).find(
      (n) => n.tag === 'button' && (n.props['aria-label'] === text || textOf(n).trim() === text),
    )!;
  const click = async (text: string) => {
    const b = button(text);
    (b.props.onClick as (event: unknown) => void)({ currentTarget: b });
    await settle();
  };
  return { root, client, props, router, button, click, settle };
}
describe('study page scope', () => {
  it('defaults to all workspace studies without All/Mine controls', async () => {
    const t = await mount();
    expect(textOf(t.root)).toContain('Own study');
    expect(textOf(t.root)).toContain('Other study');
    expect(t.button('All')).toBeUndefined();
    expect(t.button('Mine')).toBeUndefined();
    expect(t.button('2 agents working')).toBeTruthy();
  });
  it('scopes board, list, agent count and study links to the current owner', async () => {
    const t = await mount('mine');
    expect(textOf(t.root)).toContain('Own study');
    expect(textOf(t.root)).not.toContain('Other study');
    expect(t.button('1 agents working')).toBeTruthy();
    const own = all(t.root).find((n) => n.props['data-card'] === 'Own study')!;
    (own.props.onClick as () => void)();
    await t.settle();
    expect(t.router.currentRoute.value.path).toBe('/my-studies/Own study');
    await t.click('Board');
    expect(
      all(t.root)
        .filter((n) => n.props.class === 'list-row')
        .map(textOf)
        .join(),
    ).toContain('Own study');
    expect(textOf(t.root)).not.toContain('Other study');
  });
  it('updates ownership on account changes and never widens scope while user is unknown', async () => {
    const t = await mount('mine', undefined, null);
    expect(textOf(t.root)).not.toContain('Own study');
    expect(textOf(t.root)).not.toContain('Other study');
    t.client.setQueryData(keys.me, { id: 'other' });
    await t.settle();
    expect(textOf(t.root)).toContain('Other study');
    expect(textOf(t.root)).not.toContain('Own study');
    t.client.setQueryData(keys.cards('next'), [card('Next lab study', 'other')]);
    t.client.setQueryData(keys.members('next'), []);
    t.client.setQueryData(keys.workspace('next'), { slug: 'next' });
    workspaceId.value = 'next';
    await t.settle();
    expect(textOf(t.root)).toContain('Next lab study');
    expect(textOf(t.root)).not.toContain('Other study');
  });
  it('intersects toolbar filters with ownership and reacts when switching page scope', async () => {
    const t = await mount('mine');
    await t.click('Filter');
    await t.click('State');
    await t.click('Blocked');
    expect(textOf(t.root)).not.toContain('Own study');
    expect(textOf(t.root)).not.toContain('Other study');
    await t.click('Clear filters');
    expect(textOf(t.root)).toContain('Own study');
    expect(textOf(t.root)).not.toContain('Other study');
    t.props.scope = 'workspace';
    await t.settle();
    expect(textOf(t.root)).toContain('Other study');
    expect(t.button('2 agents working')).toBeTruthy();
  });
  it('preserves project All/Members/Agents tabs and their filters', async () => {
    const t = await mount(undefined, 'project');
    expect(t.button('All')).toBeTruthy();
    expect(t.button('Members')).toBeTruthy();
    expect(t.button('Agents')).toBeTruthy();
    await t.click('Members');
    expect(textOf(t.root)).not.toContain('Own study');
    t.props.projectId = undefined;
    await t.settle();
    expect(textOf(t.root)).toContain('Own study');
    t.props.projectId = 'project';
    await t.settle();
    await t.click('Agents');
    expect(textOf(t.root)).toContain('Own study');
    expect(textOf(t.root)).toContain('Other study');
  });
});
