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
  it('shows running CLI icons without making the indicator clickable', async () => {
    const t = await mount();
    t.client.setQueryData(keys.cards('lab'), [
      card('Active study', 'me', {
        agent_running: true,
        teams: { grounding: { pack_id: 'team', name: 'Grounding', stage: 'grounding' } },
        running_clis: [
          { board_column: 'grounding', harness: 'claude' },
          { board_column: 'grounding', harness: 'codex' },
        ],
      }),
      card('Solo study', 'me', {
        agent_running: true,
        teams: {},
        running_clis: [{ board_column: 'grounding', harness: 'codex' }],
      }),
      card('Another team study', 'me', {
        agent_running: true,
        teams: { grounding: { pack_id: 'team', name: 'Grounding', stage: 'grounding' } },
        running_clis: [{ board_column: 'grounding', harness: 'claude' }],
      }),
      card('Other study', 'other'),
    ]);
    await t.settle();
    const indicator = all(t.root).find(
      (n) => n.props.role === 'status' && n.props['aria-label'] === '2 agents working',
    )!;
    expect(indicator.tag).toBe('span');
    expect(indicator.props.onClick).toBeUndefined();
    expect(all(t.root).find((n) => n.props['data-cli'] === 'claude')).toBeTruthy();
    expect(all(t.root).find((n) => n.props['data-cli'] === 'codex')).toBeTruthy();
    expect(all(t.root).filter((n) => n.props['data-cli']).length).toBe(3);
    expect(all(t.root).filter((n) => n.props.class === 'working-icon').length).toBe(2);
    expect(indicator.props['aria-description']).toContain('Grounding: Claude Code, Codex');
    expect(textOf(t.root)).toContain('Active study');
    expect(textOf(t.root)).toContain('Solo study');
    expect(textOf(t.root)).toContain('Another team study');
    expect(textOf(t.root)).toContain('Other study');
  });
  it('keeps waiting studies visible separately from working groups', async () => {
    const t = await mount();
    t.client.setQueryData(keys.cards('lab'), [
      card('Running', 'me', { running_clis: [{ board_column: 'grounding', harness: 'claude' }] }),
      card('Waiting', 'me', { queued: true, waiting: 'Waiting for a machine' }),
    ]);
    await t.settle();
    expect(
      all(t.root).find((n) => n.props['aria-label'] === '1 agents working, 1 waiting'),
    ).toBeTruthy();
  });
  it('limits the icon stack and reports the remaining groups', async () => {
    const t = await mount();
    t.client.setQueryData(
      keys.cards('lab'),
      [1, 2, 3, 4].map((n) =>
        card(`Study ${n}`, 'me', {
          agent_running: true,
          running_clis: [{ board_column: 'grounding', harness: 'claude' }],
        }),
      ),
    );
    await t.settle();
    const indicator = all(t.root).find((n) => n.props['aria-label'] === '4 agents working')!;
    expect(indicator).toBeTruthy();
    expect(all(t.root).filter((n) => n.props['data-cli']).length).toBe(3);
    expect(textOf(indicator)).toContain('+1');
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
