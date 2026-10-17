import { expect, test } from '@playwright/test';
import { LAB, labCode, labFixture, labSignIn } from './lab';
import type { AgentRun, RunMessage } from '../../apps/dashboard/src/api/types';

test('the dashboard origin reaches the lab server through its proxy', async ({ request }) => {
  const me = await request.get('/api/me');
  expect(me.status()).toBe(401);
  expect(await me.json()).toEqual({ error: 'sign in required' });
  const sent = await request.post('/auth/send-code', { data: { email: 'probe@lab.test' } });
  expect(sent.status()).toBe(204);
  expect(await labCode(request, 'probe@lab.test')).toMatch(/^\d{6}$/);
});

test('the real message API paginates beyond 500 and scopes overlapping sequences by attempt', async ({
  request,
}) => {
  await labSignIn(request);
  const fixture = await labFixture(request);
  const headers = { 'X-Workspace-ID': fixture.workspace_id };
  const path = `/api/agent-runs/${fixture.run_id}/messages`;
  const first = await request.get(`${path}?attempt=2`, { headers });
  expect(first.status()).toBe(200);
  const page1: RunMessage[] = await first.json();
  expect(page1).toHaveLength(500);
  expect(page1[0]).toMatchObject({
    agent_run_id: fixture.run_id,
    attempt: 2,
    seq: 1,
    type: 'text',
    tool: '',
    content: 'Attempt 2 message 001',
  });
  expect(page1[499]).toMatchObject({ attempt: 2, seq: 500, content: 'Attempt 2 message 500' });
  const second = await request.get(`${path}?attempt=2&after=500`, { headers });
  expect(second.status()).toBe(200);
  const page2: RunMessage[] = await second.json();
  expect(page2).toHaveLength(1);
  expect(page2[0]).toMatchObject({ attempt: 2, seq: 501, content: 'Attempt 2 message 501' });
  const all = [...page1, ...page2];
  expect(all.map((m) => m.seq)).toEqual(Array.from({ length: 501 }, (_, i) => i + 1));
  expect(all.every((m) => m.attempt === 2)).toBe(true);
  const end = await request.get(`${path}?attempt=2&after=501`, { headers });
  expect(end.status()).toBe(200);
  expect(await end.json()).toEqual([]);
  const current = await request.get(path, { headers });
  expect(current.status()).toBe(200);
  const currentMessages: RunMessage[] = await current.json();
  expect(currentMessages).toHaveLength(500);
  expect(currentMessages[0]).toMatchObject({
    attempt: 2,
    seq: 1,
    content: 'Attempt 2 message 001',
  });
  const old = await request.get(`${path}?attempt=1`, { headers });
  expect(old.status()).toBe(200);
  const oldMessages: RunMessage[] = await old.json();
  expect(oldMessages.map((m) => [m.attempt, m.seq, m.content])).toEqual([
    [1, 1, 'Attempt 1 message 001'],
    [1, 2, 'Attempt 1 message 002'],
  ]);
  const oldEnd = await request.get(`${path}?attempt=1&after=2`, { headers });
  expect(oldEnd.status()).toBe(200);
  expect(await oldEnd.json()).toEqual([]);
  const invalid = await request.get(`${path}?attempt=3`, { headers });
  expect(invalid.status()).toBe(400);
  const card = await request.get(`/api/cards/${fixture.card_id}`, { headers });
  expect(card.status()).toBe(200);
  expect(await card.json()).toMatchObject({ flag: 'escalated', board_column: 'grounding' });
});

test('the lab daemon records a failed result and the engine files an Inbox item', async ({
  request,
}) => {
  await labSignIn(request);
  const created = await request.post('/api/workspaces', {
    data: { name: 'Daemon browser lab', slug: `daemon-${Date.now()}` },
  });
  expect(created.status()).toBe(201);
  const workspace = await created.json();
  const headers = { 'X-Workspace-ID': workspace.id };
  const cardResponse = await request.post('/api/cards', {
    headers,
    data: { question: 'Can the lab record a real failed execution?' },
  });
  expect(cardResponse.status()).toBe(201);
  const card = await cardResponse.json();
  await expect
    .poll(
      async () => {
        const res = await request.get(`/api/cards/${card.id}`, { headers });
        expect(res.status()).toBe(200);
        return (await res.json()).flag;
      },
      { timeout: 30_000 },
    )
    .toBe('escalated');
  const inbox = await request.get('/api/inbox', { headers });
  expect(inbox.status()).toBe(200);
  expect(await inbox.json()).toEqual(
    expect.arrayContaining([expect.objectContaining({ card_id: card.id, kind: 'escalated' })]),
  );
  const runsResponse = await request.get(`/api/cards/${card.id}/runs`, { headers });
  expect(runsResponse.status()).toBe(200);
  const runs: AgentRun[] = await runsResponse.json();
  const failed = runs.find((run) => run.status === 'completed');
  expect(failed).toBeDefined();
  const records = await request.get(`/api/cards/${card.id}/records?column=grounding`, { headers });
  expect(records.status()).toBe(200);
  expect(await records.json()).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        kind: 'failed',
        agent_run_id: failed!.id,
        body: { reason: 'the lab script stops here' },
      }),
    ]),
  );
  const messages = await request.get(`/api/agent-runs/${failed!.id}/messages`, { headers });
  expect(messages.status()).toBe(200);
  const transcript: RunMessage[] = await messages.json();
  expect(transcript.filter((m) => m.type === 'tool_use' && m.tool === 'Bash')).toHaveLength(2);
  expect(transcript).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        type: 'text',
        content: 'Read the card; the lab script stops here.',
      }),
    ]),
  );
  expect((await request.post(`${LAB}/lab/hypotheses/${card.id}`)).status()).toBe(204);
  const moved = await request.get(`/api/cards/${card.id}`, { headers });
  expect(moved.status()).toBe(200);
  expect(await moved.json()).toMatchObject({
    board_column: 'hypotheses',
    flag: '',
    state: 'working',
  });
});

test('a browser on the dashboard origin receives committed events through the WebSocket proxy', async ({
  page,
}) => {
  await labSignIn(page.request);
  const fixture = await labFixture(page.request);
  const response = await page.goto('/');
  expect(response?.status()).toBe(200);
  const event = await page.evaluate(
    ({ workspace_id, card_id }) =>
      new Promise<{ type: string; card_id: string }>((resolve, reject) => {
        const socket = new WebSocket(`ws://${location.host}/ws`);
        const timeout = setTimeout(() => {
          socket.close();
          reject(new Error('No fixture event through dashboard WebSocket proxy'));
        }, 10_000);
        const finish = () => {
          clearTimeout(timeout);
          socket.close();
        };
        socket.onopen = () =>
          socket.send(JSON.stringify({ type: 'subscribe', workspace: workspace_id, cursor: 0 }));
        socket.onerror = () => {
          finish();
          reject(new Error('Dashboard WebSocket proxy refused the browser'));
        };
        socket.onmessage = (message) => {
          const frame = JSON.parse(String(message.data));
          if (frame.type === 'error') {
            finish();
            reject(new Error(frame.error));
          }
          if (frame.type === 'event' && frame.event?.card_id === card_id) {
            finish();
            resolve(frame.event);
          }
        };
      }),
    fixture,
  );
  expect(event).toMatchObject({ type: 'card.steered', card_id: fixture.card_id });
});
