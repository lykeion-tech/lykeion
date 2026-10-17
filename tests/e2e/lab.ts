import { execFileSync } from 'node:child_process';
import { cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
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
  gate_id: string;
  long_reason: string;
  pass_threshold: number;
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

// Creates a study through the API, as another person's browser would.
export async function newStudy(
  request: APIRequestContext,
  question: string,
  extra: Record<string, unknown> = {},
): Promise<{ id: string; number: number; title: string }> {
  const res = await request.post('/api/cards', {
    headers: { 'X-Workspace-ID': await labWorkspace(request) },
    data: { question, ...extra },
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
function demoPackAt(dir: string, name: string, version: string) {
  cpSync('tests/e2e/fixtures/demo-pack', dir, { recursive: true });
  const manifest = JSON.parse(readFileSync(join(dir, 'pack.json'), 'utf8'));
  writeFileSync(join(dir, 'pack.json'), JSON.stringify({ ...manifest, name, version }));
}

// The demo Team zipped with the system zip command, named name.
export function labPackArchive(name = 'ui-demo', version = '1'): Buffer {
  const dir = mkdtempSync(join(tmpdir(), 'lyk-pack-'));
  try {
    demoPackAt(join(dir, 'pack'), name, version);
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

// Uploads the demo Team as the lab person's own Team, approves it and makes it
// their Team. Its Validation check never passes. labNoTeam undoes the choice.
export async function labPack(request: APIRequestContext): Promise<{ id: string; name: string }> {
  const headers = { 'X-Workspace-ID': await labWorkspace(request) };
  const upload = await request.post('/api/packs', {
    headers: { ...headers, 'Content-Type': 'application/zip' },
    data: labPackArchive(),
  });
  let pack: { id: string; name: string };
  if (upload.status() === 409) {
    // An earlier spec in this run already uploaded and approved this version.
    const versions = await request.get('/api/packs?name=ui-demo', { headers });
    pack = ((await versions.json()) as { id: string; name: string }[])[0]!;
  } else {
    expect(upload.status()).toBe(201);
    pack = (await upload.json()) as { id: string; name: string };
    expect((await request.post(`/api/packs/${pack.id}/approve`, { headers })).status()).toBe(200);
  }
  const team = await request.put('/api/me/pack', { headers, data: { name: pack.name } });
  expect(team.status()).toBe(200);
  return pack;
}

export async function labNoTeam(request: APIRequestContext) {
  expect((await request.post(`${LAB}/lab/no-team`)).status()).toBe(204);
}

// Revises a study's question as a stage would, citing a new result that says why.
export async function labRevise(
  request: APIRequestContext,
  cardId: string,
  text: string,
  why: string,
) {
  expect(
    (await request.post(`${LAB}/lab/revise/${cardId}`, { data: { text, why } })).status(),
  ).toBe(204);
}

// Releases a study, as a sealed report would.
export async function labRelease(request: APIRequestContext, cardId: string) {
  expect((await request.post(`${LAB}/lab/release/${cardId}`)).status()).toBe(204);
}
