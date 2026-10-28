import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createMemoryHistory, createRouter } from 'vue-router';
import AristotleChat from '../apps/dashboard/src/coresearch/AristotleChat.vue';
import ConversationRail from '../apps/dashboard/src/coresearch/ConversationRail.vue';
import { conversationKey } from '../apps/dashboard/src/coresearch/aristotle';
import { keys } from '../apps/dashboard/src/api/keys';
import { workspaceId } from '../apps/dashboard/src/api/session';
import { discardDialog, resolveDiscard } from '../apps/dashboard/src/state/resource-draft';
import { all, createTreeApp, node, textOf } from './daemon-tree';
vi.mock('../apps/dashboard/src/components/Aristotle.vue', () => ({
  default: { render: () => null },
}));
vi.hoisted(() =>
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { getItem: () => null, setItem() {}, removeItem() {} },
  }),
);
const cleanups: (() => void)[] = [];
afterEach(() => {
  cleanups.splice(0).forEach((f) => f());
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  resolveDiscard(false);
});
async function mount(withFile = false) {
  vi.stubGlobal('document', {
    activeElement: null,
    addEventListener() {},
    removeEventListener() {},
  });
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:controlled');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  vi.stubGlobal('Document', class {});
  vi.stubGlobal('ShadowRoot', class {});
  workspaceId.value = 'lab';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  });
  const agent = { id: 'a', name: 'Aristotle', harness: 'claude' };
  const conversation = {
    id: 'experiment',
    project_id: 'p',
    chat_id: 'c',
    agent_id: 'a',
    owner_id: 'me',
    mode: 'experiment',
    title: 'Residuals',
    is_welcome: false,
  };
  client.setQueryData(keys.me, { id: 'me' });
  client.setQueryData(['ws', 'lab', 'aristotle', 'me', 'bootstrap'], { agent, conversation });
  client.setQueryData(keys.agent('lab', 'a'), agent);
  client.setQueryData(conversationKey('lab', 'me'), [
    conversation,
    { project_id: 'paper', chat_id: 'co', mode: 'cowrite', shared: true, title: 'Shared paper' },
  ]);
  client.setQueryData(keys.runtimes('lab'), []);
  client.setQueryData(keys.coresearchMessages('lab', 'p', 'c'), []);
  client.setQueryData(keys.coresearchProject('lab', 'p'), {
    id: 'p',
    name: 'Residuals',
    mode: 'experiment',
  });
  client.setQueryData(keys.coresearchChats('lab', 'p'), [
    { id: 'c', owner_id: 'me', mode: 'experiment' },
  ]);
  const file = {
    id: 'f',
    path: 'notes.txt',
    head: { n: 1, size: 8, sha256: 'hash', created_at: '2026-10-28' },
  };
  client.setQueryData(keys.coresearchFiles('lab', 'p'), withFile ? [file] : []);
  client.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [file.head]);
  client.setQueryData(keys.coresearchContent('lab', 'p', 'f', 1), new Blob(['original']));
  client.setQueryData(keys.coresearchCells('lab', 'p', 'c'), []);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/coresearch/conversations/:conversationId', component: AristotleChat },
      { path: '/coresearch/:projectId/:chatId?', component: { render: () => null } },
    ],
  });
  await router.push('/coresearch/conversations/experiment');
  const app = createTreeApp({ render: () => h('div', [h(ConversationRail), h(AristotleChat)]) });
  app.use(VueQueryPlugin, { queryClient: client });
  app.use(router);
  const root = node('root');
  app.mount(root);
  const settle = async () => {
    await new Promise((r) => setTimeout(r, 0));
    await nextTick();
  };
  await settle();
  cleanups.push(() => {
    app.unmount();
    client.clear();
  });
  return { root, settle };
}
it('opens an Experiment inspector beside the conversation rail with Notebook Files Compute and no Runs', async () => {
  const { root, settle } = await mount();
  expect(all(root).some((n) => n.props['aria-label'] === 'Chat')).toBe(true);
  const notebook = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Notebook');
  expect(notebook).toBeTruthy();
  expect(notebook!.parent?.props.class).toBe('composer-top-row');
  (notebook!.props.onClick as () => void)();
  await settle();
  expect(
    all(root)
      .filter((n) => n.props.role === 'tab')
      .map(textOf),
  ).toEqual(['Notebook', 'Files', 'Compute']);
  expect(textOf(root)).not.toContain('Runs');
  expect(all(root).some((n) => n.props['aria-label'] === 'Project sidebar')).toBe(false);
});
it('links shared Cowrite rows to their project chat and labels session modes', async () => {
  const { root } = await mount();
  const link = all(root).find((n) => n.tag === 'a' && n.props.href === '/coresearch/paper/co');
  expect(link).toBeTruthy();
  expect(textOf(link!)).toContain('Cowrite');
  expect(textOf(root)).toContain('Experiment');
  expect(textOf(link!)).toContain('Shared');
});

it('returns Files to its existing list only after a dirty file draft is discarded', async () => {
  const { root, settle } = await mount(true);
  const click = async (label: string) => {
    const target = all(root).find(
      (n) => n.tag === 'button' && (n.props['aria-label'] === label || textOf(n).trim() === label),
    );
    expect(target).toBeTruthy();
    (target!.props.onClick as () => void)();
    await settle();
  };
  await click('Notebook');
  await click('Files');
  await click('notes.txt');
  await vi.waitFor(() =>
    expect(all(root).some((n) => n.props['data-testid'] === 'artifact-pane')).toBe(true),
  );
  await click('Edit content');
  const input = all(root).find(
    (n) => n.tag === 'textarea' && n.props['aria-label'] === 'File content',
  )!;
  (input.props['onUpdate:modelValue'] as (v: string) => void)('unsaved notes');
  await settle();
  await click('Files');
  expect(discardDialog.value).toBe(true);
  resolveDiscard(false);
  await settle();
  expect(all(root).some((n) => n.props['data-testid'] === 'artifact-pane')).toBe(true);
  await click('Files');
  expect(discardDialog.value).toBe(true);
  resolveDiscard(true);
  await settle();
  expect(all(root).some((n) => n.props['data-testid'] === 'artifact-pane')).toBe(false);
  expect(all(root).some((n) => n.props.title === 'notes.txt')).toBe(true);
});
