import { h, nextTick, ref } from 'vue';
import { afterEach, expect, it, vi } from 'vitest';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import ResearchesView from './ResearchesView.vue';

vi.mock('vue', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue')>();
  return {
    ...actual,
    Teleport: {
      setup(_: unknown, { slots }: any) {
        return () => actual.h('div', slots.default?.());
      },
    },
  };
});

const api = vi.fn();
const invalidateQueries = vi.fn().mockResolvedValue(undefined);
const rows = ref<any[]>([
  {
    id: 'p1',
    name: 'Project Alpha',
    description: '',
    status: 'planned',
    priority: 'none',
    lead_type: null,
    lead_id: null,
    created_at: '2026-11-04T00:00:00Z',
  },
]);
const role = ref('owner');
const query = (data: unknown) => ({
  data: ref(data),
  error: ref(null),
  isPending: ref(false),
  isSuccess: ref(true),
  refetch: vi.fn(),
});
vi.mock('../../api/client', () => ({ api: (...args: unknown[]) => api(...args) }));
vi.mock('@tanstack/vue-query', () => ({ useQueryClient: () => ({ invalidateQueries }) }));
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }));
vi.mock('../../api/session', async () => ({ workspaceId: (await import('vue')).ref('w1') }));
vi.mock('../../api/queries', () => ({
  useAgents: () => query([]),
  useCards: () => query([]),
  useMe: () => query({ id: 'u1' }),
  useMembers: () => query([]),
  useWorkspace: () => query({ name: 'Lab' }),
  useWorkspaces: () => ({ data: ref([{ id: 'w1', role: role.value }]) }),
  useResearch: () => query(undefined),
  useResearches: () => ({ ...query([]), data: rows }),
}));
vi.mock('../../ui/Modal.vue', () => ({
  default: {
    props: ['title'],
    setup(_: unknown, { slots }: any) {
      return () => h('section', { role: 'dialog' }, slots.default?.());
    },
  },
}));
vi.mock('./ProjectDetail.vue', () => ({ default: { render: () => null } }));
let app: ReturnType<typeof createTreeApp> | undefined;
const tick = async () => {
  await nextTick();
  await Promise.resolve();
  await nextTick();
};
const find = (root: ReturnType<typeof node>, text: string) =>
  all(root).find((n) => n.tag === 'button' && textOf(n).trim() === text);
afterEach(() => {
  app?.unmount();
  app = undefined;
  api.mockReset();
  invalidateQueries.mockClear();
  role.value = 'owner';
});
function mount() {
  vi.stubGlobal('document', {
    addEventListener() {},
    removeEventListener() {},
    querySelector() {
      return null;
    },
  });
  const root = node('root');
  app = createTreeApp(ResearchesView);
  app.component('RouterLink', {
    props: ['to'],
    setup(_: unknown, { slots }: any) {
      return () => h('a', slots.default?.());
    },
  });
  app.mount(root);
  return root;
}

it('confirms deletion before sending the request and refreshes projects and studies', async () => {
  const root = mount();
  const bin = all(root).find((n) => n.props['aria-label'] === 'Delete project Project Alpha');
  expect(bin).toBeDefined();
  (bin!.props.onClick as () => void)();
  await tick();
  expect(textOf(root)).toContain('Delete Project Alpha?');
  expect(api).not.toHaveBeenCalled();
  api.mockResolvedValue(undefined);
  (find(root, 'Delete project')!.props.onClick as () => void)();
  await tick();
  expect(api).toHaveBeenCalledWith('DELETE', '/api/researches/p1', undefined, undefined, {
    workspace: 'w1',
  });
  expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['ws', 'w1', 'researches'] });
  expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['ws', 'w1', 'cards'] });
});

it('does not expose deletion to an ordinary member', () => {
  role.value = 'member';
  const root = mount();
  expect(all(root).some((n) => n.props['aria-label'] === 'Delete project Project Alpha')).toBe(
    false,
  );
});

it('retains the dialog and shows an error when deletion fails', async () => {
  const root = mount();
  (
    all(root).find((n) => n.props['aria-label'] === 'Delete project Project Alpha')!.props
      .onClick as () => void
  )();
  await tick();
  api.mockRejectedValue(new Error('Could not delete'));
  (find(root, 'Delete project')!.props.onClick as () => void)();
  await tick();
  expect(textOf(root)).toContain('Could not delete');
  expect(invalidateQueries).not.toHaveBeenCalled();
});
