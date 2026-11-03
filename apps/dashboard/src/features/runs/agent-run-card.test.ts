import { afterEach, expect, it, vi } from 'vitest';
import { createRenderer, h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import AgentRunCard from './AgentRunCard.vue';
import type { AgentRun, Comment, RunMessage, Runtime } from '../../api/types';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { all, node, textOf } from '../../../../../tests/daemon-tree';

// The live RunTurn spinner uses v-show, which needs an element style object.
const { createApp: createTreeApp } = createRenderer<
  ReturnType<typeof node>,
  ReturnType<typeof node>
>({
  createElement: (tag) => Object.assign(node(tag), { style: {} }),
  createText: (text) => node('#text', text),
  createComment: () => node('#comment'),
  setText: (n, text) => (n.text = text),
  setElementText: (n, text) => {
    n.children = [];
    n.text = text;
  },
  insert(child, parent, anchor) {
    child.parent?.children.splice(child.parent.children.indexOf(child), 1);
    child.parent = parent;
    const index = anchor ? parent.children.indexOf(anchor) : -1;
    if (index < 0) parent.children.push(child);
    else parent.children.splice(index, 0, child);
  },
  remove(child) {
    child.parent?.children.splice(child.parent.children.indexOf(child), 1);
    child.parent = null;
  },
  parentNode: (n) => n.parent,
  nextSibling: (n) => n.parent?.children[n.parent.children.indexOf(n) + 1] ?? null,
  patchProp: (n, key, _prev, next) => (n.props[key] = next),
});

const disposals: (() => void)[] = [];
afterEach(() => {
  disposals.splice(0).forEach((dispose) => dispose());
  workspaceId.value = '';
  vi.unstubAllGlobals();
});
const run: AgentRun = {
  id: 'run-1',
  workspace_id: 'w',
  card_id: 'c',
  responsible_user_id: 'u',
  board_column: 'grounding',
  round: 1,
  role: 'leader',
  harness: 'claude',
  status: 'completed',
  queue_reason: null,
  runtime_id: null,
  dispatched_at: null,
  started_at: null,
  completed_at: null,
  attempt: 1,
  max_attempts: 1,
  model: 'Recorded model',
  error: '',
  failure_reason: null,
  cost_usd: 0,
  created_at: '2026-11-03T10:00:00Z',
  handoff_note: '',
  agent_id: 'a',
  agent_name: 'Ada',
  assignment: '',
  latest_text: '',
  answer: 'A **three-line** preview.',
  last_activity_at: null,
};
function setup(
  extra: Partial<AgentRun> = {},
  runtime: Runtime | null = null,
  replies: Comment[] = [],
) {
  vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} });
  workspaceId.value = 'w';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  client.setQueryData(keys.members('w'), [{ user_id: 'u', name: 'Owner' }]);
  client.setQueryData(['ws', 'w', 'run-reactions', 'c', 'run-1'], []);
  client.setQueryData(keys.card('w', 'c'), {
    id: 'c',
    machine_label: 'Bench',
    machine_notice: 'The study machine needs attention.',
  });
  const messages: RunMessage[] = [
    {
      agent_run_id: 'run-1',
      attempt: 1,
      seq: 1,
      type: 'tool_use',
      tool: 'Bash',
      content: { id: 'tool-1', input: { command: 'printf recorded' } },
      created_at: run.created_at,
    },
    {
      agent_run_id: 'run-1',
      attempt: 1,
      seq: 2,
      type: 'tool_result',
      tool: '',
      content: { tool_use_id: 'tool-1', content: 'Full recorded transcript.' },
      created_at: run.created_at,
    },
  ];
  const fetch = vi.fn(async (_path: string) => Response.json(messages));
  vi.stubGlobal('fetch', fetch);
  const cancelled: string[] = [];
  const root = node('root');
  const app = createTreeApp({
    render: () =>
      h(AgentRunCard, {
        run: { ...run, ...extra },
        study: 'Study',
        position: 1,
        studyId: 'c',
        runtime,
        replies,
        authorId: 'u',
        onCancelComment: (id: string) => cancelled.push(id),
      }),
  });
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(root);
  disposals.push(() => {
    app.unmount();
    client.clear();
  });
  const header = () =>
    all(root).find((n) => n.tag === 'button' && n.props['aria-expanded'] !== undefined)!;
  const clickHeader = async () => {
    expect(header(), textOf(root)).toBeTruthy();
    (header().props.onClick as () => void)();
    await nextTick();
  };
  return { root, fetch, cancelled, header, clickHeader };
}
it('lets the author remove a reply and uses the round send control', async () => {
  const item: Comment = {
    id: 'reply',
    workspace_id: 'w',
    card_id: 'c',
    author_id: 'u',
    body: 'Hello',
    created_at: run.created_at,
    delivery: null,
  };
  const { root, cancelled } = setup({}, null, [item]);
  await nextTick();
  const remove = all(root).find((n) => n.props['aria-label'] === 'Remove comment')!;
  expect(remove).toBeTruthy();
  (remove.props.onClick as () => void)();
  expect(cancelled).toEqual(['reply']);
  const send = all(root).find((n) => n.props['aria-label'] === 'Send reply to Ada')!;
  expect(String(send.props.class)).toContain('round-send');
});
it('does not offer removal for another member’s reply', async () => {
  const item: Comment = {
    id: 'reply',
    workspace_id: 'w',
    card_id: 'c',
    author_id: 'other',
    body: 'Hello',
    created_at: run.created_at,
    delivery: null,
  };
  const { root } = setup({}, null, [item]);
  await nextTick();
  expect(all(root).some((n) => n.props['aria-label'] === 'Remove comment')).toBe(false);
});
it('opens in place and shows the transcript', async () => {
  const { root, fetch, header, clickHeader } = setup();
  await nextTick();
  expect(textOf(root)).toContain('A three-line preview.');
  expect(textOf(root)).not.toContain('▸ show transcript');
  expect(header().props['aria-expanded']).toBe(false);
  expect(fetch).not.toHaveBeenCalled();
  expect(all(root).some((n) => n.props.class === 'agent-turn')).toBe(false);
  await clickHeader();
  expect(header()).toBeTruthy();
  expect(header().props['aria-expanded']).toBe(true);
  expect(all(root).some((n) => n.props.class === 'agent-turn')).toBe(true);
  await vi.waitFor(() => expect(textOf(root)).toContain('Full recorded transcript.'));
  expect(textOf(root)).toContain('Run finished');
  expect(fetch.mock.calls.map(([path]) => path)).toEqual([
    '/api/agent-runs/run-1/messages?after_attempt=0&after=0',
  ]);
  await clickHeader();
  expect(all(root).some((n) => n.props.class === 'agent-turn')).toBe(false);
});
it('adds a reaction to the run without opening its transcript', async () => {
  const { root, fetch, header } = setup();
  const control = all(root).find((n) => n.props['aria-label'] === 'Add run reaction')!;
  (control.props.onClick as () => void)();
  await nextTick();
  const emoji = all(root).find((n) => n.tag === 'button' && n.props['aria-label'] === '👍')!;
  (emoji.props.onClick as () => void)();
  await vi.waitFor(() => expect(fetch).toHaveBeenCalled());
  expect(fetch.mock.calls[0]?.[0]).toBe('/api/cards/c/runs/run-1/reactions');
  expect(header().props['aria-expanded']).toBe(false);
});
it.each(['running', 'dispatched'] as const)(
  'starts open while the run is working (%s)',
  async (status) => {
    const { root, header } = setup({ status });
    await nextTick();
    expect(header()).toBeTruthy();
    expect(header().props['aria-expanded']).toBe(true);
    expect(textOf(root)).toContain('Working…');
    expect(all(root).some((n) => String(n.props.class).includes('live-dot'))).toBe(true);
  },
);
it('shows the run environment when open', async () => {
  const { root, clickHeader } = setup({ machine_name: 'Old bench', machine_owner_id: 'u' });
  expect(textOf(root)).not.toContain('Old bench');
  await clickHeader();
  expect(textOf(root)).toContain('Old bench (Owner)');
  expect(textOf(root)).toContain('Claude Code');
  expect(textOf(root)).toContain('Recorded model');
  expect(textOf(root)).not.toContain('Runs on: Bench');
  expect(textOf(root)).toContain('The study machine needs attention.');
});
it('sends a reply from the card to that run', async () => {
  const { root, fetch } = setup();
  const field = all(root).find(
    (n) => n.tag === 'textarea' && n.props['aria-label'] === 'Reply to Ada',
  )!;
  expect(field).toBeTruthy();
  (field.props.onInput as (event: unknown) => void)({ target: { value: 'Please check this.' } });
  await nextTick();
  const form = all(root).find((n) => n.tag === 'form' && n.props['aria-label'] === 'Reply to Ada')!;
  (form.props.onSubmit as (event: { preventDefault(): void }) => void)({ preventDefault() {} });
  await vi.waitFor(() => expect(fetch).toHaveBeenCalled());
  const [path, options] = fetch.mock.calls.find(([url]) =>
    url.endsWith('/comments'),
  ) as unknown as [string, RequestInit];
  expect(path).toContain('/api/cards/c/comments');
  expect(JSON.parse(options.body as string)).toEqual({
    body: 'Please check this.',
    target_run_id: 'run-1',
  });
});

it('explains when a running Codex cannot receive live input', async () => {
  const { root } = setup({ status: 'running', harness: 'codex' });
  const field = all(root).find(
    (n) => n.tag === 'textarea' && n.props['aria-label'] === 'Reply to Ada',
  )!;
  (field.props.onInput as (event: unknown) => void)({ target: { value: 'Please check this.' } });
  await nextTick();
  expect(textOf(root)).toContain('Codex cannot receive replies during this run');
});
