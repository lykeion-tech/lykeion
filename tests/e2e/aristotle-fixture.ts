import type { Page } from '@playwright/test';
import { installFilesFixture } from './coresearch-files-fixture';

export async function installAristotleFixture(page: Page, online = false) {
  const base = await installFilesFixture(page);
  let agent = {
    id: 'aristotle',
    workspace_id: 'lab',
    owner_id: 'me',
    built_in_key: 'aristotle',
    name: 'Aristotle',
    description: 'Research together',
    instructions: '',
    harness: 'claude',
    model: '',
    visibility: 'private',
    archived_at: null,
    conversation_starters: [] as { label: string; prompt: string }[],
  };
  const welcome = {
    id: 'welcome',
    project_id: 'private-project',
    chat_id: 'chat',
    agent_id: 'aristotle',
    owner_id: 'me',
    workspace_id: 'lab',
    is_welcome: true,
    title: 'Getting started with Aristotle',
    preview: '',
    snapshot_harness: null,
    snapshot_model: null,
    snapshot_instructions: null,
  };
  const accepted = {
    ...welcome,
    id: 'accepted',
    is_welcome: false,
    title: 'Accepted question',
    chat_id: 'accepted-chat',
  };
  const requests: { path: string; body: Record<string, string>; workspace?: string }[] = [];
  let created = false,
    cancelled = false,
    failures = 0,
    loseAccepted = false;
  const receipts = new Map<string, unknown>();
  await page.route('**/api/**', async (route) => {
    const req = route.request(),
      path = new URL(req.url()).pathname;
    let data: unknown;
    if (path === '/api/coresearch/bootstrap') data = { agent, conversation: welcome };
    else if (path === '/api/agents/aristotle') {
      if (req.method() === 'PATCH') {
        const body = req.postDataJSON();
        requests.push({ path, body, workspace: req.headers()['x-workspace-id'] });
        agent = { ...agent, ...body };
      }
      data = agent;
    } else if (path === '/api/agents') data = [agent];
    else if (path === '/api/runtimes')
      data = online ? [{ id: 'machine', owner_id: 'me', status: 'online', harness: 'claude' }] : [];
    else if (path === '/api/coresearch/conversations' && req.method() === 'GET')
      data = created ? [welcome, accepted] : [welcome];
    else if (
      path === '/api/coresearch/conversations' ||
      /\/conversations\/[^/]+\/turns$/.test(path)
    ) {
      requests.push({ path, body: req.postDataJSON(), workspace: req.headers()['x-workspace-id'] });
      if (failures > 0) {
        failures--;
        await route.abort('failed');
        return;
      }
      created = true;
      const key = req.postDataJSON().request_id;
      if (!receipts.has(key))
        receipts.set(key, {
          conversation: accepted,
          run: { id: 'accepted-run', status: 'queued' },
        });
      data = receipts.get(key);
      if (loseAccepted) {
        loseAccepted = false;
        await route.abort('failed');
        return;
      }
    } else if (path.endsWith('/accepted-chat/messages'))
      data = [
        {
          id: 'current-turn',
          chat_id: 'accepted-chat',
          kind: 'turn',
          body: 'Current fixture transcript',
          agent_run_id: 'current-run',
          status: cancelled ? 'cancelled' : 'running',
        },
      ];
    else if (path.endsWith('/current-run/cancel')) {
      cancelled = true;
      requests.push({ path, body: {} });
      data = {};
    } else if (path.includes('/private-project/') && path.endsWith('/messages')) data = [];
    else {
      await route.fallback();
      return;
    }
    await route.fulfill({ status: req.method() === 'POST' ? 201 : 200, json: data });
  });
  return {
    ...base,
    requests,
    receipts,
    loseNextAcceptedResponse() {
      loseAccepted = true;
    },
    failNext(count: number) {
      failures = count;
    },
  };
}
