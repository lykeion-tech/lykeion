import { afterEach, expect, it, vi } from 'vitest';
import { nextTick, ref } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import AgentCustomize from './AgentCustomize.vue';
import { workspaceId } from '../../api/session';

const request = vi.hoisted(() => vi.fn());
vi.mock('../../api/client', () => ({ api: request }));
vi.mock('../../api/queries', () => ({
  useMe: () => ({ data: ref({ id: 'me' }) }),
  useSkillLibrary: () => ({
    data: ref([{ id: 'aeon', name: 'aeon' }]),
    isPending: ref(false),
    isError: ref(false),
  }),
  useConnectorLibrary: () => ({
    data: ref([{ name: 'ensembl' }]),
    isPending: ref(false),
    isError: ref(false),
  }),
  useWorkspaceMCPServers: () => ({ data: ref([]), isPending: ref(false), isError: ref(false) }),
}));
afterEach(() => {
  vi.clearAllMocks();
});

it('saves standalone selections and keeps shipped Team capabilities read-only', async () => {
  workspaceId.value = 'lab';
  let data: unknown = { skills: ['aeon'], connectors: [], teams: [] };
  request.mockImplementation(async (method: string) => (method === 'GET' ? data : {}));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = node('root');
  const app = createTreeApp(AgentCustomize, { id: 'agent', editable: true });
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(root);
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
  expect(textOf(root)).toContain('aeon');
  expect(all(root).some((n) => n.tag === 'button' && textOf(n).includes('Add skill'))).toBe(true);
  expect(all(root).some((n) => n.tag === 'button' && textOf(n).includes('Add connectors'))).toBe(
    true,
  );
  const addSkill = all(root).find((n) => n.tag === 'button' && textOf(n).includes('Add skill'))!;
  (addSkill.props.onClick as () => void)();
  await nextTick();
  const save = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Save skills')!;
  await (save.props.onClick as () => Promise<void>)();
  expect(request).toHaveBeenCalledWith(
    'PUT',
    '/api/agents/agent/customize',
    { skills: ['aeon'], connectors: [] },
    undefined,
    { workspace: 'lab' },
  );
  data = {
    skills: [],
    connectors: [],
    teams: [
      {
        id: 'team',
        member: 'leader',
        source: 'shipped',
        uploaded_by: null,
        team_name: 'Team',
        skills: ['aeon'],
        connectors: [],
      },
    ],
  };
  await client.refetchQueries({ queryKey: ['ws', 'lab', 'agent-capabilities', 'agent'] });
  await nextTick();
  expect(textOf(root)).toContain("Built-in Teams can't be changed");
  expect(all(root).some((n) => n.tag === 'button' && textOf(n).trim() === 'Save skills')).toBe(
    false,
  );
  data = {
    skills: [],
    connectors: [],
    team_member: true,
    teams: [
      {
        id: 'team',
        member: 'leader',
        source: 'uploaded',
        uploaded_by: 'me',
        team_name: 'Team',
        skills: [],
        connectors: [],
      },
    ],
  };
  await client.refetchQueries({ queryKey: ['ws', 'lab', 'agent-capabilities', 'agent'] });
  await nextTick();
  expect(textOf(root)).toContain('No skills assigned');
  expect(textOf(root)).toContain('No connectors assigned');
  const addConnectors = all(root).find(
    (n) => n.tag === 'button' && textOf(n).includes('Add connectors'),
  )!;
  (addConnectors.props.onClick as () => void)();
  await nextTick();
  const connector = all(root).find(
    (n) => n.tag === 'select' && n.props['aria-label'] === 'Add connector',
  )!;
  (connector.props.onChange as (event: unknown) => void)({ target: { value: 'ensembl' } });
  await nextTick();
  const saveConnectors = all(root).find(
    (n) => n.tag === 'button' && textOf(n).trim() === 'Save connectors',
  )!;
  await (saveConnectors.props.onClick as () => Promise<void>)();
  expect(request).toHaveBeenCalledWith(
    'POST',
    '/api/teams/team/connectors',
    { member: 'leader', connectors: [{ name: 'ensembl', catalogue: 'ensembl' }] },
    undefined,
    { workspace: 'lab' },
  );
  app.unmount();
  client.clear();
});
