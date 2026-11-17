import { afterEach, expect, it, vi } from 'vitest';
import { ref } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import SkillPanel from './settings/library/SkillPanel.vue';
import ConnectorPanel from './settings/library/ConnectorPanel.vue';
import RuntimeSkillImport from './settings/library/RuntimeSkillImport.vue';
import { workspaceId } from '../../api/session';

vi.mock('../../api/library', () => {
  const result = (data: unknown[]) => ({
    data: ref(data),
    isPending: ref(false),
    error: ref(null),
    refetch: vi.fn(),
  });
  return {
    useLibraryMutations: () => ({}),
    useRuntimeSkillRequest: () => ({
      data: ref(null),
      error: ref(null),
      isFetching: ref(false),
      isError: ref(false),
      refetch: vi.fn(),
    }),
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
    useWorkspaceMCPServers: () =>
      result([
        {
          id: 'c',
          name: 'my connector',
          transport: 'url',
          headers: { Authorization: 'secret-fixture' },
        },
      ]),
    useOpenAlexConnector: () => ({
      data: ref({ key_set: false, revision: 0, policy_enabled: true }),
      isError: ref(false),
    }),
  };
});
vi.mock('../../api/queries', () => ({
  useWorkspaces: () => ({ data: ref([]) }),
  useMe: () => ({ data: ref({ id: 'u' }) }),
  useRuntimes: () => ({
    data: ref([
      {
        id: 'r',
        owner_id: 'u',
        device_name: 'Offline machine',
        harness: 'codex',
        status: 'offline',
      },
    ]),
    isPending: ref(false),
    error: ref(null),
  }),
}));
vi.mock('../../state/availability', () => ({
  useAvailabilityChanges: () => ({
    error: ref(''),
    allowed: ref(false),
    catalogueAllowed: ref(false),
    pending: ref(''),
    toggle: vi.fn(),
  }),
}));

const apps: ReturnType<typeof createTreeApp>[] = [];
afterEach(() => {
  apps.splice(0).forEach((app) => app.unmount());
  vi.unstubAllGlobals();
});
function mount(component: typeof SkillPanel) {
  vi.stubGlobal('document', { addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} });
  workspaceId.value = 'one';
  const root = node('root');
  const app = createTreeApp(component);
  app.use(
    createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:path(.*)*', component: { render: () => null } }],
    }),
  );
  apps.push(app);
  app.mount(root);
  return root;
}
it('labels bundled and workspace skills using their actual origin', () => {
  const root = mount(SkillPanel);
  const row = (name: string) =>
    all(root).find((n) => n.tag === 'button' && n.props['aria-label'] === name)!;
  expect(textOf(row('astronomy'))).toContain('Built-in');
  expect(textOf(row('my skill'))).toContain('Workspace');
});
it('distinguishes scientific connectors from custom MCP servers', () => {
  const root = mount(ConnectorPanel);
  const row = (name: string) =>
    all(root).find((n) => n.tag === 'button' && n.props['aria-label'] === name)!;
  expect(textOf(row('my connector'))).toContain('Custom MCP');
  expect(all(root).some((n) => n.tag === 'button' && textOf(n).includes('Add connector'))).toBe(
    false,
  );
});

it('does not offer an offline runtime for skill import', () => {
  const root = mount(RuntimeSkillImport as typeof SkillPanel);
  const option = all(root).find(
    (n) => n.tag === 'option' && textOf(n).includes('Offline machine'),
  )!;
  expect(option.props.disabled).toBe(true);
});
it('does not reveal connector configuration in the library list', () => {
  const root = mount(ConnectorPanel);
  expect(textOf(root)).not.toContain('secret-fixture');
});
