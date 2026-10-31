import { expect, it, vi } from 'vitest';
import AvatarPicker from './AvatarPicker.vue';
import { all, createTreeApp, node, textOf } from '../../../../tests/daemon-tree';

it('offers emoji without image upload when used for an Agent or Team icon', () => {
  const root = node('root');
  vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} });
  let chosen = '';
  const app = createTreeApp(AvatarPicker, {
    modelValue: '',
    emojiOnly: true,
    'onUpdate:modelValue': (value: string) => {
      chosen = value;
    },
  });
  app.mount(root);
  expect(all(root).some((n) => n.tag === 'button' && textOf(n).includes('Upload image'))).toBe(
    false,
  );
  const emoji = all(root).find((n) => n.tag === 'button' && n.props['aria-label'] === '🦉');
  expect(emoji).toBeDefined();
  (emoji!.props.onClick as () => void)();
  expect(chosen).toBe('🦉');
  app.unmount();
  vi.unstubAllGlobals();
});
