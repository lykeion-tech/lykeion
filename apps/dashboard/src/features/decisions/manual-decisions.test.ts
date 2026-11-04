import { ref, nextTick, h } from 'vue';
import { afterEach, expect, it, vi } from 'vitest';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createRouter, createMemoryHistory } from 'vue-router';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import ManualDecisionForm from './ManualDecisionForm.vue';
import ManualDecisionCreate from './ManualDecisionCreate.vue';
import ManualDecisionControls from './ManualDecisionControls.vue';
import { workspaceId } from '../../api/session';
import { blankManualDecisionDraft } from './manualDecisionDraft';
import { labKey } from '../aristotle-window/api';
const api = vi.fn();
const me = ref({ id: 'me', timezone: 'Europe/Copenhagen' });
const cards = ref([{ id: '11111111-1111-1111-1111-111111111111', title: 'Viewer study' }]);
vi.mock('../../api/client', () => ({ api: (...args: unknown[]) => api(...args) }));
vi.mock('../../api/queries', () => ({
  useMe: () => ({ data: me }),
  useCards: () => ({ data: cards }),
}));
vi.mock('../aristotle-window/AristotleWindow.vue', () => ({
  default: {
    props: ['manualDraft', 'submitManualQuestion', 'appliedMessageId'],
    emits: ['manualProposal'],
    setup:
      (props: any, { emit }: any) =>
      () =>
        h('div', { 'data-applied-id': props.appliedMessageId }, [
          h(
            'button',
            {
              'aria-label': 'Test ask',
              onClick: async () => {
                const turn = {
                  user_request: 'Refine',
                  current_draft: { ...props.manualDraft },
                  harness: 'claude',
                  model: '',
                  request_id: '11111111-1111-1111-1111-111111111111',
                };
                return props.submitManualQuestion(turn);
              },
            },
            'Ask',
          ),
          h(
            'button',
            {
              'aria-label': 'Test final proposal',
              onClick: () =>
                emit('manualProposal', {
                  messageId: '22222222-2222-2222-2222-222222222222',
                  draft: { ...props.manualDraft, title: 'AI title' },
                  input: props.manualDraft,
                }),
            },
            'Aristotle',
          ),
        ]),
  },
}));
const cleanups: (() => void)[] = [];
const settle = async () => {
  await new Promise((r) => setTimeout(r, 0));
  await nextTick();
};
afterEach(() => {
  cleanups.splice(0).forEach((f) => f());
  api.mockReset();
  workspaceId.value = 'w1';
  me.value = { id: 'me', timezone: 'Europe/Copenhagen' };
});
async function mount(component: any, props: any = {}, path = '/decisions/new') {
  workspaceId.value = 'w1';
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/decisions/:id?', component: { render: () => null } }],
  });
  await router.push(path);
  const root = node('root'),
    app = createTreeApp(component, props);
  app.use(VueQueryPlugin, { queryClient: client });
  app.use(router);
  app.mount(root);
  cleanups.push(() => {
    app.unmount();
    client.clear();
  });
  await settle();
  return { root, app, client, router };
}
const button = (root: ReturnType<typeof node>, label: string) =>
  all(root).find(
    (n) => n.tag === 'button' && (textOf(n).trim() === label || n.props['aria-label'] === label),
  )!;
