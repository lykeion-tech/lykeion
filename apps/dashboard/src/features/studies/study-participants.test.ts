import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import StudyParticipants from './StudyParticipants.vue';
import { workspaceId } from '../../api/session';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';

vi.mock('../coresearch/Aristotle.vue', () => ({ default: { render: () => null } }));

const cleanups: (() => void)[] = [];
afterEach(() => {
  while (cleanups.length) cleanups.pop()!();
  vi.unstubAllGlobals();
});

async function open(runtime: Record<string, unknown> | null, aristotle = false) {
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
  client.setQueryData(['ws', 'lab', 'agent-capabilities', 'a1'], {
    skills: [],
    teams: [],
    team_member: false,
  });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push('/studies/s1');
  const app = createTreeApp({
    render: () =>
      h(StudyParticipants, {
        runs: [
          {
            agent_id: 'a1',
            agent_name: 'searcher · Grounding Team',
            role: 'searcher',
            runtime_id: 'r1',
            harness: 'claude',
            model: '',
            board_column: 'grounding',
          },
        ] as never,
        agents: [
          {
            id: 'a1',
            name: aristotle ? 'Aristotle' : 'searcher · Grounding Team',
            owner_id: 'u1',
            avatar_url: '',
            built_in_key: aristotle ? 'aristotle' : '',
          },
        ] as never,
        owner: aristotle
          ? ({ user_id: 'u1', name: 'Owner', email: 'owner@example.com', role: 'owner' } as never)
          : undefined,
        runtimes: (runtime
          ? [{ id: 'r1', device_name: 'Mac', harness: 'claude', ...runtime }]
          : []) as never,
        teams: {},
      }),
  });
  app.use(VueQueryPlugin, { queryClient: client });
  app.use(router);
  const root = node('root');
  app.mount(root);
  cleanups.push(() => {
    app.unmount();
    client.clear();
  });
  await nextTick();
  const icon = all(root).find((n) => /^Show .* details$/.test(String(n.props['aria-label'])))!;
  (icon.props.onClick as () => void)();
  await nextTick();
  return root;
}

describe('a study participant card', () => {
  it('shows Aristotle’s colored avatar in the owner’s agent list', async () => {
    const root = await open(null, true);
    const avatars = all(root).filter((entry) =>
      String(entry.props.class ?? '').includes('is-aristotle'),
    );
    expect(avatars.length).toBeGreaterThan(0);
    expect(textOf(root)).toContain('Aristotle');
  });
  const dot = (root: ReturnType<typeof node>) =>
    all(root).find((n) => String(n.props.class ?? '').includes('status-dot'))!;
  it('says Active, with a green dot, when its machine is up and healthy', async () => {
    const root = await open({ status: 'online', unhealthy_reason: null });
    expect(textOf(root)).toContain('Active');
    expect(textOf(root)).not.toContain('Inactive');
    expect(String(dot(root).props.class)).not.toContain('inactive');
  });
  it.each([
    ['offline', { status: 'offline', unhealthy_reason: null }],
    ['unhealthy', { status: 'online', unhealthy_reason: 'sign_in' }],
    ['not known', null],
  ])('says Inactive, with a red dot, when its machine is %s', async (_name, runtime) => {
    const root = await open(runtime);
    expect(textOf(root)).toContain('Inactive');
    expect(String(dot(root).props.class)).toContain('inactive');
  });
});
