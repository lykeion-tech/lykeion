import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import StudyPullRequests from './StudyPullRequests.vue';
import { workspaceId } from '../../api/session';
const call = vi.fn();
vi.mock('../../api/client', () => ({
  api: (...args: unknown[]) => call(...args),
  ApiError: Error,
}));
const cleanup: (() => void)[] = [];
afterEach(() => {
  cleanup.splice(0).forEach((fn) => fn());
  workspaceId.value = '';
  call.mockReset();
  vi.unstubAllGlobals();
});
const pr = {
  id: 'p',
  repo_owner: 'lab',
  repo_name: 'science',
  number: 1,
  title: 'Data',
  state: 'open',
  html_url: 'https://github.com/lab/science/pull/1',
  author_login: 'ada',
  additions: 3,
  deletions: 2,
  changed_files: 4,
  ci_state: 'passed',
  ci_passed: 3,
  ci_total: 3,
  ci_failing: ['lint', 'test'],
  mergeability: 'mergeable',
  snapshot_stale: false,
};
async function mount(rows: Record<string, unknown>[]) {
  vi.stubGlobal('Document', class {});
  vi.stubGlobal('ShadowRoot', class {});
  workspaceId.value = 'w';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  client.setQueryData(['ws', 'w', 'cards', 'c', 'pull-requests'], rows);
  call.mockResolvedValue(rows);
  const app = createTreeApp({ render: () => h(StudyPullRequests, { cardId: 'c' }) });
  app.use(VueQueryPlugin, { queryClient: client });
  const root = node('root');
  app.mount(root);
  cleanup.push(() => {
    app.unmount();
    client.clear();
  });
  await nextTick();
  return root;
}
it.each([
  ['open', 'passed', 'mergeable', 'Open', 'All checks passed (3)', 'Mergeable'],
  ['draft', 'failed', 'conflicting', 'Draft', 'Failed: lint, test', 'Conflicts'],
  ['open', 'pending', 'blocked', 'Open', 'Checks running', 'Blocked'],
  ['open', null, 'behind', 'Open', '', 'Behind'],
])(
  'renders %s pull request snapshots (%s / %s)',
  async (state, ci_state, mergeability, label, ci, merge) => {
    const root = await mount([{ ...pr, state, ci_state, mergeability, snapshot_stale: true }]);
    const link = all(root).find((n) => n.tag === 'a')!;
    expect(link.props).toMatchObject({ href: pr.html_url, target: '_blank' });
    for (const s of ['lab/science #1', 'Data', 'ada', '+3 −2 · 4 files', label!, merge!])
      expect(textOf(link)).toContain(s);
    if (ci) expect(textOf(link)).toContain(ci + ' (stale)');
  },
);
it.each(['merged', 'closed'])('hides CI and mergeability for %s', async (state) => {
  const root = await mount([{ ...pr, state }]);
  expect(textOf(root)).toContain(state === 'merged' ? 'Merged' : 'Closed');
  expect(textOf(root)).not.toMatch(/All checks passed|Mergeable/);
});
it('adds a pasted link and removes a linked PR', async () => {
  const root = await mount([]);
  expect(textOf(root)).toContain('No pull requests yet.');
  (
    all(root).find((n) => n.props['aria-label'] === 'Add pull request')!.props.onClick as () => void
  )();
  await nextTick();
  const input = all(root).find((n) => n.props['aria-label'] === 'Pull request link')!;
  (input.props['onUpdate:modelValue'] as (s: string) => void)(pr.html_url);
  call.mockImplementation(async (method: string) => (method === 'GET' ? [pr] : pr));
  await nextTick();
  (all(root).find((n) => n.tag === 'form')!.props.onSubmit as (e: unknown) => void)({
    preventDefault() {},
  });
  await vi.waitFor(() =>
    expect(
      call.mock.calls.some(
        ([m, p, b]) => m === 'POST' && p === '/api/cards/c/pull-requests' && b.url === pr.html_url,
      ),
    ).toBe(true),
  );
  await vi.waitFor(() => expect(all(root).find((n) => n.tag === 'form')).toBeUndefined());
  expect(textOf(root)).toContain('Data');
  (
    all(root).find((n) => n.props['aria-label'] === 'Pull request actions for lab/science #1')!
      .props.onClick as () => void
  )();
  await nextTick();
  (
    all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Remove from study')!.props
      .onClick as () => void
  )();
  await vi.waitFor(() =>
    expect(
      call.mock.calls.some(([m, p]) => m === 'DELETE' && p === '/api/cards/c/pull-requests/p'),
    ).toBe(true),
  );
});