const click = async (root: ReturnType<typeof node>, label: string) => {
  await (button(root, label).props.onClick as Function)();
  await settle();
};
function backend(initial = blankManualDecisionDraft()) {
  let draft = initial;
  let marker: string | null = null;
  api.mockImplementation(async (method: string, path: string, body: any) => {
    if (path.endsWith('/draft')) {
      if (method === 'PUT') {
        const { applied_message_id, ...fields } = body;
        draft = fields;
        if (applied_message_id !== undefined) marker = applied_message_id;
      }
      return { chat_id: 'd1', purpose: 'manual_decision_draft', draft, applied_message_id: marker };
    }
    if (path === '/api/lab/chats' && method === 'POST') return { chat: { id: 'd1' } };
    if (path === '/api/decisions/manual') return { id: 'note1' };
    return { chats: [{ id: 'd1', title: 'Saved draft' }] };
  });
}
it('offers both starting choices without creating a note', async () => {
  backend();
  const { root } = await mount(ManualDecisionCreate);
  expect(textOf(root)).toContain('Start blank');
  expect(textOf(root)).toContain('Create with AI');
  expect(api.mock.calls.some((c) => c[0] === 'POST')).toBe(false);
});
it.each(['Start blank', 'Create with AI'])(
  'persists a purpose-bound draft for %s without creating a note',
  async (label) => {
    backend();
    const { root, router } = await mount(ManualDecisionCreate);
    const choice = all(root).find((n) => n.tag === 'button' && textOf(n).includes(label))!;
    await (choice.props.onClick as Function)();
    await settle();
    expect(api).toHaveBeenCalledWith(
      'POST',
      '/api/lab/chats',
      { purpose: 'manual_decision_draft' },
      undefined,
      { workspace: 'w1' },
    );
    expect(router.currentRoute.value.query.draft).toBe('d1');
    expect(textOf(root)).toContain('Save a decision');
    expect(all(root).find((n) => n.props['aria-label'] === 'Back to starting options')?.props.href).toBe('/decisions/new');
    expect(api.mock.calls.some((c) => c[1] === '/api/decisions/manual')).toBe(false);
  },
);
it('has only the editable note fields, authorized studies and explicit submission', async () => {
  const submit = vi.fn();
  const { root } = await mount(ManualDecisionForm, {
    modelValue: { title: 'Reviewed title', context: '', deadline: null, card_id: null },
    onSubmit: submit,
  });
  expect(all(root).filter((n) => ['input', 'textarea', 'select'].includes(n.tag)).length).toBe(4);
  expect(textOf(root)).toContain('Viewer study');
  expect(submit).not.toHaveBeenCalled();
  await (all(root).find((n) => n.tag === 'form')!.props.onSubmit as Function)({
    preventDefault() {},
  });
  expect(submit).toHaveBeenCalled();
});
it.each(['', '&mode=ai'])(
  'restores saved blank and AI drafts, saves edits, submits only reviewed values: %s',
  async (mode) => {
    backend({ title: 'Recovered', context: 'Details', deadline: '2099-11-04', card_id: null });
    const { root } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1' + mode);
    const input = all(root).find((n) => n.props['aria-label'] === 'Title')!;
    expect(input.props.value).toBe('Recovered');
    await (input.props.onInput as Function)({ target: { value: 'My reviewed edit' } });
    await settle();
    expect(api.mock.calls.some((c) => c[0] === 'PUT' && c[2].title === 'My reviewed edit')).toBe(
      true,
    );
    expect(api.mock.calls.some((c) => c[1] === '/api/decisions/manual')).toBe(false);
    await (all(root).find((n) => n.tag === 'form')!.props.onSubmit as Function)({
      preventDefault() {},
    });
    await settle();
    expect(api).toHaveBeenCalledWith(
      'POST',
      '/api/decisions/manual',
      { title: 'My reviewed edit', context: 'Details', deadline: '2099-11-04', card_id: null },
      undefined,
      { workspace: 'w1' },
    );
  },
);
it('merges a final proposal without creating a note', async () => {
  backend({ ...blankManualDecisionDraft(), title: 'Original' });
  const { root } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1&mode=ai');
  await click(root, 'Test final proposal');
  expect(all(root).find((n) => n.props['aria-label'] === 'Title')?.props.value).toBe('AI title');
  expect(api.mock.calls.some((c) => c[1] === '/api/decisions/manual')).toBe(false);
});
it('hides the form when authorization refresh fails', async () => {
  backend({ ...blankManualDecisionDraft(), title: 'Private' });
  const { root, client } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1');
  api.mockRejectedValue(Object.assign(new Error('Not found'), { status: 404 }));
  await client.invalidateQueries({ queryKey: labKey('w1') });
  await settle();
  expect(all(root).some((n) => n.props.value === 'Private')).toBe(false);
  expect(textOf(root)).toContain('unavailable');
});
it('clears form state immediately on workspace and owner change', async () => {
  backend({ ...blankManualDecisionDraft(), title: 'Private' });
  const { root } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1');
  api.mockImplementation(() => new Promise(() => {}));
  workspaceId.value = 'w2';
  me.value = { ...me.value, id: 'someone-else' };
  await nextTick();
  expect(all(root).some((n) => n.props.value === 'Private')).toBe(false);
});
it('shows private manual actions without answer choices and uses triage endpoints', async () => {
  backend();
  const item = {
    id: 'note1',
    workspace_id: 'w1',
    user_id: 'me',
    kind: 'manual_decision',
    title: 'Private note',
    context: '',
    deadline: '2020-01-01',
    status: 'open',
    can_edit: true,
    can_resolve: true,
    can_dismiss: true,
    kept: false,
    card_id: null,
  };
  const { root } = await mount(ManualDecisionControls, { item });
  expect(textOf(root)).toContain('Overdue');
  expect(textOf(root)).not.toContain('Expires');
  expect(textOf(root)).not.toContain('Decision options');
  await click(root, 'Keep');
  await click(root, 'Archive');
  await click(root, 'Resolve');
  for (const action of ['keep', 'archive', 'resolve'])
    expect(api.mock.calls.some((c) => c[1] === '/api/decisions/note1/' + action)).toBe(true);
});
it('keeps terminal history and revive without reopening lifecycle', async () => {
  backend();
  const { root } = await mount(ManualDecisionControls, {
    item: {
      id: 'note1',
      workspace_id: 'w1',
      user_id: 'me',
      status: 'dismissed',
      archived_at: '2026-01-01',
      deadline: null,
    },
  });
  expect(button(root, 'Resolve')).toBeUndefined();
  expect(button(root, 'Edit')).toBeUndefined();
  await click(root, 'Restore');
  expect(api.mock.calls.some((c) => c[1] === '/api/decisions/note1/revive')).toBe(true);
});
it('can retry an authorization refresh failure without losing access to an unchanged saved draft', async () => {
  const draft = { ...blankManualDecisionDraft(), title: 'Recovered on retry' };
  backend(draft);
  const { root, client } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1');
  api.mockRejectedValue(new Error('Temporary connection failure'));
  await client.invalidateQueries({ queryKey: labKey('w1') });
  await settle();
  backend(draft);
  await client.invalidateQueries({ queryKey: labKey('w1') });
  await settle();
  expect(all(root).find((n) => n.props['aria-label'] === 'Title')?.props.value).toBe(
    'Recovered on retry',
  );
});
it('does not delete the private conversation when explicitly creating a note', async () => {
  backend({ ...blankManualDecisionDraft(), title: 'Reviewed' });
  const { root } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1');
  await (all(root).find((n) => n.tag === 'form')!.props.onSubmit as Function)({
    preventDefault() {},
  });
  await settle();
  expect(api.mock.calls.some((c) => c[0] === 'DELETE')).toBe(false);
});
it('uses the selected snooze time and permits clearing it', async () => {
  backend();
  const { root } = await mount(ManualDecisionControls, {
    item: {
      id: 'note1',
      workspace_id: 'w1',
      user_id: 'me',
      status: 'open',
      decide_by: 'today',
      snoozed_until: '2026-11-05T10:00:00Z',
    },
  });
  const input = all(root).find((n) => n.props['aria-label'] === 'Snooze until')!;
  (input.props.onInput as Function)({ target: { value: '2026-11-06T12:30' } });
  await settle();
  await click(root, 'Snooze');
  expect(
    api.mock.calls.some(
      (c) =>
        c[0] === 'PUT' &&
        c[1].endsWith('/triage') &&
        c[2].snoozed_until === new Date('2026-11-06T12:30').toISOString(),
    ),
  ).toBe(true);
  await click(root, 'Clear snooze');
  expect(api.mock.calls.some((c) => c[2]?.snoozed_until === null)).toBe(true);
});
it('archives after creation and still opens the created note when archiving fails', async () => {
  backend({ ...blankManualDecisionDraft(), title: 'Reviewed' });
  const normal = api.getMockImplementation()!;
  api.mockImplementation((method, path, ...args) =>
    path.endsWith('/archive')
      ? Promise.reject(new Error('Archive failed'))
      : normal(method, path, ...args),
  );
  const { root, router } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1');
  await (all(root).find((n) => n.tag === 'form')!.props.onSubmit as Function)({
    preventDefault() {},
  });
  await settle();
  expect(api.mock.calls.some((c) => c[0] === 'POST' && c[1] === '/api/lab/chats/d1/archive')).toBe(
    true,
  );
  expect(router.currentRoute.value.path).toBe('/decisions/note1');
  expect(textOf(root)).not.toContain('Archive failed');
});
it('opens an archived draft read-only without a Create or AI send action', async () => {
  backend({ ...blankManualDecisionDraft(), title: 'Archived proposal' });
  const normal = api.getMockImplementation()!;
  api.mockImplementation(async (method, path, ...args) => {
    const value = await normal(method, path, ...args);
    return path.endsWith('/draft') ? { ...value, archived_at: '2026-11-04T00:00:00Z' } : value;
  });
  const { root } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1');
  expect(textOf(root)).toContain('Archived draft');
  expect(button(root, 'Create')).toBeUndefined();
  expect(all(root).find((n) => n.props['aria-label'] === 'Title')?.props.disabled).toBe(true);
});

