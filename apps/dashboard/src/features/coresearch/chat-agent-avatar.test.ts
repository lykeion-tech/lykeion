import { expect, it, vi } from 'vitest';
import { h } from 'vue';
import { createTreeApp, node } from '../../../../../tests/daemon-tree';
import ChatAgentAvatar from './ChatAgentAvatar.vue';

vi.mock('./Aristotle.vue', () => ({ default: { template: '<span />' } }));

it('uses the accent background only for the Aristotle portrait', () => {
  const root = node('root');
  const app = createTreeApp({ render: () => h('div', [
    h(ChatAgentAvatar, { fallbackAristotle: true }),
    h(ChatAgentAvatar),
  ]) });
  app.mount(root);
  const avatars = root.children[0].children;
  expect(avatars[0].props.class).toContain('is-aristotle');
  expect(avatars[1].props.class).not.toContain('is-aristotle');
  app.unmount();
});
