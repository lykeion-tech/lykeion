import { afterEach, expect, it, vi } from 'vitest';
import { nextTick, ref } from 'vue';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import SkillPanel from './settings/library/SkillPanel.vue';
import ConnectorPanel from './settings/library/ConnectorPanel.vue';
import { workspaceId } from '../../api/session';

vi.mock('../../api/library', () => {
  const result = (data: unknown[]) => ({
    data: ref(data),
    isPending: ref(false),
    error: ref(null),
    refetch: vi.fn(),
  });
  return {
    useSkillLibrary: () =>
      result([
        { id: 'a', name: 'astronomy', description: 'stars', domain: 'Physics', origin: 'bundled' },
        { id: 'b', name: 'genomics', description: 'genes', domain: 'Biology', origin: 'bundled' },
        { id: 'c', name: 'my skill', description: 'custom', domain: 'Other', origin: 'imported' },
        {
          id: 'd',
          name: 'Alpha skill',
          description: 'custom',
          domain: 'Other',
          origin: 'imported',
        },
      ]),
    useConnectorLibrary: () =>
      result([
        { name: 'telescope', purpose: 'stars', domain: 'Physics', policy_enabled: true },
        { name: 'genes', purpose: 'genetics', domain: 'Biology', policy_enabled: true },
      ]),
    useWorkspaceMCPServers: () => result([{ id: 'c', name: 'my connector', transport: 'url' }]),
    useOpenAlexConnector: () => ({
      data: ref({ key_set: false, revision: 0, policy_enabled: true }),
      isError: ref(false),
    }),
  };
});
vi.mock('../../api/queries', () => ({ useWorkspaces: () => ({ data: ref([]) }) }));

const apps: ReturnType<typeof createTreeApp>[] = [];
afterEach(() => {
  apps.splice(0).forEach((app) => app.unmount());
});
function mount(component: typeof SkillPanel) {
  workspaceId.value = 'one';
  const root = node('root');
  const app = createTreeApp(component);
  apps.push(app);
  app.mount(root);
  return root;
}
async function field(root: ReturnType<typeof node>, value: string) {
  const option = all(root).find((n) => n.tag === 'button' && textOf(n) === (value || 'All'));
  expect(option).toBeDefined();
  (option!.props.onClick as () => void)();
  await nextTick();
}
it('sorts bundled and workspace skills together by name', () => {
  const root = mount(SkillPanel);
  expect(
    all(root)
      .filter((n) => n.tag === 'button' && n.props.class === 'skill-row')
      .map((n) => n.props['aria-label']),
  ).toEqual(['Alpha skill', 'astronomy', 'genomics', 'my skill']);
});
it('sorts built-in, catalogue and workspace connectors together by name', () => {
  const root = mount(ConnectorPanel);
  expect(
    all(root)
      .filter((n) => n.tag === 'button' && n.props.class === 'connector-row')
      .map((n) => n.props['aria-label']),
  ).toEqual(['genes', 'my connector', 'OpenAlex', 'telescope']);
});
it('filters skills by catalogue domain or Workspace and combines search', async () => {
  const root = mount(SkillPanel);
  await field(root, 'Physics');
  expect(all(root).some((n) => n.props['aria-label'] === 'astronomy')).toBe(true);
  expect(all(root).some((n) => n.props['aria-label'] === 'genomics')).toBe(false);
  expect(all(root).some((n) => n.props['aria-label'] === 'my skill')).toBe(false);
  const input = all(root).find(
    (n) => n.tag === 'input' && n.props['aria-label'] === 'Search skills',
  )!;
  (input.props.onInput as (event: unknown) => void)({ target: { value: 'genes' } });
  await nextTick();
  expect(all(root).some((n) => n.props['aria-label'] === 'astronomy')).toBe(false);
  (input.props.onInput as (event: unknown) => void)({ target: { value: '' } });
  await field(root, 'Workspace');
  expect(all(root).some((n) => n.props['aria-label'] === 'my skill')).toBe(true);
  expect(all(root).some((n) => n.props['aria-label'] === 'genomics')).toBe(false);
  workspaceId.value = 'two';
  await nextTick();
  expect(all(root).some((n) => n.props['aria-label'] === 'genomics')).toBe(true);
});
it('filters connectors by catalogue domain or Workspace', async () => {
  const root = mount(ConnectorPanel);
  await field(root, 'Biology');
  expect(all(root).some((n) => n.props['aria-label'] === 'genes')).toBe(true);
  expect(all(root).some((n) => n.props['aria-label'] === 'telescope')).toBe(false);
  expect(all(root).some((n) => n.props['aria-label'] === 'my connector')).toBe(false);
  await field(root, 'Workspace');
  expect(all(root).some((n) => n.props['aria-label'] === 'my connector')).toBe(true);
  expect(all(root).some((n) => n.props['aria-label'] === 'genes')).toBe(false);
});