it('keeps dirty edits through failed PUT and transient GET and retries the working copy', async () => {
  const original = { ...blankManualDecisionDraft(), title: 'Server title' };
  backend(original);
  const { root, client } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1');
  api.mockRejectedValue(new Error('Network offline'));
  const title = all(root).find((n) => n.props['aria-label'] === 'Title')!;
  await (title.props.onInput as Function)({ target: { value: 'Unsaved private edit' } });
  await settle();
  await client.invalidateQueries({ queryKey: labKey('w1') });
  await settle();
  expect(all(root).find((n) => n.props['aria-label'] === 'Title')?.props.value).toBe(
    'Unsaved private edit',
  );
  expect(textOf(root)).toContain('Not saved');
  backend(original);
  await client.invalidateQueries({ queryKey: labKey('w1') });
  await settle();
  expect(all(root).find((n) => n.props['aria-label'] === 'Title')?.props.value).toBe(
    'Unsaved private edit',
  );
  await click(root, 'Retry saving draft');
  expect(api.mock.calls.some((c) => c[0] === 'PUT' && c[2].title === 'Unsaved private edit')).toBe(
    true,
  );
});
it.each([401, 403, 404])(
  'clears dirty private content when authorization returns %s',
  async (status) => {
    backend({ ...blankManualDecisionDraft(), title: 'Private' });
    const { root, client } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1');
    api.mockRejectedValue(Object.assign(new Error('Access denied'), { status }));
    await client.invalidateQueries({ queryKey: labKey('w1') });
    await settle();
    expect(all(root).some((n) => n.props.value === 'Private')).toBe(false);
  },
);
it('serializes question snapshot A before newer form B and reports pending B honestly', async () => {
  let server = { ...blankManualDecisionDraft(), title: 'A' };
  let finishQuestion!: () => void;
  const blocked = new Promise<void>((resolve) => {
    finishQuestion = resolve;
  });
  let questionStarted = false;
  api.mockImplementation(async (method, path, body) => {
    if (path.endsWith('/questions')) {
      questionStarted = true;
      await blocked;
      server = body.current_draft;
      return {};
    }
    if (path.endsWith('/draft')) {
      if (method === 'PUT') server = body;
      return { chat_id: 'd1', purpose: 'manual_decision_draft', draft: server };
    }
    return { chats: [] };
  });
  const { root } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1&mode=ai');
  const sending = (button(root, 'Test ask').props.onClick as Function)();
  await settle();
  expect(questionStarted).toBe(true);
  await (all(root).find((n) => n.props['aria-label'] === 'Title')!.props.onInput as Function)({
    target: { value: 'B' },
  });
  await settle();
  const pendingStatus = textOf(root);
  finishQuestion();
  await sending;
  await settle();
  expect(server.title).toBe('B');
  expect(pendingStatus).not.toContain('Draft saved');
  expect(all(root).find((n) => n.props['aria-label'] === 'Title')?.props.value).toBe('B');
  expect(textOf(root)).toContain('Draft saved');
});
it.each([401, 403, 404])(
  'scrubs a rejected autosave on %s and can reauthorize the same saved snapshot',
  async (status) => {
    const original = { ...blankManualDecisionDraft(), title: 'Private saved title' };
    backend(original);
    const { root, client } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1');
    api.mockRejectedValue(Object.assign(new Error('Access denied'), { status }));
    await (all(root).find((n) => n.props['aria-label'] === 'Title')!.props.onInput as Function)({
      target: { value: 'Private dirty edit' },
    });
    await settle();
    expect(all(root).some((n) => n.props.value === 'Private dirty edit')).toBe(false);
    expect(textOf(root)).toContain('unavailable');
    backend(original);
    await client.invalidateQueries({ queryKey: labKey('w1') });
    await settle();
    expect(all(root).find((n) => n.props['aria-label'] === 'Title')?.props.value).toBe(
      'Private saved title',
    );
  },
);

