import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick, type Component } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createMemoryHistory, createRouter } from 'vue-router';
import CoresearchProject from './CoresearchProject.vue';
import ChatComposer from './ChatComposer.vue';
import ChatTranscript from './ChatTranscript.vue';
import MembersDialog from './MembersDialog.vue';
import NewChatDialog from './NewChatDialog.vue';
import ProjectSidebar from './ProjectSidebar.vue';
import SidebarFiles from './SidebarFiles.vue';
import AssistantMessage from './AssistantMessage.vue';
import RightPaneTabs from './RightPaneTabs.vue';
import { tabAfterClose, PANE_TABS } from './pane-tabs';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
vi.mock('vue-spinner', async () => {
  const { h } = await import('vue');
  return { RingLoader: { render: () => h('div', { class: 'v-spinner' }) } };
});
vi.hoisted(() => {
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { getItem: () => null, setItem() {}, removeItem() {} },
  });
});
const cleanups: (() => void)[] = [];
afterEach(() => {
  cleanups.splice(0).forEach((fn) => fn());
  vi.unstubAllGlobals();
});
const project = {
  id: 'p',
  name: 'Orbital residuals',
  role: 'owner',
  created_by: 'me',
  archived_at: null,
};
const chat = { id: 'c', owner_id: 'me', cli: 'claude', project_id: 'p' };
const messages = [
  {
    id: 'turn',
    kind: 'turn',
    body: 'Inspect the residuals',
    agent_run_id: 'run',
    status: 'failed',
    failure_reason: 'timeout',
  },
  { id: 'reply', kind: 'reply', body: 'The residuals are stable.', agent_run_id: 'run' },
  { id: 'comment', kind: 'comment', body: 'Check the baseline too.', author_user_id: 'other' },
  {
    id: 'failure',
    kind: 'failure',
    body: 'The run timed out.',
    agent_run_id: 'run',
    failure_reason: 'timeout',
  },
];
async function mount(component: Component, props = {}, seed?: (client: QueryClient) => void) {
  vi.stubGlobal('window', { innerWidth: 1500, addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal('document', { activeElement: null });
  vi.stubGlobal('Document', class {});
  vi.stubGlobal('ShadowRoot', class {});
  workspaceId.value = 'lab';
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity, gcTime: Infinity, enabled: false },
    },
  });
  client.setQueryData(keys.me, { id: 'me', name: 'Ada' });
  client.setQueryData(keys.members('lab'), [
    { user_id: 'me', name: 'Ada' },
    { user_id: 'other', name: 'Grace' },
  ]);
  client.setQueryData(keys.runtimes('lab'), [
    { owner_id: 'me', status: 'online', harness: 'claude' },
    { owner_id: 'other', status: 'online', harness: 'codex' },
  ]);
  client.setQueryData(keys.coresearchProject('lab', 'p'), project);
  client.setQueryData(keys.coresearchChats('lab', 'p'), [chat]);
  client.setQueryData(keys.coresearchMessages('lab', 'p', 'c'), []);
  client.setQueryData(keys.coresearchFiles('lab', 'p'), []);
  client.setQueryData(keys.coresearchSteps('lab', 'p', 'c', 'run'), []);
  client.setQueryData(keys.coresearchMembers('lab', 'p'), [{ user_id: 'me', role: 'owner' }]);
  seed?.(client);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push('/coresearch/p/c');
  const app = createTreeApp({ render: () => h(component, props) });
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
  return { root, client, router, settle };
}
const button = (root: ReturnType<typeof node>, label: string) =>
  all(root).find(
    (n) => n.tag === 'button' && (n.props['aria-label'] === label || textOf(n).trim() === label),
  );
