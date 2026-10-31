import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { nextTick, ref } from 'vue';
import { all, createTreeApp, node, textOf } from './daemon-tree';
import ConnectorPanel from '../apps/dashboard/src/components/settings/library/ConnectorPanel.vue';
import { workspaceId } from '../apps/dashboard/src/api/session';

const openalex = ref<Record<string, unknown> | undefined>();
const role = ref('owner');
const setConnector = vi.fn();
vi.mock('../apps/dashboard/src/api/library', () => {
  const result = (data: unknown[]) => ({
    data: ref(data),
    isPending: ref(false),
    error: ref(null),
    refetch: vi.fn(),
  });
  return {
    useConnectorLibrary: () =>
      result([{ name: 'telescope', purpose: 'stars', domain: 'Physics', policy_enabled: true }]),
    useWorkspaceMCPServers: () => result([{ id: 'c', name: 'my connector', transport: 'url' }]),
    useOpenAlexConnector: () => ({ data: openalex, refetch: vi.fn() }),
    useLibraryMutations: () => ({ setConnector }),
  };
});
vi.mock('../apps/dashboard/src/api/queries', () => ({
  useWorkspaces: () => ({ data: ref([{ id: 'one', role: role.value }]), refetch: vi.fn() }),
}));
vi.mock('../apps/dashboard/src/state/availability', () => ({
  useAvailabilityChanges: () => ({
    error: ref(''),
    allowed: ref(false),
    catalogueAllowed: ref(false),
    pending: ref(''),
    toggle: vi.fn(),
  }),
}));

const apps: ReturnType<typeof createTreeApp>[] = [];
beforeEach(() => {
  vi.stubGlobal('document', {
    activeElement: null,
    addEventListener() {},
    removeEventListener() {},
  });
  openalex.value = { name: 'openalex', key_set: false, revision: 0, takes_key: true };
  role.value = 'owner';
  setConnector.mockReset().mockResolvedValue({});
});
afterEach(() => {
  apps.splice(0).forEach((app) => app.unmount());
  vi.unstubAllGlobals();
});
function mount() {
  workspaceId.value = 'one';
  const root = node('root');
  const app = createTreeApp(ConnectorPanel);
  apps.push(app);
  app.mount(root);
  return root;
}
const byLabel = (root: ReturnType<typeof node>, label: string) =>
  all(root).find((n) => n.props['aria-label'] === label);
async function click(root: ReturnType<typeof node>, text: string) {
  const b = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === text);
  expect(b, text).toBeDefined();
  (b!.props.onClick as () => void)();
  await nextTick();
}
async function openDialog(root: ReturnType<typeof node>) {
  (byLabel(root, 'OpenAlex')!.props.onClick as () => void)();
  await nextTick();
}

it('lists OpenAlex under Literature with its daily budget', async () => {
  const root = mount();
  expect(textOf(byLabel(root, 'OpenAlex')!)).toContain('No key · about 100 searches a day');
  await click(root, 'Literature');
  expect(byLabel(root, 'OpenAlex')).toBeDefined();
  expect(byLabel(root, 'telescope')).toBeUndefined();
  await click(root, 'Workspace');
  expect(byLabel(root, 'OpenAlex')).toBeUndefined();
});

it('tells a workspace without a key what a free key gives, and saves one', async () => {
  const root = mount();
  await openDialog(root);
  const text = textOf(root);
  expect(text).toContain('A free API key gives this workspace about 10× more OpenAlex searches.');
  expect(text).toContain('~100');
  expect(text).toContain('~1,000');
  const input = byLabel(root, 'OpenAlex API key')!;
  (input.props.onInput as (e: unknown) => void)({ target: { value: 'oa-key-1234567' } });
  await nextTick();
  await click(root, 'Save');
  expect(setConnector).toHaveBeenCalledWith('openalex', { revision: 0, key: 'oa-key-1234567' });
});

it('confirms a saved key and clears it', async () => {
  openalex.value = { name: 'openalex', key_set: true, revision: 3, takes_key: true };
  const root = mount();
  expect(textOf(byLabel(root, 'OpenAlex')!)).toContain('Key saved · about 1,000 searches a day');
  await openDialog(root);
  expect(textOf(root)).toContain('Key saved: about 1,000 OpenAlex searches a day');
  expect(textOf(root)).not.toContain('about 10× more');
  await click(root, 'Clear saved key');
  expect(setConnector).toHaveBeenCalledWith('openalex', { revision: 3, key: '' });
});

it('lets only an owner change the key', async () => {
  role.value = 'member';
  const root = mount();
  await openDialog(root);
  expect(byLabel(root, 'OpenAlex API key')!.props.disabled).toBe(true);
  expect(textOf(root)).toContain('Only the workspace owner can save a key.');
});
