import { execFileSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { expect, type APIRequestContext } from '@playwright/test';

export const LAB = 'http://127.0.0.1:18080';
export const EMAIL = 'ana@lab.test';

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
// pass assignee: null to leave it unassigned in Backlog.
export async function newStudy(
  request: APIRequestContext,
  question: string,
  extra: Record<string, unknown> = {},
): Promise<{ id: string; number: number; title: string }> {
  const workspaceId = await labWorkspace(request);
  const data: Record<string, unknown> = { question, ...extra };
  if (data.assignee === null) delete data.assignee;
  else if (!data.assignee)
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
function demoPackAt(
  dir: string,
  name: string,
  version: string,
  extra: Record<string, string> = {},
) {
  cpSync('tests/e2e/fixtures/demo-pack', dir, { recursive: true });
  const manifest = JSON.parse(readFileSync(join(dir, 'pack.json'), 'utf8'));
  writeFileSync(join(dir, 'pack.json'), JSON.stringify({ ...manifest, name, version, ...extra }));
}

// The demo Team zipped with the system zip command, named name.
export function labPackArchive(
  name = 'ui-demo',
  version = '1',
  extra: Record<string, string> = {},
): Buffer {
  const dir = mkdtempSync(join(tmpdir(), 'lyk-pack-'));
  try {
    demoPackAt(join(dir, 'pack'), name, version, extra);
    const archive = join(dir, 'pack.zip');
    execFileSync('zip', ['-qr', archive, '.', '-x', '.*', '*/.*'], { cwd: join(dir, 'pack') });
    return readFileSync(archive);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
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

// Uploads the demo Team and approves its version for the lab person.
export async function labPack(
  request: APIRequestContext,
  name = 'ui-demo',
  workspaceId?: string,
  extra: { domain?: string; stage?: string } = {},
): Promise<{ id: string; name: string }> {
  const headers = { 'X-Workspace-ID': workspaceId ?? (await labWorkspace(request)) };
  const upload = await request.post('/api/packs', {
    headers: { ...headers, 'Content-Type': 'application/zip' },
    data: labPackArchive(name, '1', extra),
  });
  if (upload.status() === 409) {
    // An earlier spec in this run already uploaded and approved this version.
    const versions = await request.get(`/api/packs?name=${name}`, { headers });
    return ((await versions.json()) as { id: string; name: string }[])[0]!;
  }
  expect(upload.status()).toBe(201);
  const pack = (await upload.json()) as { id: string; name: string };
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
