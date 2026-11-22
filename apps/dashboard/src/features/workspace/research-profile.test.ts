import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ssrContextKey, type Component } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import SettingsView from './SettingsView.vue';
import ColleaguesView from './ColleaguesView.vue';

vi.mock('../machines/MachinesView.vue', () => ({ default: { render: () => null } }));
vi.mock('./settings/StoragePanel.vue', () => ({ default: { render: () => null } }));
vi.mock('./settings/library/SkillPanel.vue', () => ({ default: { render: () => null } }));
vi.mock('./settings/library/ConnectorPanel.vue', () => ({ default: { render: () => null } }));
vi.mock('./settings/AnalyticsPanel.vue', () => ({ default: { render: () => null } }));

const cleanup: Array<() => void> = [];
const draft = {
  identity: 'Studied bees',
  links: ['https://example.test/work'],
};
const published = { ...draft, identity: 'Published introduction' };
const personal = {
  draft,
  published,
  draft_revision: 'draft-1',
  published_revision: 'published-1',
  workspace_ids: ['field'],
};
const workspaces = [
  { id: 'field', name: 'Field Lab', role: 'member' },
  { id: 'ocean', name: 'Ocean Lab', role: 'member' },
];
const viewer = { id: 'viewer', name: 'Researcher', email: 'r@example.test' };
const colleague = {
  user_id: 'colleague',
  name: 'Colleague',
  email: 'c@example.test',
  role: 'member',
};
const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

afterEach(() => {
  cleanup.splice(0).forEach((unmount) => unmount());
  workspaceId.value = '';
  vi.unstubAllGlobals();
});

async function mount(component: Component, fetch: typeof globalThis.fetch, path = '/settings') {
  vi.stubGlobal('document', { addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal('Document', class {});
  vi.stubGlobal('ShadowRoot', class {});
  vi.stubGlobal('window', { innerWidth: 1000, addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal('fetch', fetch);
  workspaceId.value = 'field';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity, staleTime: Infinity } },
  });
  client.setQueryData(keys.me, viewer);
  client.setQueryData(keys.workspaces, workspaces);
  client.setQueryData(keys.tokens, []);
  for (const workspace of workspaces) {
    client.setQueryData(keys.workspace(workspace.id), workspace);
    client.setQueryData(keys.members(workspace.id), [colleague]);
    client.setQueryData(keys.cards(workspace.id), []);
  }
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/settings', component },
      { path: '/colleagues/:memberId?', component },
    ],
  });
  await router.push(path);
  const app = createTreeApp({
    render: () => h(component, component === SettingsView ? { section: 'profile' } : {}),
  });
  app.use(VueQueryPlugin, { queryClient: client });
  app.use(router);
  app.provide(ssrContextKey, {});
  const root = node('root');
  app.mount(root);
  cleanup.push(() => {
    app.unmount();
    client.clear();
  });
  await tick();
  return { root, client };
}

function button(root: ReturnType<typeof node>, label: string) {
  return all(root).find((n) => n.tag === 'button' && textOf(n).trim() === label)!;
}

function submit(form: ReturnType<typeof node>) {
  return (form.props.onSubmit as (event: { preventDefault(): void }) => Promise<void>)({
    preventDefault() {},
  });
}

it('updates already published profiles with a single Settings save', async () => {
  let state = personal;
  const mutations: Array<{ method: string; path: string }> = [];
  const { root } = await mount(SettingsView, async (input, options) => {
    const method = options?.method ?? 'GET';
    if (method !== 'GET') mutations.push({ method, path: String(input) });
    if (method === 'PUT') {
      const saved = JSON.parse(String(options?.body));
      state = { ...state, draft: saved, published: saved, draft_revision: 'draft-2' };
    }
    return Response.json(state);
  });
  expect(textOf(root)).toContain('Research profile');
  expect(Boolean(button(root, 'Manage sharing'))).toBe(false);
  expect(Boolean(button(root, 'Publish changes'))).toBe(false);
  const editors = all(root).filter((n) => n.tag === 'textarea');
  expect(editors).toHaveLength(1);
  const [identity] = editors;
  expect(identity.props.value).toBe('Studied bees');
  const updatedIdentity = 'I study bees.\n\nI can help design field observations.';
  (identity.props.onInput as (event: unknown) => void)({
    target: { value: updatedIdentity },
  });
  await nextTick();
  const form = all(root).find((n) => n.tag === 'form' && textOf(n).includes('Save profile'))!;
  await submit(form);
  await nextTick();
  expect(mutations).toEqual([{ method: 'PUT', path: '/api/me/research-profile/save' }]);
  expect(state.draft).toEqual({ identity: updatedIdentity, links: draft.links });
  expect(state.published.identity).toBe(updatedIdentity);
  expect(textOf(root)).toContain('Profile updated in shared workspaces.');
});

it('drops the private editor contents when the authenticated account changes', async () => {
  let state = personal;
  const { root, client } = await mount(SettingsView, async () => Response.json(state));
  const identities = () =>
    all(root)
      .filter((n) => n.tag === 'textarea')
      .map((n) => n.props.value);
  expect(identities()).toContain('Studied bees');
  state = {
    ...personal,
    draft: { ...draft, identity: 'New account profile' },
    workspace_ids: [],
  };
  client.setQueryData(keys.me, { ...viewer, id: 'different-account' });
  await tick();
  await tick();
  expect(identities()).toContain('New account profile');
  expect(identities()).not.toContain('Studied bees');
});

it('loads a structured response from an older server into the identity editor', async () => {
  const { root } = await mount(SettingsView, async () =>
    Response.json({
      ...personal,
      draft: { background: 'Studied bees', methods: 'Field observation', links: draft.links },
    }),
  );
  const identity = all(root).find((n) => n.tag === 'textarea');
  expect(identity?.props.value).toBe('Studied bees\n\nMethods and tools: Field observation');
});

it('reads published colleague profiles in the selected workspace', async () => {
  const seen: string[] = [];
  const { root } = await mount(
    ColleaguesView,
    async (input, options) => {
      const path = String(input);
      seen.push(path);
      const workspace = (options?.headers as Record<string, string>)['X-Workspace-ID'];
      return Response.json({ profile: workspace === 'field' ? published : null });
    },
    '/colleagues/colleague',
  );
  expect(textOf(root)).toContain('Published introduction');
  expect(all(root).some((n) => n.props['aria-label'] === 'Edit research profile')).toBe(false);
  workspaceId.value = 'ocean';
  await tick();
  await tick();
  expect(textOf(root)).toContain('No research profile is published here.');
  expect(textOf(root)).not.toContain('Published introduction');
  expect(seen).toEqual([
    '/api/workspaces/field/members/colleague/research-profile',
    '/api/workspaces/ocean/members/colleague/research-profile',
  ]);
});
