import { expect, it, vi } from 'vitest';
import EmojiIcon from '../apps/dashboard/src/components/EmojiIcon.vue';
import { all, createTreeApp, node } from './daemon-tree';

it('lets an editor pick an emoji and prevents editing for a read-only object', async () => {
  vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} });
  const root = node('root');
  const select = vi.fn();
  const app = createTreeApp(EmojiIcon, {
    value: '',
    editable: true,
    fallback: 'bot',
    onSelect: select,
  });
  app.mount(root);
  const button = all(root).find((n) => n.props['aria-label'] === 'Change avatar');
  expect(button).toBeDefined();
  (button!.props.onClick as () => void)();
  const { nextTick } = await import('vue');
  await nextTick();
  (all(root).find((n) => n.props['aria-label'] === '🦉')!.props.onClick as () => void)();
  expect(select).toHaveBeenCalledWith('emoji:🦉');
  app.unmount();
  const readOnly = node('root');
  const view = createTreeApp(EmojiIcon, { value: 'emoji:🦉', editable: false, fallback: 'bot' });
  view.mount(readOnly);
  expect(all(readOnly).some((n) => n.props['aria-label'] === 'Change avatar')).toBe(false);
  view.unmount();
  vi.unstubAllGlobals();
});
