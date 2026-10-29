import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { h, nextTick, type Component } from 'vue';
import { createTreeApp, node, all, textOf } from './daemon-tree';
import CowriteRightPane from '../apps/dashboard/src/coresearch/cowrite/CowriteRightPane.vue';
import ProjectInfoPane from '../apps/dashboard/src/coresearch/cowrite/ProjectInfoPane.vue';
import ComputePanel from '../apps/dashboard/src/coresearch/cowrite/ComputePanel.vue';
import CommentsPane from '../apps/dashboard/src/coresearch/cowrite/CommentsPane.vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { workspaceId } from '../apps/dashboard/src/api/session';
const cleanup: (() => void)[] = [];
const calls: { url: string; body: unknown }[] = [];
let compute: { kernels: unknown[]; machines: unknown[] };
beforeEach(() => {
  workspaceId.value = 'ws';
  calls.length = 0;
  compute = { kernels: [], machines: [] };
  vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({
        url: String(url),
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      });
      return new Response(JSON.stringify(String(url).endsWith('/compute') ? compute : []));
    }),
  );
});
afterEach(() => {
  cleanup.splice(0).forEach((f) => f());
  vi.unstubAllGlobals();
});
function mount(component: Component, props: Record<string, unknown> = {}) {
  const root = node('root'),
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const app = createTreeApp({ render: () => h(component, props) });
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(root);
  cleanup.push(() => {
    app.unmount();
    client.clear();
  });
  return root;
}
async function click(root: ReturnType<typeof node>, label: string, event = 'onClick') {
  const button = all(root).find(
    (n) => n.tag === 'button' && (n.props['aria-label'] === label || textOf(n).trim() === label),
  );
  expect(button, label).toBeTruthy();
  (button!.props[event] as (e: object) => void)({});
  await nextTick();
}
const base = {
  projectId: 'p',
  title: 'Paper',
  builds: [],
  sources: [],
  versions: [],
  comments: [],
  people: [],
  meId: 'me',
};
it('Tools opens the four permanent tabs in source order and Close returns to PDF', async () => {
  const root = mount(CowriteRightPane, base);
  expect(textOf(root)).toContain('Nothing to show yet — compile the paper.');
  await click(root, 'Tools');
  expect(
    all(root)
      .filter((n) => n.props.role === 'tab')
      .map(textOf),
  ).toEqual(['Project Info', 'Versions', 'Comments', 'Logs']);
  expect(
    all(root)
      .filter((n) => String(n.props['aria-label']).startsWith('Close '))
      .map((n) => n.props['aria-label']),
  ).toEqual(['Close panel']);
  await click(root, 'Close panel');
  expect(textOf(root)).toContain('Compile');
});
it('Compute adds a tab and shows the real empty-kernel state', async () => {
  const root = mount(CowriteRightPane, { ...base, compute: true });
  await vi.waitFor(() => expect(textOf(root)).toContain('No live kernels'));
  expect(
    all(root)
      .filter((n) => n.props.role === 'tab')
      .map(textOf),
  ).toEqual(['Project Info', 'Versions', 'Comments', 'Logs', 'Compute']);
});
it('Stop requests a reason and sends it to the actual kernel operation route', async () => {
  compute = {
    kernels: [
      {
        chat_id: 'mine',
        kernel: 'python3',
        state: 'running',
        runtime_id: 'r',
        device_name: 'My Mac',
        samples: [{ t: '2026-10-28T10:00:00Z', rss: 1048576, cpu: 150 }],
      },
    ],
    machines: [
      {
        runtime_id: 'r',
        device_name: 'My Mac',
        metrics: {
          mem_used: 1,
          mem_total: 2,
          cpu_used: 150,
          cores: 8,
          disk_free: 10,
          disk_total: 20,
        },
      },
    ],
  };
  const root = mount(ComputePanel, { projectId: 'p', title: 'Paper' });
  await vi.waitFor(() => expect(textOf(root)).toContain('My Mac'));
  expect(textOf(root)).toContain('1.5 of 8 cores');
  await click(root, 'Actions for the Py kernel');
  await click(root, 'Stop', 'onMousedown');
  const input = all(root).find((n) => n.props.placeholder === 'Why? The agent reads this.');
  expect(input).toBeTruthy();
  (input!.props.onInput as (e: object) => void)({ target: { value: 'Need another method' } });
  await nextTick();
  await click(root, 'Stop');
  await vi.waitFor(() =>
    expect(calls).toContainEqual({
      url: '/api/coresearch/projects/p/chats/mine/kernel',
      body: { op: 'stop', reason: 'Need another method' },
    }),
  );
});
it('Project Info counts every supplied TeX file in the eight pinned tiles', () => {
  const root = mount(ProjectInfoPane, {
    sources: ['\\section{First}\nTwo words.', '\\section{Second}\nThree more words.'],
  });
  expect(
    all(root)
      .filter((n) => n.props.class === 'pinfo-label')
      .map(textOf),
  ).toEqual([
    'Words',
    'Words in Text',
    'Words in Headers',
    'Words outside text',
    'Number of headers',
    'Number of figures',
    'Number of math inlines',
    'Number of math displayed',
  ]);
  const values = all(root)
    .filter((n) => n.props.class === 'pinfo-value')
    .map(textOf);
  expect(values[0]).toBe('7');
  expect(values[4]).toBe('2');
});
it('Comments filters resolved and mentions while delete remains author-only', async () => {
  const comments = [
    {
      id: 'a',
      author_user_id: 'other',
      mentions: ['me'],
      body: 'Read this',
      quote: 'Passage',
      path: 'main.tex',
      line: 2,
      created_at: '2026-10-28T10:00:00Z',
      resolved_at: null,
    },
    {
      id: 'b',
      author_user_id: 'me',
      mentions: [],
      body: 'Done',
      quote: 'Text',
      path: 'other.tex',
      line: 1,
      created_at: '2026-10-28T10:00:00Z',
      resolved_at: '2026-10-28T11:00:00Z',
    },
  ];
  const root = mount(CommentsPane, {
    comments,
    people: [
      { id: 'me', name: 'Me' },
      { id: 'other', name: 'Other' },
    ],
    meId: 'me',
  });
  expect(all(root).filter((n) => n.props['aria-label'] === 'Delete comment')).toHaveLength(1);
  await click(root, 'Comments view: All Comments');
  await click(root, 'Only Unresolved');
  expect(
    all(root)
      .filter((n) => n.props.class === 'ms-comment-text md-prose')
      .map((n) => n.props.innerHTML),
  ).toEqual(['<p>Read this</p>\n']);
});
