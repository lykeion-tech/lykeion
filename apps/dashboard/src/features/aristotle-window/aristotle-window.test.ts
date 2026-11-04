import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createMemoryHistory, createRouter } from 'vue-router';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import { api } from '../../api/client';
import { labRecordTitle, labKey } from './api';
import type { Card, StudyRecord } from '../../api/types';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import AristotleWindow from './AristotleWindow.vue';
import LabAnswer from './LabAnswer.vue';

vi.mock('../coresearch/Aristotle.vue', () => ({ default: { render: () => null } }));
const cleanups: (() => void)[] = [];
afterEach(() => {
  cleanups.splice(0).forEach((fn) => fn());
  vi.unstubAllGlobals();
});
const settle = async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
};
const starter = 'What has the lab already decided about this study?';
const fact = { ref: 'study:s', title: 'Lab study', study_id: 's' };
const answer = {
  id: 'a',
  kind: 'answer',
  body: '',
  evidence: [{ ...fact, via: 'search' }],
  statements: [
    { kind: 'fact', text: 'Measured.', cites: [fact] },
    {
      kind: 'lesson',
      text: 'Repeat.',
      cites: [{ ref: 'lesson:l', title: 'Replication', facts: [fact] }],
    },
    { kind: 'gap', text: 'No record.', cites: [] },
  ],
};
async function mount(onlyAnswer = false, offline = false, pages?: (path: string) => unknown, extra: Record<string, unknown> = {}) {
  vi.stubGlobal('window', {
    innerWidth: 1000,
    innerHeight: 800,
    addEventListener() {},
    removeEventListener() {},
  });
  vi.stubGlobal('document', { addEventListener() {}, removeEventListener() {} });
  workspaceId.value = 'lab';
  const calls: { path: string; method: string; body: any }[] = [];
  vi.stubGlobal('fetch', async (path: string, opts: RequestInit) => {
    const body = opts.body && JSON.parse(String(opts.body));
    calls.push({ path, method: opts.method!, body });
    const denied = offline && path.endsWith('/questions');
    const data =
      (opts.method === 'GET' && pages ? pages(path) : undefined) ??
      (denied
        ? { code: 'no_machine', error: 'Your machine is offline' }
        : opts.method === 'POST'
          ? {
              chat: { id: 'c', title: 'New chat' },
              message: { id: 'q', kind: 'question', body: starter },
            }
          : path.includes('/messages')
            ? { messages: [], next_cursor: '' }
            : { chats: [], next_cursor: '' });
    return new Response(JSON.stringify(data), { status: denied ? 409 : 200 });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  });
  client.setQueryData(keys.cards('lab'), [{ id: 's', title: 'Lab study' }]);
  client.setQueryData(keys.runtimes('lab'), [
    { owner_id: 'me', harness: 'claude', status: 'online' },
  ]);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/studies/:cardId', component: { render: () => null } },
      { path: '/machines', component: { render: () => null } },
    ],
  });
  await router.push('/studies/s');
  const submitManualQuestion = extra.draftChat ? (turn: unknown) => api('POST', `/api/lab/chats/${extra.draftChat}/questions`, turn, undefined, { workspace: 'lab' }) : undefined;
  const app = createTreeApp({
    render: () =>
      onlyAnswer
        ? h(LabAnswer, { message: answer, evidence: true })
        : h(AristotleWindow, { userId: 'me', studyId: 's', studyTitle: 'Lab study', submitManualQuestion, ...extra }),
  });
  app.use(VueQueryPlugin, { queryClient: client });
  app.use(router);
  const root = node('root');
  app.mount(root);
  cleanups.push(() => {
    app.unmount();
    client.clear();
  });
  const find = (label: string) =>
    all(root).find(
      (n) => n.tag === 'button' && (n.props['aria-label'] === label || textOf(n).trim() === label),
    );
  const click = async (label: string) => {
    const btn = find(label)!;
    (btn.props.onClick as Function)({ currentTarget: btn });
    await settle();
  };
  const composer = () => all(root).find((n) => n.props.placeholder === 'Ask Aristotle')!;
  await settle();
  const tryDraft = async (question: string) => {
    (composer().props.onInput as Function)({ target: { value: question } });
    await settle();
    const form = all(root).find((n) => n.tag === 'form')!;
    (form.props.onSubmit as Function)({ preventDefault() {} });
    await settle();
  };
  return { root, click, calls, composer, client, tryDraft };
}
it('opens and minimizes, suggests removable study context and shows starters', async () => {
  const t = await mount();
  await t.click('Ask Aristotle');
  expect(textOf(t.root)).toContain('This study: Lab study');
  await t.click('Remove context');
  expect(textOf(t.root)).not.toContain('This study:');
  expect(textOf(t.root)).not.toContain('Ask the lab');
  (t.composer().props.onFocus as Function)();
  await settle();
  expect(all(t.root).filter((n) => n.tag === 'button' && textOf(n).endsWith('?'))).toHaveLength(4);
  expect(t.calls.some((c) => c.method === 'POST')).toBe(false);
  expect(textOf(t.root)).not.toContain('Start voice chat');
  await t.click('Minimize');
  expect(t.composer()).toBeUndefined();
});
it('preserves the draft when the machine is offline', async () => {
  const t = await mount(false, true);
  await t.click('Ask Aristotle');
  (t.composer().props.onFocus as Function)();
  await settle();
  await t.tryDraft(starter);
  expect(t.calls.some((c) => c.path.endsWith('/questions'))).toBe(true);
  expect(textOf(t.root)).toContain('Your machine is offline');
  expect(t.composer().props.value ?? t.composer().value).toBe(starter);
  expect(all(t.root).find((n) => n.props.href === '/machines')).toBeDefined();
});
it('renders marked statements, links, evidence paths and flags a lesson', async () => {
  const t = await mount(true);
  for (const s of ['Lesson', 'No record.', 'Replication', 'Evidence'])
    expect(textOf(t.root)).toContain(s);
  expect(textOf(t.root)).not.toContain('via search');
  expect(all(t.root).some((n) => n.tag === 'span' && textOf(n) === 'Fact')).toBe(false);
  expect(all(t.root).some((n) => n.props.href === '/studies/s')).toBe(true);
  await t.click('Flag lesson');
  await t.click('Submit flag');
  expect(t.calls.find((c) => c.path === '/api/lessons/l/flag')?.body).toEqual({ reason: '' });
});

