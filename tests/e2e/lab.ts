import { execFileSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import {
  expect,
  request as apiRequest,
  type APIRequestContext,
  type Browser,
  type Page,
  type WebSocketRoute,
} from '@playwright/test';

export const LAB = `http://${process.env.LYK_UI_LAB_ADDR || '127.0.0.1:18080'}`;
export const EMAIL = 'ana@lab.test';

export async function chooseCompact(page: Page, label: string, choice: string | number) {
  await page.getByRole('combobox', { name: label, exact: true }).click();
  const options = page.getByRole('listbox', { name: label, exact: true }).getByRole('option');
  await (
    typeof choice === 'number' ? options.nth(choice) : options.filter({ hasText: choice }).first()
  ).click();
}

export async function labCode(request: APIRequestContext, email: string): Promise<string> {
  const res = await request.get(`${LAB}/lab/code?email=${encodeURIComponent(email)}`);
  expect(res.status()).toBe(200);
  return res.text();
}

export interface UILabFixture {
  workspace_id: string;
  card_id: string;
  run_id: string;
}

// Uses the shipped email-code route and cookie, including for a second person.
export async function labSignIn(request: APIRequestContext, email = EMAIL): Promise<void> {
  const owner = await apiRequest.newContext({ storageState: 'test-results/ui-lab/session.json' });
  try {
    expect((await owner.post(`${LAB}/lab/auth-ip/reset`)).status()).toBe(204);
  } finally {
    await owner.dispose();
  }
  expect((await request.post('/auth/send-code', { data: { email } })).status()).toBe(204);
  const code = await labCode(request, email);
  expect((await request.post('/auth/verify-code', { data: { email, code } })).status()).toBe(200);
}

// Returns a fresh disposable workspace owned by EMAIL. Application reads still
// go through normal authentication, membership checks and workspace scoping.
export async function labFixture(request: APIRequestContext): Promise<UILabFixture> {
  const res = await request.post(`${LAB}/lab/fixtures`);
  expect(res.status()).toBe(201);
  return res.json();
}

// The id of the workspace the setup project created.
export async function labWorkspace(request: APIRequestContext): Promise<string> {
  const list = (await (await request.get('/api/workspaces')).json()) as {
    id: string;
    slug: string;
  }[];
  const ws = list.find((w) => w.slug === 'beta-cell-lab');
  expect(ws).toBeTruthy();
  return ws!.id;
}

// Adds the demo Team for the lab person in a workspace and returns its id.
export async function labTeam(request: APIRequestContext, workspaceId: string): Promise<string> {
  const pack = await labPack(request, 'ui-demo', workspaceId);
  const headers = { 'X-Workspace-ID': workspaceId };
  expect((await request.post(`/api/teams/${pack.id}/add`, { headers })).status()).toBe(200);
  return pack.id;
}

// Creates a study through the API, as another person's browser would. It is
// assigned the demo Team for Grounding, so the lab's scripted agent works it;
// pass assignee: null for a study nobody works, which the lab seeds directly.
export async function newStudy(
  request: APIRequestContext,
  question: string,
  extra: Record<string, unknown> = {},
): Promise<{ id: string; number: number; title: string }> {
  const workspaceId = await labWorkspace(request);
  const data: Record<string, unknown> = { question, ...extra };
  if (data.assignee === null) {
    const res = await request.post(`${LAB}/lab/idle-study`, {
      headers: { 'X-Workspace-ID': workspaceId },
      data: { title: data.title ?? '', question },
    });
    expect(res.status()).toBe(201);
    return res.json();
  }
  if (!data.assignee)
    data.assignee = {
      type: 'team',
      teams: { grounding: await labTeam(request, workspaceId) },
    };
  const res = await request.post('/api/cards', {
    headers: { 'X-Workspace-ID': workspaceId },
    data,
  });
  expect(res.status()).toBe(201);
  return res.json();
}

// Gives a study n evidence records in its current column and round, each with a
// long claim ending in an unbroken 300-character token.
export async function labRecords(request: APIRequestContext, cardId: string, n: number) {
  expect((await request.post(`${LAB}/lab/records/${cardId}?n=${n}`)).status()).toBe(204);
}

// Puts a study in Hypotheses, which has no roles yet, so it can be sent back.
export async function labHypotheses(request: APIRequestContext, cardId: string) {
  expect((await request.post(`${LAB}/lab/hypotheses/${cardId}`)).status()).toBe(204);
}

// The folder the lab reads its shipped Teams from, live, one folder per name.
export const SHIPPED = resolve('test-results/ui-lab/shipped');

// Copies the demo Team in fixtures/demo-pack to dir under another name and
// version.
export function demoPackAt(
  dir: string,
  name: string,
  version: string,
  extra: Record<string, string> = {},
) {
  cpSync('tests/e2e/fixtures/demo-pack', dir, { recursive: true });
  const manifest = JSON.parse(readFileSync(join(dir, 'pack.json'), 'utf8'));
  writeFileSync(join(dir, 'pack.json'), JSON.stringify({ ...manifest, name, version, ...extra }));
}

// Makes the lab ship the demo Team as name at version, replacing any version
// it shipped before. labUnship stops shipping it.
export function labShip(name: string, version: string) {
  labUnship(name);
  demoPackAt(join(SHIPPED, name), name, version);
}

export function labUnship(name: string) {
  rmSync(join(SHIPPED, name), { recursive: true, force: true });
}

const stageNames = ['grounding', 'hypotheses', 'experimentation', 'validation'];
export const builtInTeams = stageNames.map((s) => `${s[0]!.toUpperCase()}${s.slice(1)} Team`);

// Built-in Teams install when a workspace is created, so the lab ships four
// test-only stage Teams just while create makes one; workspaces made without
// it have no built-in Teams.
export async function withBuiltInTeams<T>(create: () => Promise<T>): Promise<T> {
  stageNames.forEach((stage, i) =>
    demoPackAt(join(SHIPPED, builtInTeams[i]!), builtInTeams[i]!, '1', { domain: '', stage }),
  );
  try {
    return await create();
  } finally {
    builtInTeams.forEach(labUnship);
  }
}

// Installs the fixed demo Team as pre-existing lab data and approves its version.
export async function labPack(
  request: APIRequestContext,
  name = 'ui-demo',
  workspaceId?: string,
  extra: { domain?: string; stage?: string } = {},
): Promise<{ id: string; name: string }> {
  const headers = { 'X-Workspace-ID': workspaceId ?? (await labWorkspace(request)) };
  const installed = await request.post(`${LAB}/lab/installed-teams`, {
    headers,
    data: { name, ...extra },
  });
  expect(installed.status()).toBe(201);
  const pack = (await installed.json()) as { id: string; name: string };
  expect((await request.post(`/api/packs/${pack.id}/approve`, { headers })).status()).toBe(200);
  return pack;
}

// Puts an Inbox item of a kind on a study, as the flow would write it.
export async function labInbox(
  request: APIRequestContext,
  cardId: string,
  kind: string,
  text: string,
) {
  expect(
    (await request.post(`${LAB}/lab/inbox/${cardId}`, { data: { kind, text } })).status(),
  ).toBe(204);
}

export interface FlowRecord {
  kind: string;
  key?: string;
  body: Record<string, unknown>;
}

// Puts a study in a column and state and writes the flow's records into it:
// scores, steps, an audit, calls. Seed an unassigned study so no Team works it.
export async function labFlow(
  request: APIRequestContext,
  cardId: string,
  flow: {
    column: string;
    state: string;
    block_kind?: string;
    redos?: number;
    round?: number;
    records?: FlowRecord[];
  },
) {
  const res = await request.post(`${LAB}/lab/flow-records/${cardId}`, {
    data: { round: 1, ...flow },
  });
  expect(res.status()).toBe(204);
}

export function labSkillArchive(name: string): Buffer {
  const dir = mkdtempSync(join(tmpdir(), 'lyk-skill-'));
  try {
    mkdirSync(join(dir, 'references'));
    mkdirSync(join(dir, 'scripts'));
    writeFileSync(
      join(dir, 'SKILL.md'),
      `---\nname: ${name}\ndescription: Complete folder.\n---\n\n# Complete folder\n`,
    );
    writeFileSync(join(dir, 'references/raw.bin'), Buffer.from([0, 255, 128, 13, 10]));
    writeFileSync(join(dir, 'scripts/check.py'), 'print("never executed")');
    const archive = join(tmpdir(), `${name}.zip`);
    try {
      execFileSync('zip', ['-qr', archive, '.'], { cwd: dir });
      return readFileSync(archive);
    } finally {
      rmSync(archive, { force: true });
    }
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

export function labSkillFolder(name: string): string {
  const dir = mkdtempSync(join(tmpdir(), 'lyk-skill-folder-'));
  mkdirSync(join(dir, 'references'));
  mkdirSync(join(dir, 'scripts'));
  writeFileSync(
    join(dir, 'SKILL.md'),
    `---\nname: ${name}\ndescription: Folder with raw bytes.\n---\n\n# Folder skill\n`,
  );
  writeFileSync(join(dir, 'references/raw.bin'), Buffer.from([0, 255, 128, 13, 10]));
  writeFileSync(join(dir, 'scripts/check.py'), 'print("never executed")');
  writeFileSync(join(dir, '.hidden'), 'hidden supporting file');
  return dir;
}

export const fullAutoresearchReport = `# Full research report\n\n${'Recorded evidence beyond the Inbox excerpt.\n\n'.repeat(80)}<script>window.reportExecuted = true</script>\n<img src=x onerror="window.reportExecuted = true">\n\n[unsafe](javascript:alert(1)) [local](file:///etc/passwd) [app](codex://settings)\n\nend-of-full-report`;

export async function labAutoresearchRun(
  request: APIRequestContext,
  workspaceId: string,
  autoresearchId: string,
  data: {
    run_id?: string;
    status: string;
    report?: string;
    reason?: string;
    card_id?: string;
    inbox?: boolean;
  },
): Promise<string> {
  const response = await request.post(`${LAB}/lab/autoresearch/${autoresearchId}`, {
    data: { workspace_id: workspaceId, ...data },
  });
  expect(response.status(), await response.text()).toBe(200);
  return ((await response.json()) as { run_id: string }).run_id;
}

export async function labReportAutoresearch(request: APIRequestContext, mode = 'run_only') {
  const fixture = await labFixture(request);
  const headers = { 'X-Workspace-ID': fixture.workspace_id };
  const agentResponse = await request.post('/api/agents', {
    headers,
    data: { name: 'Report watcher', stage: 'experimentation', harness: 'claude' },
  });
  expect(agentResponse.status()).toBe(201);
  const agent = await agentResponse.json();
  const response = await request.post('/api/autoresearch', {
    headers,
    data: {
      title: 'Report literature watch',
      description: 'Read sources and report.',
      execution_mode: mode,
      assignee: { type: 'agent', id: agent.id },
      scope: { from: 'experimentation', to: 'experimentation' },
    },
  });
  expect(response.status()).toBe(201);
  return {
    ...fixture,
    agent_id: agent.id as string,
    autoresearch_id: ((await response.json()) as { id: string }).id,
  };
}

// A colleague signs in and accepts a real workspace invitation in an isolated
// cookie jar. It never inherits the setup owner's session.
export async function labMember(
  browser: Browser,
  owner: APIRequestContext,
  workspace: string,
  role: 'member' | 'admin' = 'member',
) {
  const email = `realtime-${Date.now()}-${Math.random().toString(16).slice(2)}@lab.test`;
  const invitation = await owner.post(`/api/workspaces/${workspace}/invitations`, {
    headers: { 'X-Workspace-ID': workspace },
    data: { email, role },
  });
  expect(invitation.status()).toBe(201);
  const { token } = await invitation.json();
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  await labSignIn(context.request, email);
  expect((await context.request.patch('/api/me', { data: { onboarded: true } })).status()).toBe(
    200,
  );
  expect((await context.request.post(`/api/invitations/${token}/accept`)).status()).toBe(200);
  const me = await (await context.request.get('/api/me')).json();
  const page = await context.newPage();
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), workspace);
  return { context, page, userId: me.id as string };
}

// The real browser/server socket stays transparent: connectToServer forwards
// every frame unchanged. Only the transport can be closed/held for a network
// interruption; state and replay always come from the application's /ws route.
export async function labRealtime(page: Page) {
  let subscriptions = 0;
  const events: string[] = [];
  let active: { browser: WebSocketRoute; server: WebSocketRoute } | undefined;
  let gate: Promise<void> | undefined;
  page.on('websocket', (socket) =>
    socket.on('framesent', ({ payload }) => {
      const frame = JSON.parse(String(payload));
      if (frame.type === 'subscribe') subscriptions++;
    }),
  );
  page.on('websocket', (socket) =>
    socket.on('framereceived', ({ payload }) => {
      const frame = JSON.parse(String(payload));
      if (frame.type === 'event') events.push(frame.event.type);
    }),
  );
  await page.routeWebSocket('**/ws', async (browser) => {
    if (gate) await gate;
    active = { browser, server: browser.connectToServer() };
  });
  return {
    events: () => [...events],
    subscribed: (count = 1) => expect.poll(() => subscriptions).toBe(count),
    async pause() {
      let release!: () => void;
      gate = new Promise<void>((resolve) => {
        release = resolve;
      });
      const current = active!;
      await current.server.close({ code: 1001, reason: 'Transport interruption' });
      await current.browser.close({ code: 1001, reason: 'Transport interruption' });
      return () => {
        gate = undefined;
        release();
      };
    },
  };
}

// Agent runs are cards on the study page; a run's transcript opens in a dialog.
export async function openAgentTranscript(page: Page, index = 0) {
  await page
    .locator('.agent-card')
    .nth(index)
    .getByRole('button', { name: /^Open transcript:/ })
    .click({ timeout: 30_000 });
  const dialog = page.locator('dialog.agent-run-dialog');
  await expect(dialog).toBeVisible();
  return dialog;
}
