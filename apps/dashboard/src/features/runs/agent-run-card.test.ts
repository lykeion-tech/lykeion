import { afterEach, expect, it, vi } from 'vitest';
import { createRenderer, defineComponent, h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import AgentRunCard from './AgentRunCard.vue';
vi.mock('../../ui/RecordedMarkdown.vue', async () => {
  const { h } = await import('vue');
  return {
    default: {
      props: ['content'],
      setup: (p: { content: string }) => () =>
        h('div', { class: 'recorded-markdown-stub' }, p.content),
    },
  };
});
vi.mock('../../ui/Modal.vue', async () => {
  const { h } = await import('vue');
  return {
    default: {
      setup:
        (_: unknown, { slots }: { slots: { default?: () => unknown } }) =>
        () =>
          h('div', { role: 'dialog' }, slots.default?.() as never),
    },
  };
});
import type { AgentDefinition, AgentRun, Comment, RunMessage, Runtime } from '../../api/types';
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
const visibleAgent: AgentDefinition = {
  id: 'a',
  workspace_id: 'w',
  name: 'Ada',
  avatar_url: 'emoji:🐸',
  description: '',
  instructions: '',
  harness: 'claude',
  model: '',
  visibility: 'workspace',
  owner_id: 'u',
  stage: '',
  laboratory: '',
  runtime_id: null,
  archived_at: null,
  created_at: '',
  updated_at: '',
};
function setup(
  extra: Partial<AgentRun> = {},
  runtime: Runtime | null = null,
  replies: Comment[] = [],
  overrides?: RunMessage[],
  agent: AgentDefinition | null = visibleAgent,
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
  const fetch = vi.fn(async (_path: string, _init?: RequestInit) =>
    Response.json(overrides ?? messages),
  );
  vi.stubGlobal('fetch', fetch);
  const cancelled: string[] = [];
  const root = node('root');
  const app = createTreeApp({
    render: () =>
      h(AgentRunCard, {
        run: { ...run, ...extra },
        agent,
        study: 'Study',
        position: 1,
        studyId: 'c',
        runtime,
        replies,
        authorId: 'u',
        onCancelComment: (id: string) => cancelled.push(id),
      }),
  });
  app.component(
    'RouterLink',
    defineComponent({
      props: { to: { type: String, required: true }, ariaLabel: String },
      setup:
        (props, { slots }) =>
        () =>
          h('a', { href: props.to, 'aria-label': props.ariaLabel }, slots.default?.()),
    }),
  );
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
it('links the agent avatar to its profile when the run has an agent', async () => {
  const { root } = setup();
  await nextTick();
  const link = all(root).find((n) => n.tag === 'a' && n.props.href === '/agents/a');
  expect(link?.props['aria-label']).toBe('Open Ada’s agent details');
});
it('does not link an avatar for a legacy or deleted agent', async () => {
  const { root } = setup({ agent_id: null });
  await nextTick();
  expect(all(root).some((n) => n.tag === 'a' && String(n.props.href).startsWith('/agents/'))).toBe(
    false,
  );
});
it('shows the configured agent avatar and does not link an inaccessible agent', async () => {
  const visible = setup();
  await nextTick();
  expect(textOf(visible.root)).toContain('🐸');
  const inaccessible = setup({}, null, [], undefined, null);
  await nextTick();
  expect(all(inaccessible.root).some((n) => n.tag === 'a' && n.props.href === '/agents/a')).toBe(
    false,
  );
  expect(textOf(inaccessible.root)).toContain('Ada');
  const wrongAgent = setup({}, null, [], undefined, { ...visibleAgent, id: 'other' });
  await nextTick();
  expect(all(wrongAgent.root).some((n) => n.tag === 'a' && n.props.href === '/agents/a')).toBe(
    false,
  );
});
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
it('explains that a saved file reply awaits the same agent even after its run ends', async () => {
  const item: Comment = {
    id: 'file-reply',
    workspace_id: 'w',
    card_id: 'c',
    author_id: 'u',
    body: '',
    created_at: run.created_at,
    delivery: null,
    attachments: [{ id: 'a', name: 'photo.png', content_type: 'image/png', size_bytes: 2 }],
  };
  const { root } = setup({ status: 'completed' }, null, [item]);
  await nextTick();
  expect(textOf(root)).toContain('files will reach Ada only if Ada has a later eligible run');
  expect(textOf(root)).toContain('this reply does not start one');
});
it('keeps the final answer visible and opens its transcript separately', async () => {
  const { root, fetch } = setup();
  await nextTick();
  expect(textOf(root)).toContain('A **three-line** preview.');
  expect(all(root).some((n) => n.props.class === 'agent-turn')).toBe(false);
  const log = all(root).find((n) => n.props['aria-label'] === 'Open full log')!;
  (log.props.onClick as () => void)();
  await vi.waitFor(() => expect(textOf(root)).toContain('Full recorded transcript.'));
  expect(textOf(root)).toContain('Run finished');
  expect(fetch.mock.calls.map(([path]) => path)).toEqual([
    '/api/agent-runs/run-1/messages?after_attempt=0&after=0',
  ]);
  const close = all(root).find((n) => n.props['aria-label'] === 'Close full log')!;
  (close.props.onClick as () => void)();
  await nextTick();
  expect(all(root).some((n) => n.props.class === 'agent-turn')).toBe(false);
});
it('adds a reaction without opening the full log', async () => {
  const { root, fetch } = setup();
  const control = all(root).find((n) => n.props['aria-label'] === 'Add run reaction')!;
  (control.props.onClick as () => void)();
  await nextTick();
  const emoji = all(root).find((n) => n.tag === 'button' && n.props['aria-label'] === '👍')!;
  (emoji.props.onClick as () => void)();
  await vi.waitFor(() => expect(fetch).toHaveBeenCalled());
  expect(fetch.mock.calls[0]?.[0]).toBe('/api/cards/c/runs/run-1/reactions');
  expect(all(root).some((n) => n.props.class === 'agent-turn')).toBe(false);
});
it.each(['running', 'dispatched'] as const)(
  'shows a working indicator while the run is %s',
  async (status) => {
    const { root } = setup({ status });
    await nextTick();
    expect(textOf(root)).toContain('Working…');
    expect(all(root).some((n) => String(n.props.class).includes('live-dot'))).toBe(true);
    expect(all(root).some((n) => String(n.props.class).includes('run-status-spinner'))).toBe(true);
  },
);
it.each(['queued', 'completed', 'failed', 'cancelled'] as const)(
  'does not show a working spinner when the run is %s',
  async (status) => {
    const { root } = setup({ status });
    await nextTick();
    expect(all(root).some((n) => String(n.props.class).includes('run-status-spinner'))).toBe(false);
  },
);
it('omits the run environment while retaining machine notices', async () => {
  const { root } = setup({ machine_name: 'Old bench', machine_owner_id: 'u' });
  await nextTick();
  expect(textOf(root)).not.toContain('Old bench (Owner)');
  expect(all(root).some((n) => n.props['aria-label'] === 'Run environment')).toBe(false);
  expect(textOf(root)).not.toContain('Runs on: Bench');
  expect(textOf(root)).toContain('The study machine needs attention.');
});
it('sends a reply as a comment without starting a follow-up run', async () => {
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
  expect(
    fetch.mock.calls.filter(([, init]) => init?.method === 'POST').map(([url]) => url),
  ).toEqual(['/api/cards/c/comments']);
  expect(textOf(root)).not.toContain('Waiting for an eligible agent run');
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

it('does not treat a truncated interim excerpt as the final answer', async () => {
  const { root } = setup({
    answer: '',
    status: 'cancelled',
    latest_text: '{"answer":"A useful finding.\\n\\nAnother incomplete',
  });
  await nextTick();
  expect(textOf(root)).not.toContain('A useful finding. Another incomplete');
  expect(textOf(root)).not.toContain('{"answer"');
});

it('does not expose a structured excerpt when its answer is not available', async () => {
  const { root } = setup({
    answer: '',
    status: 'cancelled',
    latest_text: '{"status":"done","papers":[',
  });
  await nextTick();
  expect(textOf(root)).not.toContain('{"status"');
});

it('shows a final report separately from its full log and offers per-run stop', async () => {
  const { root } = setup({
    status: 'completed',
    answer: 'Final **answer** with [source](https://example.org).',
  });
  await nextTick();
  expect(all(root).some((n) => n.props['aria-label'] === 'Open full log')).toBe(true);
  expect(all(root).some((n) => n.props.class === 'agent-turn')).toBe(false);
  const log = all(root).find((n) => n.props['aria-label'] === 'Open full log')!;
  (log.props.onClick as () => void)();
  await nextTick();
  expect(all(root).some((n) => n.props.class === 'agent-turn')).toBe(true);
  const running = setup({ id: 'active-run', status: 'running' });
  await nextTick();
  expect(all(running.root).some((n) => n.props['aria-label'] === 'Stop Ada run')).toBe(true);
});

it('cancels only the selected active run from its card', async () => {
  const { root, fetch } = setup({ id: 'run-1', status: 'running' });
  const stop = all(root).find((n) => n.props['aria-label'] === 'Stop Ada run')!;
  expect(stop).toBeTruthy();
  (stop.props.onClick as () => void)();
  await vi.waitFor(() =>
    expect(
      fetch.mock.calls.some(
        ([path, init]) => path === '/api/agent-runs/run-1/cancel' && init?.method === 'POST',
      ),
    ).toBe(true),
  );
  expect(fetch.mock.calls.some(([path]) => path.includes('/cards/c/stop'))).toBe(false);
});

it('allows a file-only reply attached to this run', async () => {
  class UploadRequest {
    upload: { onprogress?: (e: unknown) => void } = {};
    status = 200;
    responseText = JSON.stringify({ id: 'attachment-1', name: 'photo.png', size_bytes: 2 });
    onload?: () => void;
    open() {}
    setRequestHeader() {}
    send() {
      queueMicrotask(() => this.onload?.());
    }
  }
  vi.stubGlobal('XMLHttpRequest', UploadRequest);
  const { root, fetch } = setup();
  const picker = all(root).find((n) => n.tag === 'input' && n.props.type === 'file')!;
  expect(picker).toBeTruthy();
  (picker.props.onChange as (e: unknown) => void)({
    target: { files: [new File(['hi'], 'photo.png')], value: '' },
  });
  await vi.waitFor(() => expect(textOf(root)).toContain('photo.png'));
  const form = all(root).find((n) => n.tag === 'form' && n.props['aria-label'] === 'Reply to Ada')!;
  (form.props.onSubmit as (e: { preventDefault(): void }) => void)({ preventDefault() {} });
  await vi.waitFor(() =>
    expect(fetch.mock.calls.some(([url]) => url === '/api/cards/c/comments')).toBe(true),
  );
  const [, init] = fetch.mock.calls.find(([url]) => url === '/api/cards/c/comments')!;
  expect(JSON.parse(String(init?.body))).toEqual({
    body: '',
    target_run_id: 'run-1',
    attachment_ids: ['attachment-1'],
  });
});

it('does not send a reply while a file upload is pending and allows removal', async () => {
  class PendingUpload {
    upload = {};
    open() {}
    setRequestHeader() {}
    send() {}
  }
  vi.stubGlobal('XMLHttpRequest', PendingUpload);
  const { root, fetch } = setup();
  const picker = all(root).find((n) => n.tag === 'input' && n.props.type === 'file')!;
  (picker.props.onChange as (e: unknown) => void)({
    target: { files: [new File(['hi'], 'pending.png')], value: '' },
  });
  await nextTick();
  const send = all(root).find((n) => n.props['aria-label'] === 'Send reply to Ada')!;
  expect(send.props.disabled).toBe(true);
  const form = all(root).find((n) => n.tag === 'form' && n.props['aria-label'] === 'Reply to Ada')!;
  (form.props.onSubmit as (e: { preventDefault(): void }) => void)({ preventDefault() {} });
  expect(fetch.mock.calls.some(([url]) => url === '/api/cards/c/comments')).toBe(false);
  const remove = all(root).find((n) => n.props['aria-label'] === 'Remove pending.png')!;
  (remove.props.onClick as () => void)();
  await nextTick();
  expect(textOf(root)).not.toContain('pending.png');
});

it('offers stop for a queued run without stopping the study', async () => {
  const { root } = setup({ status: 'queued' });
  expect(all(root).some((n) => n.props['aria-label'] === 'Stop Ada run')).toBe(true);
});

it('does not label an interim text as the final message when no accepted answer exists', async () => {
  const { root } = setup({ status: 'completed', answer: '', latest_text: 'intermediate thought' });
  await vi.waitFor(() => expect(textOf(root)).toContain('Final message unavailable'));
  expect(textOf(root)).not.toContain('Full recorded transcript.');
  expect(textOf(root)).not.toContain('intermediate thought');
});

it('keeps the answer expanded with no model name or fold control', async () => {
  const { root } = setup({ answer: 'Final **answer**', model: 'Secret model' });
  await nextTick();
  expect(textOf(root)).toContain('Final **answer**');
  expect(textOf(root)).not.toContain('Secret model');
  expect(
    all(root).some(
      (n) =>
        n.props['aria-expanded'] !== undefined && String(n.props['aria-label']).includes('Run 1'),
    ),
  ).toBe(false);
});

function textMessage(
  seq: number,
  type: RunMessage['type'],
  tool: string,
  content: unknown,
): RunMessage {
  return {
    agent_run_id: 'run-1',
    attempt: 1,
    seq,
    type,
    tool,
    content,
    created_at: run.created_at,
  };
}
it('shows an explicit terminal output marker instead of earlier text', async () => {
  const { root } = setup(
    { answer: '', harness: 'claude' },
    null,
    [],
    [
      textMessage(1, 'text', '', 'interim'),
      textMessage(2, 'status', 'lykeion.final_output', { text: 'Authoritative terminal result' }),
    ],
  );
  await vi.waitFor(() => expect(textOf(root)).toContain('Authoritative terminal result'));
  expect(textOf(root)).not.toContain('interim');
});
it('recovers old completed Codex final text but not Claude interim text', async () => {
  const messages = [
    textMessage(1, 'text', '', 'first'),
    textMessage(2, 'text', '', 'actual Codex final'),
  ];
  const codex = setup({ answer: '', harness: 'codex' }, null, [], messages);
  await vi.waitFor(() => expect(textOf(codex.root)).toContain('actual Codex final'));
  const claude = setup({ answer: '', harness: 'claude' }, null, [], messages);
  await vi.waitFor(() => expect(textOf(claude.root)).toContain('Final message unavailable'));
  expect(textOf(claude.root)).not.toContain('actual Codex final');
});