it('follows cursors for older chats and messages and replaces revoked answers on refresh', async () => {
  let revoked = false;
  const pages = (path: string) => {
    if (path === '/api/lab/chats')
      return { chats: [{ id: 'c', title: 'First' }], next_cursor: 'chat-token' };
    if (path.endsWith('?cursor=chat-token'))
      return { chats: [{ id: 'older', title: 'Second' }], next_cursor: '' };
    if (path.endsWith('?cursor=msg-token'))
      return {
        messages: [{ id: 'q', kind: 'question', body: 'Earlier question' }],
        next_cursor: '',
      };
    if (path.includes('/messages'))
      return {
        messages: [
          revoked
            ? {
                id: 'a',
                kind: 'failure',
                body: 'Answer unavailable.',
                statements: [],
                evidence: [],
              }
            : answer,
        ],
        next_cursor: 'msg-token',
      };
  };
  const t = await mount(false, false, pages);
  await t.click('Ask Aristotle');
  await t.click('Switch chat');
  await t.click('More chats');
  await t.click('Second');
  expect(textOf(t.root)).toContain('Measured.');
  await t.click('Earlier messages');
  expect(textOf(t.root)).toContain('Earlier question');
  revoked = true;
  await t.client.invalidateQueries({ queryKey: labKey('lab') });
  await settle();
  expect(textOf(t.root)).toContain('Answer unavailable.');
  expect(textOf(t.root)).not.toContain('Measured.');
  expect(textOf(t.root)).not.toContain('Replication');
});
it('uses the owner Codex runtime without exposing other members models', async () => {
  const t = await mount();
  t.client.setQueryData(keys.runtimes('lab'), [
    { owner_id: 'me', harness: 'codex', status: 'online' },
    {
      owner_id: 'another',
      harness: 'claude',
      status: 'online',
      models: [{ value: 'private-model', label: 'Private model' }],
    },
  ]);
  await t.click('Ask Aristotle');
  await t.click('Select model');
  expect(textOf(t.root)).toContain('Codex · Default');
  expect(textOf(t.root)).not.toContain('Private model');
  await t.click('Select model');
  (t.composer().props.onFocus as Function)();
  await settle();
  await t.tryDraft(starter);
  expect(t.calls.find((c) => c.path.endsWith('/questions'))?.body.harness).toBe('codex');
});
it('offers canonical text and binary file records and excludes detached or mismatched attachments', () => {
  const card = { attachments: [{ id: 'file', record_key: 'c1', name: 'Protocol' }] } as Card;
  const row: StudyRecord = {
    id: 'r',
    card_id: 's',
    board_column: 'grounding',
    round: 0,
    role: 'server',
    agent_run_id: null,
    superseded_at: null,
    created_at: '2026-11-03',
    key: 'c1',
    kind: 'call',
    body: { server: 'attachments' },
    blob_key: '',
  };
  expect(labRecordTitle(row, card, 'lab')).toBe('File · Protocol');
  expect(labRecordTitle({ ...row, blob_key: 'lab/attachments/file.text' }, card, 'lab')).toBe(
    'File · Protocol',
  );
  expect(
    labRecordTitle({ ...row, blob_key: 'lab/coresearch-uploads/file.text' }, card, 'lab'),
  ).toBe('');
  expect(labRecordTitle({ ...row, key: 'c2' }, card, 'lab')).toBe('');
});

