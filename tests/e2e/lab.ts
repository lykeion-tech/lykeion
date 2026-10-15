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