describe('Coresearch workspace', () => {
  it('chat owner sees Send and other members see Comment regardless of project role', async () => {
    const owner = await mount(CoresearchProject, { projectId: 'p', chatId: 'c' }, (c) =>
      c.setQueryData(keys.coresearchProject('lab', 'p'), { ...project, role: 'member' }),
    );
    expect(button(owner.root, 'Send')).toBeTruthy();
    expect(button(owner.root, 'Comment')).toBeUndefined();
    const member = await mount(CoresearchProject, { projectId: 'p', chatId: 'c' }, (c) =>
      c.setQueryData(keys.coresearchChats('lab', 'p'), [{ ...chat, owner_id: 'other' }]),
    );
    expect(button(member.root, 'Comment')).toBeTruthy();
    expect(button(member.root, 'Stop')).toBeUndefined();
  });
  it('failure row shows the reason and comments are labelled', async () => {
    const { root } = await mount(ChatTranscript, { projectId: 'p', chatId: 'c', messages });
    expect(textOf(root)).toContain('timeout');
    expect(textOf(root)).toContain('Comment');
    expect(
      all(root).filter((n) => String(n.props.class).includes('run-line--failed')),
    ).toHaveLength(1);
  });
  it('preserves Notebook Files Compute order', async () => {
    const { root } = await mount(RightPaneTabs, {
      tabs: PANE_TABS,
      activeId: 'notebook',
      paneMode: 'split',
    });
    expect(
      all(root)
        .filter((n) => n.props.role === 'tab')
        .map(textOf),
    ).toEqual(['Notebook', 'Files', 'Compute']);
  });
  it('tabAfterClose picks the right neighbour then the left if last', () => {
    expect(tabAfterClose(PANE_TABS, 'files')).toBe('compute');
    expect(tabAfterClose(PANE_TABS, 'compute')).toBe('files');
    expect(tabAfterClose([PANE_TABS[0]!], 'notebook')).toBeNull();
    expect(tabAfterClose(PANE_TABS, 'missing')).toBeNull();
  });
  it('members dialog is read only for project members', async () => {
    const { root } = await mount(MembersDialog, { projectId: 'p', editable: false });
    expect(textOf(root)).toContain('Ada');
    expect(textOf(root)).toContain('Owner');
    expect(button(root, 'Add member')).toBeUndefined();
    expect(button(root, 'Remove Ada')).toBeUndefined();
  });
  it('new chat offers only the callers supported online CLIs', async () => {
    const { root } = await mount(NewChatDialog, { projectId: 'p' });
    expect(
      all(root)
        .filter((n) => n.props['data-cli'])
        .map((n) => n.props['data-cli']),
    ).toEqual(['claude']);
  });
  it('sends and cancels through the dedicated Coresearch routes', async () => {
    const requests: { url: string; method: string; body: unknown }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, options: RequestInit) => {
        requests.push({
          url,
          method: options.method!,
          body: options.body ? JSON.parse(String(options.body)) : null,
        });
        return new Response(JSON.stringify({ id: 'new-run' }), {
          status: 201,
          headers: { 'Content-Type': 'application/json' },
        });
      }),
    );
    const { root, settle } = await mount(ChatComposer, {
      projectId: 'p',
      chatId: 'c',
      owner: true,
    });
    const input = all(root).find((n) => n.tag === 'textarea')!;
    (input.props['onUpdate:modelValue'] as (v: string) => void)('Check baseline');
    await settle();
    (button(root, 'Send')!.props.onClick as () => void)();
    await settle();
    expect(requests[0]).toMatchObject({
      url: '/api/coresearch/projects/p/chats/c/turns',
      method: 'POST',
      body: { body: 'Check baseline' },
    });
    const running = await mount(ChatComposer, {
      projectId: 'p',
      chatId: 'c',
      owner: true,
      activeRun: 'run',
    });
    (button(running.root, 'Stop')!.props.onClick as () => void)();
    await running.settle();
    expect(requests[1]).toMatchObject({
      url: '/api/coresearch/projects/p/chats/c/turns/run/cancel',
      method: 'POST',
    });
  });
  it('members can comment during a running turn and retain a rejected draft', async () => {
    const requests: { url: string; body: unknown }[] = [];
    let reject = true;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, options: RequestInit) => {
        requests.push({ url, body: JSON.parse(String(options.body)) });
        return new Response(
          JSON.stringify(reject ? { error: 'Comment refused' } : { id: 'comment' }),
          { status: reject ? 409 : 201, headers: { 'Content-Type': 'application/json' } },
        );
      }),
    );
    const { root, settle } = await mount(ChatComposer, {
      projectId: 'p',
      chatId: 'c',
      owner: false,
      activeRun: 'run',
    });
    const input = all(root).find((n) => n.tag === 'textarea')!;
    (input.props['onUpdate:modelValue'] as (v: string) => void)('Review the baseline');
    await settle();
    (button(root, 'Comment')!.props.onClick as () => void)();
    await settle();
    expect(requests[0]).toEqual({
      url: '/api/coresearch/projects/p/chats/c/comments',
      body: { body: 'Review the baseline' },
    });
    expect(textOf(root)).toContain('Comment refused');
    expect(button(root, 'Comment')!.props.disabled).toBe(false);
    reject = false;
    (button(root, 'Comment')!.props.onClick as () => void)();
    await settle();
    expect(requests[1]?.body).toEqual({ body: 'Review the baseline' });
    expect(button(root, 'Comment')!.props.disabled).toBe(true);
    expect(button(root, 'Stop')).toBeUndefined();
  });
  it('reads paired tool steps only through the member checked turn route', async () => {
    const urls: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        urls.push(url);
        return new Response(
          JSON.stringify([
            {
              attempt: 1,
              seq: 1,
              type: 'tool_use',
              tool: 'Bash',
              content: { type: 'tool_use', id: 'call', input: { command: 'echo baseline' } },
            },
            {
              attempt: 1,
              seq: 2,
              type: 'tool_result',
              tool: '',
              content: { tool_use_id: 'call', content: 'baseline' },
            },
          ]),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }),
    );
    const { root, settle } = await mount(ChatTranscript, {
      projectId: 'p',
      chatId: 'c',
      messages: [{ ...messages[0], agent_run_id: 'different' }],
    });
    await settle();
    expect(urls).toEqual(['/api/coresearch/projects/p/chats/c/turns/different/steps']);
    const step = all(root).find((n) => n.tag === 'details' && n.props.class === 'tool-step');
    expect(step).toBeTruthy();
    expect(textOf(step!)).toContain('echo baseline');
    expect(textOf(step!)).toContain('baseline');
  });
  it('owner membership controls exclude existing members and protect the last owner', async () => {
    const requests: { url: string; method: string; body: unknown }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, options: RequestInit) => {
        requests.push({
          url,
          method: options.method!,
          body: options.body ? JSON.parse(String(options.body)) : null,
        });
        return new Response(
          JSON.stringify(
            options.method === 'GET'
              ? [
                  { user_id: 'me', role: 'owner' },
                  { user_id: 'other', role: 'member' },
                ]
              : { user_id: 'other', role: 'member' },
          ),
          {
            status: 201,
            headers: { 'Content-Type': 'application/json' },
          },
        );
      }),
    );
    const { root, settle } = await mount(MembersDialog, { projectId: 'p', editable: true });
    expect(button(root, 'Remove Ada')!.props.disabled).toBe(true);
    expect(
      all(root)
        .filter((n) => n.tag === 'option' && n.props.value)
        .map((n) => n.props.value),
    ).toEqual(['other']);
    (
      all(root).find((n) => n.tag === 'select')!.props['onUpdate:modelValue'] as (v: string) => void
    )('other');
    await settle();
    (button(root, 'Add member')!.props.onClick as () => void)();
    await settle();
    expect(requests[0]).toMatchObject({
      url: '/api/coresearch/projects/p/members',
      method: 'POST',
      body: { user_id: 'other', role: 'member' },
    });
  });
  it('caps files at ten and keeps an opened file beyond the cap visible', async () => {
    const files = Array.from({ length: 12 }, (_, i) => ({
      id: String(i),
      path: `data/${i}.csv`,
      head: { n: 1 },
    }));
    const { root } = await mount(SidebarFiles, { files, limit: 10 });
    expect(all(root).filter((n) => String(n.props.class) === 'tsb-file-open')).toHaveLength(10);
    expect(button(root, 'See all 12 files')).toBeTruthy();
    const active = await mount(SidebarFiles, { files, limit: 10, activeId: '11' });
    expect(all(active.root).some((n) => n.props.title === 'data/11.csv')).toBe(true);
  });
  it('replies escape raw HTML and unsafe URLs while code remains copyable', async () => {
    const { root } = await mount(AssistantMessage, {
      text: '<img src=x onerror=alert(1)> [click](javascript:alert(1))\n\n```python\nprint("safe")\n```',
    });
    const html = all(root)
      .map((n) => n.props.innerHTML ?? '')
      .join('');
    expect(html).toContain('&lt;img');
    expect(html).not.toContain('<img');
    expect(html).not.toContain('href="javascript:');
    expect(textOf(root)).toContain('print("safe")');
    expect(button(root, 'Copy')).toBeTruthy();
  });
  it('keeps fenced code and continuation paragraphs inside their Markdown containers', async () => {
    const { root } = await mount(AssistantMessage, {
      text: '3. First item\n\n   ```python\n   print(1)\n   ```\n\n   Continued within item\n\n4. Second item\n\n> Quote\n>\n> ```python\n> print(2)\n> ```\n>\n> After quote code',
    });
    const list = all(root).find((n) => n.tag === 'ol');
    expect(list).toBeTruthy();
    expect(String(list!.props.start)).toBe('3');
    const items = all(list!).filter((n) => n.tag === 'li');
    expect(items).toHaveLength(2);
    expect(all(items[0]!).filter((n) => n.props.class === 'code-block')).toHaveLength(1);
    expect(
      all(items[0]!).some((n) => String(n.props.innerHTML).includes('Continued within item')),
    ).toBe(true);
    const quote = all(root).find((n) => n.tag === 'blockquote');
    expect(quote).toBeTruthy();
    expect(all(quote!).filter((n) => n.props.class === 'code-block')).toHaveLength(1);
  });
  it('shows the same ring spinner for active chat turns, and not queued or finished turns', async () => {
    for (const status of ['running', 'dispatched', 'queued', 'completed']) {
      const { root } = await mount(ChatTranscript, {
        projectId: 'p',
        chatId: 'c',
        messages: [{ ...messages[0], status }],
      });
      const spinner = all(root).find((node) =>
        String(node.props.class).includes('run-status-spinner'),
      );
      expect(Boolean(spinner), status).toBe(status === 'running' || status === 'dispatched');
    }
  });
  it('hides run logs and status entries while retaining real tool steps', async () => {
    const { root } = await mount(
      ChatTranscript,
      { projectId: 'p', chatId: 'c', messages: messages.slice(0, 2) },
      (c) =>
        c.setQueryData(keys.coresearchSteps('lab', 'p', 'c', 'run'), [
          { attempt: 1, seq: 1, type: 'log', tool: '', content: 'diagnostic' },
          { attempt: 1, seq: 2, type: 'status', tool: '', content: 'working' },
          { attempt: 1, seq: 4, type: 'text', tool: '', content: '' },
          { attempt: 1, seq: 5, type: 'thinking', tool: '', content: '   ' },
          {
            attempt: 1,
            seq: 3,
            type: 'tool_use',
            tool: 'Bash',
            content: { type: 'tool_use', id: 'call', input: { command: 'echo baseline' } },
          },
        ]),
    );
    expect(textOf(root)).not.toContain('Run log');
    expect(textOf(root)).not.toContain('Run status');
    expect(
      all(root).filter((n) => n.tag === 'details' && n.props.class === 'tool-step'),
    ).toHaveLength(1);
    expect(textOf(root)).not.toContain('3 steps');
    expect(
      all(root).filter(
        (n) => n.tag === 'li' && String(n.props.class).split(/\s+/).includes('mark-message'),
      ),
    ).toHaveLength(1);
  });
  it('thinking stays prose and is excluded from the copied assistant reply', async () => {
    const copied: string[] = [];
    vi.stubGlobal('navigator', {
      clipboard: {
        writeText: async (text: string) => {
          copied.push(text);
        },
      },
    });
    const { root, settle } = await mount(
      ChatTranscript,
      { projectId: 'p', chatId: 'c', messages: messages.slice(0, 2) },
      (c) =>
        c.setQueryData(keys.coresearchSteps('lab', 'p', 'c', 'run'), [
          { attempt: 1, seq: 1, type: 'thinking', tool: '', content: 'Checking uncertainty.' },
        ]),
    );
    expect(
      all(root).filter((n) => n.tag === 'details' && n.props.class === 'tool-step'),
    ).toHaveLength(0);
    expect(
      all(root)
        .map((n) => n.props.innerHTML ?? '')
        .join(''),
    ).toContain('Checking uncertainty.');
    const reply = all(root).find((n) => n.props.class === 'turn-reply')!;
    (
      all(reply).find((n) => n.tag === 'button' && textOf(n).trim() === 'Copy')!.props
        .onClick as () => void
    )();
    await settle();
    expect(copied).toEqual(['The residuals are stable.']);
  });
  it('does not duplicate a settled reply when a comment arrived during the turn', async () => {
    const { root } = await mount(
      ChatTranscript,
      { projectId: 'p', chatId: 'c', messages: [messages[0], messages[2], messages[1]] },
      (c) =>
        c.setQueryData(keys.coresearchSteps('lab', 'p', 'c', 'run'), [
          { attempt: 1, seq: 1, type: 'text', tool: '', content: 'The residuals are stable.' },
        ]),
    );
    const html = all(root)
      .map((n) => n.props.innerHTML ?? '')
      .join('');
    expect(html.split('The residuals are stable.')).toHaveLength(2);
  });
});