it('keeps waiting for the second question when the first fails out of order', async () => {
  const t = await mount(false, false, (path) =>
    path.includes('/messages')
      ? {
          messages: [
            { id: 'q1', kind: 'question', body: 'First' },
            { id: 'q2', kind: 'question', body: 'Second' },
            { id: 'f1', kind: 'failure', body: 'First failed' },
          ],
          next_cursor: '',
          pending: true,
        }
      : { chats: [{ id: 'chat', title: 'Overlapping questions' }], next_cursor: '' },
  );
  await t.click('Ask Aristotle');
  await t.click('Switch chat');
  await t.click('Overlapping questions');
  expect(textOf(t.root)).toContain('Reading the lab’s records');
  expect(textOf(t.root)).not.toContain('Retry question');
});

it('places new chat in the menu header and deletes the selected chat', async () => {
  const pages = (path: string) => path.includes('/messages')
    ? { messages: [{ id: 'old', kind: 'question', body: 'Previous question', statements: [], evidence: [] }], next_cursor: '' }
    : { chats: [{ id: 'c', title: 'Previous chat' }], next_cursor: '' };
  const t = await mount(false, false, pages);
  await t.click('Ask Aristotle');
  await t.click('Switch chat');
  expect(all(t.root).some((n) => n.tag === 'button' && n.props['aria-label'] === 'New chat')).toBe(true);
  await t.click('Previous chat');
  expect(textOf(t.root)).toContain('Previous question');
  await t.click('Switch chat');
  await t.click('Delete Previous chat');
  expect(t.calls.find((c) => c.method === 'DELETE')?.path).toBe('/api/lab/chats/c');
  expect(textOf(t.root)).not.toContain('Previous question');
});

it('does not send a question while chat deletion is pending', async () => {
  const pages = (path: string) => path.includes('/messages')
    ? { messages: [], next_cursor: '' }
    : { chats: [{ id: 'c', title: 'Previous chat' }], next_cursor: '' };
  const t = await mount(false, false, pages);
  await t.click('Ask Aristotle');
  await t.click('Switch chat');
  await t.click('Previous chat');
  let finish!: () => void;
  const pending = new Promise<void>((resolve) => { finish = resolve; });
  const originalFetch = globalThis.fetch;
  vi.stubGlobal('fetch', async (path: string, opts: RequestInit) => {
    if (opts.method === 'DELETE') await pending;
    return originalFetch(path, opts);
  });
  await t.click('Switch chat');
  await t.click('Delete Previous chat');
  await t.tryDraft('A question during deletion');
  expect(t.calls.some((c) => c.path.endsWith('/questions'))).toBe(false);
  finish();
  await settle();
  expect(textOf(t.root)).not.toContain('Previous chat');
});


