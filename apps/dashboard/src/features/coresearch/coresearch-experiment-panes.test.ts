import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createMemoryHistory, createRouter } from 'vue-router';
import AristotleChat from './AristotleChat.vue';
import ConversationRail from './ConversationRail.vue';
import { conversationKey } from './aristotle';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { discardDialog, resolveDiscard } from '../../state/resource-draft';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
vi.mock('./Aristotle.vue', () => ({
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
  const session = new Map<string, string>();
  vi.stubGlobal('sessionStorage', {
    getItem: (key: string) => session.get(key) ?? null,
    setItem: (key: string, value: string) => session.set(key, value),
    removeItem: (key: string) => session.delete(key),
  });
  vi.stubGlobal('window', { innerWidth: 1500, addEventListener() {}, removeEventListener() {} });
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
  expect(all(root).some((n) => n.props.class === 'composer-notebook-chip')).toBe(false);
  const toggle = all(root).find((n) => n.props['aria-label'] === 'Open experiment surfaces')!;
  (toggle.props.onClick as () => void)();
  await settle();
  const notebook = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Notebook')!;
  (notebook.props.onClick as () => void)();
  await settle();
  expect(all(root).some((n) => n.props['aria-label'] === 'Notebook')).toBe(true);
  const back = all(root).find((n) => n.props['aria-label'] === 'Back to surfaces')!;
  (back.props.onClick as () => void)();
  await settle();
  expect(textOf(root)).toContain('Open a surface');
  expect(textOf(root)).toContain('Files');
  expect(textOf(root)).toContain('Compute');
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
  await click('Open experiment surfaces');
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
  await click('Back to surfaces');
  expect(discardDialog.value).toBe(true);
  resolveDiscard(false);
  await settle();
  expect(all(root).some((n) => n.props['data-testid'] === 'artifact-pane')).toBe(true);
  await click('Back to surfaces');
  expect(discardDialog.value).toBe(true);
  resolveDiscard(true);
  await settle();
  expect(all(root).some((n) => n.props['data-testid'] === 'artifact-pane')).toBe(false);
  await click('Files');
  expect(all(root).some((n) => n.props.title === 'notes.txt')).toBe(true);
});

it('opens the Experiment chooser with one surface at a time', async () => {
  const { root, settle } = await mount();
  const toggle = all(root).find((n) => n.props['aria-label'] === 'Open experiment surfaces');
  expect(toggle).toBeTruthy();
  (toggle!.props.onClick as () => void)();
  await settle();
  expect(
    all(root).find((n) => n.props['aria-label'] === 'Open experiment surfaces'),
  ).toBeUndefined();
  expect(textOf(root)).toContain('Open a surface');
  for (const label of ['Notebook', 'Files', 'Compute']) {
    const choice = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === label);
    expect(choice).toBeTruthy();
    (choice!.props.onClick as () => void)();
    await settle();
    const back = all(root).find((n) => n.props['aria-label'] === 'Back to surfaces');
    expect(back).toBeTruthy();
    (back!.props.onClick as () => void)();
    await settle();
    expect(textOf(root)).toContain('Open a surface');
  }
  (all(root).find((n) => n.props['aria-label'] === 'Close pane')!.props.onClick as () => void)();
  await settle();
  expect(all(root).find((n) => n.props['aria-label'] === 'Open experiment surfaces')).toBeTruthy();
});

it('uses an overlay for a narrow Experiment container in a wide window', async () => {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(private callback: (entries: unknown[]) => void) {}
      observe() {
        this.callback([{ contentRect: { width: 550 } }]);
      }
      disconnect() {}
    },
  );
  const { root, settle } = await mount();
  const toggle = all(root).find((n) => n.props['aria-label'] === 'Open experiment surfaces')!;
  (toggle.props.onClick as () => void)();
  await settle();
  expect(all(root).find((n) => n.props['aria-label'] === 'Right pane')?.props.role).toBe('dialog');
});
