import { expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import StudyHandIn from './StudyHandIn.vue';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';

vi.mock('../studies/StudyFileThumbnail.vue', () => ({
  default: { template: '<span class="thumbnail" />' },
}));
vi.mock('../../ui/RecordedMarkdown.vue', () => ({ default: { template: '<span />' } }));

it('opens the saved output belonging to this run', async () => {
  workspaceId.value = 'w';
  const client = new QueryClient({
    defaultOptions: { queries: { staleTime: Infinity, retry: false } },
  });
  client.setQueryData(
    [...keys.recordsAll('w', 'c'), 'kind', 'output'],
    [
      {
        id: 'old',
        key: 'old-key',
        agent_run_id: 'other',
        body: { name: 'result.csv', size_bytes: 3 },
      },
      { id: 'new', key: 'new-key', agent_run_id: 'r', body: { name: 'result.csv', size_bytes: 3 } },
    ],
  );
  const open = vi.fn();
  const root = node('root');
  const app = createTreeApp({
    render: () =>
      h(StudyHandIn, {
        cardId: 'c',
        runId: 'r',
        role: 'agent',
        column: 'experimentation',
        handIn: { answer: 'Done', outputs: ['result.csv'] },
        onOpenOutput: open,
      }),
  });
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(root);
  await nextTick();
  const button = all(root).find((n) => n.tag === 'button' && textOf(n).includes('result.csv'))!;
  expect(all(button).some((n) => n.props.class === 'thumbnail')).toBe(true);
  (button.props.onClick as () => void)();
  expect(open).toHaveBeenCalledWith('new-key');
  app.unmount();
  client.clear();
  workspaceId.value = '';
});

it('leaves the question for the researcher out of the run report', async () => {
  const client = new QueryClient();
  const root = node('root');
  const app = createTreeApp({
    render: () =>
      h(StudyHandIn, {
        role: 'leader',
        column: 'grounding',
        handIn: {
          answer: 'No topic given.',
          status: 'ask',
          decision: { title: 'What research question?', options: [{ id: 'give_topic' }] },
          reason: 'A topic is needed.',
        },
      }),
  });
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(root);
  await nextTick();
  const text = textOf(root);
  expect(text).toContain('A topic is needed.');
  expect(text).not.toContain('What research question?');
  expect(text).not.toContain('give_topic');
  app.unmount();
  client.clear();
});