const manual = { title: 'Human title', context: '', deadline: null, card_id: null };
it('saves manual edits before a purpose-specific turn and preserves them offline', async () => {
  const save = vi.fn((turn: unknown) => api('POST', '/api/lab/chats/draft/questions', turn, undefined, { workspace: 'lab' }));
  const t = await mount(false, true, undefined, { draftChat: 'draft', manualDraft: manual, submitManualQuestion: save });
  await t.tryDraft('Refine this note');
  expect(save).toHaveBeenCalledOnce();
  const post = t.calls.find(c => c.path === '/api/lab/chats/draft/questions');
  expect(post?.body).toMatchObject({ user_request: 'Refine this note', current_draft: manual, harness: 'claude' });
  expect(post?.body.request_id).toMatch(/^[a-f0-9-]{36}$/);
  expect(textOf(t.root)).toContain('Your draft is saved');
  expect(t.composer().props.value).toBe('Refine this note');
  expect(t.calls.some(c => c.path === '/api/decisions/manual')).toBe(false);
  expect(t.calls.some(c => c.path === '/api/lab/chats' && c.method === 'GET')).toBe(false);
});
it('accepts only valid final proposals and never parses streamed or unknown output', async () => {
  const onManualProposal = vi.fn();
  const question = { id: 'q', kind: 'question', body: 'Draft please', current_draft: manual, statements: [], evidence: [] };
  let proposal: unknown = { message: 'Proposal', draft: { ...manual, title: 'Suggested', unknown: true } };
  const pages = (path: string) => path.includes('/messages') ? { messages: [question, { id: 'a1', kind: 'answer', body: '{title: stream}', proposal, statements: [], evidence: [] }], next_cursor: '', pending: false } : undefined;
  const t = await mount(false, false, pages, { draftChat: 'draft', manualDraft: manual,  onManualProposal });
  expect(onManualProposal).not.toHaveBeenCalled();
  expect(textOf(t.root)).not.toContain('{title: stream}');
  proposal = { message: 'A suggestion', draft: { ...manual, title: 'Suggested' } };
  // A real completion has a new immutable message identity.
  vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ messages: [question, { id: 'a2', kind: 'answer', body: '', proposal, statements: [], evidence: [] }], pending: false, next_cursor: '' })));
  await t.client.invalidateQueries({ queryKey: labKey('lab') }); await settle();
  expect(onManualProposal).toHaveBeenCalledWith({ draft: { ...manual, title: 'Suggested' }, input: manual });
  expect(textOf(t.root)).toContain('Proposal');
});
it('stops a draft question without restoring composer text and permits a fresh turn', async () => {
  let pending = true;
  const t = await mount(false, false, path => path.includes('/messages') ? { messages: [{ id: 'q', kind: 'question', body: 'Previous question', current_draft: manual, statements: [], evidence: [] }], next_cursor: '', pending } : undefined, { draftChat: 'draft', manualDraft: manual,  });
  pending = false;
  await t.click('Stop drafting');
  expect(t.calls.some(c => c.path === '/api/lab/chats/draft/questions/q/cancel')).toBe(true);
  expect(t.composer().props.value).toBe('');
  expect(textOf(t.root)).toContain('Previous question');
  await t.tryDraft('A fresh question');
  expect(t.calls.some(c => c.path.endsWith('/questions') && c.body.user_request === 'A fresh question')).toBe(true);
});


it('does not replay an old proposal after the person cleared and saved its context then reloaded', async () => {
  let current = { ...manual };
  let messages: unknown[] = [];
  const autosave = vi.fn((value: any) => { current = value.draft; });
  const pages = (path: string) => path.includes('/messages') ? { messages, pending: false, next_cursor: '' } : undefined;
  const first = await mount(false, false, pages, { draftChat: 'draft', manualDraft: current,  onManualProposal: autosave });
  messages = [
    { id: 'q-old', kind: 'question', body: 'Help', current_draft: manual, statements: [], evidence: [] },
    { id: 'a-old', kind: 'answer', body: '', proposal: { message: 'Proposed context', draft: { ...manual, context: 'AI context' } }, statements: [], evidence: [] },
  ];
  await first.client.invalidateQueries({ queryKey: labKey('lab') }); await settle();
  expect(current.context).toBe('AI context');
  current = { ...current, context: '' }; autosave.mockClear();
  await mount(false, false, pages, { draftChat: 'draft', manualDraft: current,  onManualProposal: autosave });
  expect(autosave).not.toHaveBeenCalled();
  expect(current.context).toBe('');
});