it.each([401, 403, 404])(
  'releases Create busy state after draft-save denial %s and authorized recovery',
  async (status) => {
    const original = { ...blankManualDecisionDraft(), title: 'Ready to create' };
    backend(original);
    const { root, client } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1');
    api.mockRejectedValue(Object.assign(new Error('Access denied'), { status }));
    await (all(root).find((n) => n.tag === 'form')!.props.onSubmit as Function)({
      preventDefault() {},
    });
    await settle();
    expect(textOf(root)).toContain('unavailable');
    expect(api.mock.calls.some((c) => c[1] === '/api/decisions/manual')).toBe(false);
    backend(original);
    await client.invalidateQueries({ queryKey: labKey('w1') });
    await settle();
    expect(all(root).find((n) => n.props['aria-label'] === 'Title')?.props.disabled).toBe(false);
    expect(button(root, 'Create')?.props.disabled).toBe(false);
    await (all(root).find((n) => n.tag === 'form')!.props.onSubmit as Function)({
      preventDefault() {},
    });
    await settle();
    expect(api.mock.calls.some((c) => c[1] === '/api/decisions/manual')).toBe(true);
  },
);

it('restores the persisted application marker before rendering the drafting conversation', async () => {
  backend({ ...blankManualDecisionDraft(), title: 'Human reversion' });
  const normal = api.getMockImplementation()!;
  api.mockImplementation(async (method, path, ...args) => {
    const result = await normal(method, path, ...args);
    return path.endsWith('/draft')
      ? { ...result, applied_message_id: '22222222-2222-2222-2222-222222222222' }
      : result;
  });
  const { root } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1&mode=ai');
  expect(
    all(root).some((n) => n.props['data-applied-id'] === '22222222-2222-2222-2222-222222222222'),
  ).toBe(true);
  expect(all(root).find((n) => n.props['aria-label'] === 'Title')?.props.value).toBe(
    'Human reversion',
  );
});
it('atomically persists proposal plus marker and preserves later user reversion on reload', async () => {
  let form = { ...blankManualDecisionDraft(), title: 'Original' };
  let marker: string | null = null;
  api.mockImplementation(async (method, path, body) => {
    if (path.endsWith('/draft')) {
      if (method === 'PUT') {
        const { applied_message_id, ...fields } = body;
        form = fields;
        if (applied_message_id !== undefined) marker = applied_message_id;
      }
      return {
        chat_id: 'd1',
        purpose: 'manual_decision_draft',
        draft: form,
        applied_message_id: marker,
      };
    }
    return { chats: [] };
  });
  const first = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1&mode=ai');
  await click(first.root, 'Test final proposal');
  expect(
    api.mock.calls.some(
      (c) =>
        c[0] === 'PUT' &&
        c[2].title === 'AI title' &&
        c[2].applied_message_id === '22222222-2222-2222-2222-222222222222',
    ),
  ).toBe(true);
  await (all(first.root).find((n) => n.props['aria-label'] === 'Title')!.props.onInput as Function)(
    { target: { value: 'Original' } },
  );
  await settle();
  first.app.unmount();
  first.client.clear();
  const restored = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1&mode=ai');
  expect(all(restored.root).find((n) => n.props['aria-label'] === 'Title')?.props.value).toBe(
    'Original',
  );
  expect(
    all(restored.root).some((n) => n.props['data-applied-id'] === marker && marker !== null),
  ).toBe(true);
});
it('retries failed proposal marker writes atomically with the current human edits', async () => {
  backend({ ...blankManualDecisionDraft(), title: 'Original' });
  const { root } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1&mode=ai');
  api.mockRejectedValue(new Error('Network offline'));
  await click(root, 'Test final proposal');
  expect(textOf(root)).toContain('Not saved');
  await (all(root).find((n) => n.props['aria-label'] === 'Title')!.props.onInput as Function)({
    target: { value: 'Human reversion' },
  });
  await settle();
  api.mockResolvedValue({});
  await click(root, 'Retry saving draft');
  const last = api.mock.calls.filter((c) => c[0] === 'PUT').at(-1)!;
  expect(last[2]).toEqual({
    ...blankManualDecisionDraft(),
    title: 'Human reversion',
    applied_message_id: '22222222-2222-2222-2222-222222222222',
  });
  expect(textOf(root)).toContain('Draft saved');
});

