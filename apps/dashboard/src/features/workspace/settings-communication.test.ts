import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import CommunicationPanel from './settings/CommunicationPanel.vue';
import { slackManifest } from './slack-manifest';
import { workspaceId } from '../../api/session';
import { invalidationsFor } from '../../api/realtime';
import { keys } from '../../api/keys';

const call = vi.fn();
vi.mock('../../api/client', () => ({ api: (...a: unknown[]) => call(...a), ApiError: Error }));

let role = 'owner';
let slack: Record<string, unknown> = {};
async function settle() {
  for (let i = 0; i < 6; i++) {
    await nextTick();
    await new Promise((r) => setTimeout(r, 0));
  }
}
const tick = settle;
const apps: ReturnType<typeof createTreeApp>[] = [];
beforeEach(() => {
  vi.stubGlobal('document', { activeElement: null, addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal('confirm', vi.fn(() => true));
  role = 'owner';
  slack = { connected: false, drafting_agent_id: null, my_binding: null };
  call.mockReset().mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/workspaces') return [{ id: 'w', role }];
    if (method === 'GET' && path === '/api/slack') return slack;
    return undefined;
  });
});
afterEach(() => {
  apps.splice(0).forEach((a) => a.unmount());
  workspaceId.value = '';
  vi.unstubAllGlobals();
});
async function mount() {
  workspaceId.value = 'w';
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = node('root');
  const app = createTreeApp({ render: () => h(CommunicationPanel) });
  app.use(VueQueryPlugin, { queryClient: client });
  apps.push(app);
  app.mount(root);
  await settle();
  return root;
}
const button = (root: ReturnType<typeof node>, text: string) =>
  all(root).find((n) => n.tag === 'button' && textOf(n).trim() === text);
const byLabel = (root: ReturnType<typeof node>, l: string) => all(root).find((n) => n.props['aria-label'] === l);

it('lists exactly one Slack row and the owner sees Connect', async () => {
  const root = await mount();
  expect(all(root).filter((n) => n.props['data-app'] === 'slack')).toHaveLength(1);
  expect(textOf(root)).toContain('Disconnected');
  expect(textOf(root)).toContain('Workspace owner only');
  expect(textOf(root)).toContain('Not linked — message Aristotle in Slack to link');
  expect(button(root, 'Connect')).toBeDefined();
});

it('shows the visibility warning when connected and hides actions from members', async () => {
  role = 'member';
  slack = { connected: true, status: 'active', team_name: 'Lab', drafting_agent_id: 'a1', my_binding: { slack_user_id: 'U1' } };
  const root = await mount();
  expect(textOf(root)).toContain('Connected · Lab');
  expect(textOf(root)).toContain(
    'Answers in a Slack channel are visible to everyone in that channel, including people outside this workspace.',
  );
  expect(button(root, 'Manage')).toBeUndefined();
  expect(button(root, 'Connect')).toBeUndefined();
  expect(textOf(root)).toContain('Linked as U1');
  (button(root, 'Remove')!.props.onClick as () => void)();
  await tick();
  expect(call).toHaveBeenCalledWith('DELETE', '/api/slack/binding');
});

it('offers Reconnect when the installation was revoked', async () => {
  slack = { connected: true, status: 'revoked', team_name: 'Lab', drafting_agent_id: null, my_binding: null };
  const root = await mount();
  expect(textOf(root)).toContain('Reconnect needed');
  expect(button(root, 'Reconnect')).toBeDefined();
});

it('shows Copied on the manifest button only after a successful clipboard write', async () => {
  const writeText = vi.fn().mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('blocked'));
  vi.stubGlobal('navigator', { clipboard: { writeText } });
  const root = await mount();
  (button(root, 'Connect')!.props.onClick as () => void)();
  await tick();
  const copy = button(root, 'Copy')!.props.onClick as () => Promise<void>;

  await copy();
  await nextTick();
  expect(writeText).toHaveBeenCalledWith(slackManifest());
  expect(button(root, 'Copied')).toBeDefined();

  await copy();
  await nextTick();
  expect(button(root, 'Copy')).toBeDefined();
  expect(textOf(root)).toContain('Could not access your clipboard.');
});

it('connects with both tokens without asking for a drafting agent and clears the inputs', async () => {
  const root = await mount();
  (button(root, 'Connect')!.props.onClick as () => void)();
  await tick();
  const bot = all(root).find((n) => n.props['aria-label'] === 'Bot token (xoxb-)')!;
  const app = all(root).find((n) => n.props['aria-label'] === 'App-level token (xapp-)')!;
  expect(bot.props.type).toBe('password');
  expect(app.props.type).toBe('password');
  expect(byLabel(root, 'Drafting agent')).toBeUndefined();
  expect(String(byLabel(root, 'Slack app manifest')!.props.value)).toContain('users:read.email');
  (bot.props.onInput as (e: unknown) => void)({ target: { value: 'xoxb-1' } });
  (app.props.onInput as (e: unknown) => void)({ target: { value: 'xapp-1' } });
  await tick();
  const connect = all(root).filter((n) => n.tag === 'button' && textOf(n).trim() === 'Connect').pop()!;
  (connect.props.onClick as () => void)();
  await tick();
  expect(call).toHaveBeenCalledWith('PUT', '/api/slack', {
    bot_token: 'xoxb-1',
    app_token: 'xapp-1',
  });
  // The dialog closes and unmounts the token inputs; a reopened one starts empty.
  expect(byLabel(root, 'Bot token (xoxb-)')).toBeUndefined();
  (button(root, 'Connect')!.props.onClick as () => void)();
  await tick();
  expect(byLabel(root, 'Bot token (xoxb-)')!.props.value).toBe('');
});

it('disconnects behind a confirm in manage mode', async () => {
  slack = { connected: true, status: 'active', team_name: 'Lab', drafting_agent_id: 'a1', my_binding: null };
  const root = await mount();
  (button(root, 'Manage')!.props.onClick as () => void)();
  await tick();
  (button(root, 'Disconnect')!.props.onClick as () => void)();
  await tick();
  expect(confirm).toHaveBeenCalled();
  expect(call).toHaveBeenCalledWith('DELETE', '/api/slack');
});

it('invalidates the Slack query on slack.changed', () => {
  expect(
    invalidationsFor('w', { type: 'slack.changed', workspace_id: 'w', card_id: null, payload: {} } as never),
  ).toEqual([keys.slack('w')]);
  expect(keys.slack('w')).toEqual(['ws', 'w', 'slack']);
});

it('ships a manifest with the email scope and the /to-do command', () => {
  const m = slackManifest();
  expect(m).toContain('users:read.email');
  expect(m).toContain('- command: /to-do');
  expect(m).not.toContain('- command: /study');
  expect(m).toContain('messages_tab_enabled: true');
  expect(m).toContain('messages_tab_read_only_enabled: false');
  expect(m).toContain('socket_mode_enabled: true');
  expect(m).toContain('tokens_revoked');
});
