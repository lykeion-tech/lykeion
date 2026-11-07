import { expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import MarkdownViewer from './MarkdownViewer.vue';
import { all, createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';

vi.mock('../AssistantMessage.vue', () => ({
  default: { props: ['text'], template: '<div data-testid="rendered">{{ text }}</div>' },
}));

it('switches between rendered Markdown and its source', async () => {
  const root = node('root');
  const app = createTreeApp({
    render: () => h(MarkdownViewer, { text: '# Research\n\nEvidence' }),
  });
  app.mount(root);
  await nextTick();
  const find = (label: string) => all(root).find((n) => n.props['aria-label'] === label)!;
  expect(find('Rendered Markdown').props['aria-pressed']).toBe(true);
  expect(find('Markdown source').props['aria-pressed']).toBe(false);
  expect(textOf(root)).toContain('Research');
  (find('Markdown source').props.onClick as () => void)();
  await nextTick();
  expect(find('Markdown source').props['aria-pressed']).toBe(true);
  expect(textOf(root)).toContain('# Research');
  (find('Rendered Markdown').props.onClick as () => void)();
  await nextTick();
  expect(find('Rendered Markdown').props['aria-pressed']).toBe(true);
  expect(all(root).some((n) => n.props['data-testid'] === 'rendered')).toBe(true);
  app.unmount();
});
