import { expect, it, vi } from 'vitest';
import { h } from 'vue';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import TranscriptTurn from './TranscriptTurn.vue';

vi.mock('./api', () => ({
  isActiveTurn: () => false,
  useCoresearchSteps: () => ({ data: { value: [] }, error: { value: null } }),
}));
vi.mock('./ExperimentFileCard.vue', () => ({
  default: {
    props: ['file'],
    render() {
      return h('span', 'file card');
    },
  },
}));

it('labels generated files below a separator only when files exist', () => {
  const version = { n: 1, size: 1, sha256: 'x', created_at: '', agent_run_id: 'run' };
  const item = { file: { id: 'f', path: 'file.csv', head: version }, version };
  const render = (files: (typeof item)[]) => {
    const app = createTreeApp({
      render: () =>
        h(TranscriptTurn, {
          projectId: 'p',
          chatId: 'c',
          turn: {
            id: 't',
            body: 'prompt',
            kind: 'turn',
            workspace_id: 'lab',
            chat_id: 'c',
            author_user_id: null,
            agent_run_id: 'run',
            created_at: '',
            status: null,
            failure_reason: null,
          },
          replies: [],
          files,
        }),
    });
    const root = node('root');
    app.mount(root);
    return { app, root };
  };
  const shown = render([item]);
  const area = all(shown.root).find((n) => n.props['aria-label'] === 'Generated files');
  if (!area) throw new Error('Generated files section missing');
  expect(textOf(area)).toContain('FILES GENERATED');
  expect(all(area).some((n) => n.tag === 'hr')).toBe(true);
  expect(textOf(area).indexOf('FILES GENERATED')).toBeLessThan(textOf(area).indexOf('file card'));
  shown.app.unmount();
  const empty = render([]);
  expect(textOf(empty.root)).not.toContain('FILES GENERATED');
  empty.app.unmount();
});