it.each(['', '   '])('uses email or ID when owner name is blank: %j', async (name) => {
  const members = [
    { user_id: 'me', name, email: 'ada@example.org' },
    { user_id: 'other', name, email: ' ' },
  ];
  const sidebar = await mount(ProjectSidebar, {
    name: 'Project',
    files: [],
    chats: [chat, { ...chat, id: 'other-chat', owner_id: 'other' }],
    members,
  });
  expect(textOf(sidebar.root)).toContain('ada@example.org');
  expect(textOf(sidebar.root)).toContain('other');
  expect(all(sidebar.root).some((n) => n.props['aria-label'] === 'Owner: ada@example.org')).toBe(
    true,
  );
  const dialog = await mount(MembersDialog, { projectId: 'p', editable: true }, (c) => {
    c.setQueryData(keys.members('lab'), members);
    c.setQueryData(keys.coresearchMembers('lab', 'p'), [
      { user_id: 'me', role: 'owner' },
      { user_id: 'other', role: 'member' },
    ]);
  });
  expect(button(dialog.root, 'Remove ada@example.org')).toBeTruthy();
  expect(button(dialog.root, 'Remove other')).toBeTruthy();
});

it('opens the Experiment project chooser and selects Compute', async () => {
  const { root, settle } = await mount(CoresearchProject, { projectId: 'p', chatId: 'c' });
  const toggle = button(root, 'Open experiment surfaces');
  expect(toggle).toBeTruthy();
  (toggle!.props.onClick as () => void)();
  await settle();
  expect(textOf(root)).toContain('Open a surface');
  const compute = button(root, 'Compute');
  expect(compute).toBeTruthy();
  (compute!.props.onClick as () => void)();
  await settle();
  expect(all(root).some((n) => n.props['data-testid'] === 'compute-panel')).toBe(true);
});

it('uses an overlay when the Experiment project container is narrow', async () => {
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
  const { root, settle } = await mount(CoresearchProject, { projectId: 'p', chatId: 'c' });
  const toggle = button(root, 'Open experiment surfaces')!;
  (toggle.props.onClick as () => void)();
  await settle();
  expect(all(root).find((n) => n.props['aria-label'] === 'Right pane')?.props.role).toBe('dialog');
});