it('keeps newer edits and the application marker when a proposal save is still in flight', async () => {
  let form = { ...blankManualDecisionDraft(), title: 'Original' };
  let marker: string | null = null;
  let release!: () => void;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  let first = true;
  api.mockImplementation(async (method, path, body) => {
    if (path.endsWith('/draft')) {
      if (method === 'PUT') {
        if (first) {
          first = false;
          await pending;
        }
        const { applied_message_id, ...fields } = body;
        form = fields;
        if (applied_message_id !== undefined) marker = applied_message_id;
      }
      return {
        chat_id: 'd1',
        purpose: 'manual_decision_draft',
        draft: form,
        applied_message_id: marker,
      };
    }
    return { chats: [] };
  });
  const { root } = await mount(ManualDecisionCreate, {}, '/decisions/new?draft=d1&mode=ai');
  await click(root, 'Test final proposal');
  await (all(root).find((n) => n.props['aria-label'] === 'Title')!.props.onInput as Function)({
    target: { value: 'Newer human title' },
  });
  await settle();
  expect(textOf(root)).not.toContain('Draft saved');
  release();
  await settle();
  expect(form.title).toBe('Newer human title');
  expect(marker).toBe('22222222-2222-2222-2222-222222222222');
  expect(textOf(root)).toContain('Draft saved');
});
