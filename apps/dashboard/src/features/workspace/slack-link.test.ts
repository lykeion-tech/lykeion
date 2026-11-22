import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import SlackLinkView from './SlackLinkView.vue';

const call = vi.fn();
vi.mock('../../api/client', () => ({ api: (...a: unknown[]) => call(...a), ApiError: Error }));
async function settle() {
  for (let i = 0; i < 6; i++) {
    await nextTick();
    await new Promise((r) => setTimeout(r, 0));
  }
}
const apps: ReturnType<typeof createTreeApp>[] = [];
beforeEach(() => {
  call.mockReset().mockImplementation(async (method: string, path: string) =>
    method === 'GET' && path === '/api/slack' ? { connected: true, team_name: 'Lab', my_binding: null } : undefined,
  );
});
afterEach(() => apps.splice(0).forEach((a) => a.unmount()));
async function mount(query = 'workspace=w9&token=tok123') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/slack/link', component: { render: () => null } }, { path: '/:p(.*)*', component: { render: () => null } }],
  });
  await router.push(`/slack/link?${query}`);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = node('root');
  const app = createTreeApp({ render: () => h(SlackLinkView) });
  app.use(router).use(VueQueryPlugin, { queryClient: client });
  apps.push(app);
  app.mount(root);
  await settle();
  return root;
}
const button = (root: ReturnType<typeof node>, text: string) =>
  all(root).find((n) => n.tag === 'button' && textOf(n).trim() === text)!;

it('asks for confirmation, then posts the query token in the query workspace', async () => {
  const root = await mount();
  expect(textOf(root)).toContain('Link Slack account to Lab in this workspace?');
  expect(call).not.toHaveBeenCalledWith('POST', '/api/slack/link', expect.anything(), undefined, expect.anything());
  call.mockImplementation(async (m: string) => (m === 'POST' ? { slack_user_id: 'U1', team_name: 'Lab' } : undefined));
  (button(root, 'Link').props.onClick as () => void)();
  await settle();
  expect(call).toHaveBeenCalledWith('POST', '/api/slack/link', { token: 'tok123' }, undefined, { workspace: 'w9' });
  expect(textOf(root)).toContain('Slack account linked');
});

it('shows the server error verbatim', async () => {
  const root = await mount();
  call.mockImplementation(async (m: string) => {
    if (m === 'POST') throw new Error('Slack email does not match your Lykeion email');
  });
  (button(root, 'Link').props.onClick as () => void)();
  await settle();
  expect(textOf(root)).toContain('Slack email does not match your Lykeion email');
});

it('says the link is incomplete and hides Link without a token', async () => {
  const root = await mount('workspace=w9');
  expect(textOf(root)).toContain('This link is incomplete. Message Aristotle in Slack for a new one.');
  expect(all(root).some((n) => n.tag === 'button' && textOf(n).trim() === 'Link')).toBe(false);
});
