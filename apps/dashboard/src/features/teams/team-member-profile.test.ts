import { expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import { createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import TeamMemberProfile from './TeamMemberProfile.vue';

vi.mock('vue-router', () => ({
  RouterLink: defineComponent({
    props: { to: { type: String, required: true } },
    setup:
      (props, { slots }) =>
      () =>
        h('a', { href: props.to }, slots.default?.()),
  }),
}));

const member = { name: 'leader', role: 'Plans the analysis', agent: { instructions: '' } };

function render(agentId?: string) {
  const root = node('root');
  const app = createTreeApp(TeamMemberProfile, { member, role: 'Leader', agentId });
  app.mount(root);
  const text = textOf(root);
  app.unmount();
  return text;
}

it('links to the agent page when the member has an agent', () => {
  expect(render('a1')).toContain('Details →');
});

it('shows no Details link when the member has no agent', () => {
  expect(render()).not.toContain('Details');
});
