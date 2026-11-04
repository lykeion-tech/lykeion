import { afterEach, expect, it, vi } from 'vitest';
import { nextTick, ref } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import DecisionBuilderConversation from './DecisionBuilderConversation.vue';
import { workspaceId } from '../../api/session';

const sendAPI = vi.fn();
const pages = ref<any>({ pages: [{ messages: [], pending: false }] });
vi.mock('../../api/client', () => ({ api: (...args: unknown[]) => sendAPI(...args), ApiError: class extends Error { status = 0; body = {}; } }));
vi.mock('../aristotle-window/api', () => ({
  labKey: (ws: string) => ['ws', ws, 'lab'],
  useMessages: () => ({ data: pages, isError: ref(false), isSuccess: ref(true), isFetching: ref(false), hasNextPage: ref(false), fetchNextPage: vi.fn(), refetch: vi.fn() }),
}));
vi.mock('../aristotle-window/LabAnswer.vue', () => ({ default: { render: () => null } }));
vi.mock('../runs/RunStatus.vue', () => ({ default: { render: () => null } }));
const draft = { title: 'Human title', context: '', deadline: null, card_id: null };
const runtime = { id: 'r', owner_id: 'me', harness: 'claude', status: 'online', unhealthy_reason: null };
const cleanups: (() => void)[] = [];
afterEach(() => { cleanups.splice(0).forEach((fn) => fn()); sendAPI.mockReset(); pages.value = { pages: [{ messages: [], pending: false }] }; });
async function mount(overrides: Record<string, unknown> = {}) {
  workspaceId.value = 'w';
  const submit = vi.fn(async (_turn: unknown) => {}), proposal = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = node('root');
  const app = createTreeApp(DecisionBuilderConversation, { userId: 'me', draftChat: 'chat', manualDraft: draft, appliedMessageId: null, manualDraftBusy: false, readonly: false, runtime, model: '', submitManualQuestion: submit, onManualProposal: proposal, ...overrides });
  app.use(VueQueryPlugin, { queryClient: client }); app.mount(root);
  cleanups.push(() => { app.unmount(); client.clear(); });
  await nextTick();
  return { root, submit, proposal };
}
it('sends the reviewed draft through the selected runtime and never creates a note', async () => {
  const t = await mount();
  const input = all(t.root).find((n) => n.props['aria-label'] === 'Describe your decision')!;
  (input.props.onInput as Function)({ target: { value: 'Please draft' } });
  await nextTick();
  await (all(t.root).find((n) => n.tag === 'form')!.props.onSubmit as Function)({ preventDefault() {} });
  await nextTick();
  expect(t.submit).toHaveBeenCalledOnce();
  expect(t.submit.mock.calls[0]![0]).toMatchObject({ user_request: 'Please draft', current_draft: draft, harness: 'claude' });
  expect(sendAPI).not.toHaveBeenCalled();
});
it('applies only a valid structured proposal with the matching frozen input', async () => {
  const t = await mount();
  pages.value = { pages: [{ pending: false, messages: [
    { id:'q', kind:'question', body:'Draft', agent_run_id:'run', current_draft:draft },
    { id:'a', kind:'answer', body:'', agent_run_id:'run', statements:[], evidence:[], proposal:{ message:'Here is a draft', draft:{...draft,title:'Suggested'} } },
  ] }] };
  await nextTick();
  expect(t.proposal).toHaveBeenCalledWith({ messageId:'a', draft:{...draft,title:'Suggested'}, input:draft });
  expect(textOf(t.root)).toContain('Here is a draft');
});
it('stops the pending turn without restoring its text', async () => {
  pages.value = { pages: [{ pending:true, messages:[{id:'q', kind:'question', body:'Original', agent_run_id:'run'}] }] };
  const t = await mount();
  const stop = all(t.root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Stop')!;
  await (stop.props.onClick as Function)();
  expect(sendAPI).toHaveBeenCalledWith('POST', '/api/lab/chats/chat/questions/q/cancel', undefined, undefined, { workspace:'w' });
  expect(all(t.root).find((n) => n.props['aria-label'] === 'Describe your decision')?.props.value ?? '').toBe('');
});
it('explains when the selected machine is offline', async () => {
  const t = await mount({ runtime: { ...runtime, status:'offline' } });
  expect(textOf(t.root)).toContain('Your selected machine is offline');
  expect(all(t.root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Send')?.props.disabled).toBe(true);
});
