import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick, reactive } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createMemoryHistory, createRouter } from 'vue-router';
import StudyBoard from './StudyBoard.vue';
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
  const app = createTreeApp({ render: () => h(StudyBoard, props) });
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
  return { root, client, props, router, button, click, settle, storage };
}
describe('study display controls', () => {
  it('shows customized properties and restores the defaults without changing studies', async () => {
    const t = await mount();
    const cardsBefore = JSON.stringify(t.client.getQueryData(keys.cards('lab')));
    await t.click('Display');
    await t.click('Question');
    expect(t.button('Display').props.title).toContain('1 hidden');
    await t.click('Reset properties');
    expect(t.button('Question').props['aria-checked']).toBe(true);
    expect(t.button('Display').props.title).toBe('Display');
    expect(textOf(t.root)).toContain('Own study');
    expect(textOf(t.root)).toContain('Other study');
    expect(JSON.stringify(t.client.getQueryData(keys.cards('lab')))).toBe(cardsBefore);
    expect(JSON.parse(t.storage.get('lykeion.study-display')!).properties).toContain('Question');
  });
  it('keeps stage order on an empty board and on workspace switch', async () => {
    const t = await mount();
    t.client.setQueryData(keys.cards('lab'), []);
    await t.settle();
    const empty = textOf(t.root);
    expect(empty.indexOf('Grounding')).toBeLessThan(empty.indexOf('Hypotheses'));
    expect(empty.indexOf('Hypotheses')).toBeLessThan(empty.indexOf('Experimentation'));
    expect(empty.indexOf('Experimentation')).toBeLessThan(empty.indexOf('Validation'));
    t.client.setQueryData(keys.cards('next'), [card('Next study', 'me')]);
    t.client.setQueryData(keys.workspace('next'), { slug: 'next' });
    t.client.setQueryData(keys.members('next'), []);
    workspaceId.value = 'next';
    await t.settle();
    expect(textOf(t.root)).toContain('Next study');
    expect(textOf(t.root)).not.toContain('Own study');
  });
  it('shows active filters and clears a filtered-empty board', async () => {
    const t = await mount();
    await t.click('Filter');
    await t.click('State');
    await t.click('Blocked');
    expect(textOf(t.root)).toContain('No studies match these filters');
    expect(textOf(t.root)).toContain('State: Blocked');
    await t.click('Clear');
    expect(textOf(t.root)).toContain('Own study');
  });
});
