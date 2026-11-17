import { expect, test } from '@playwright/test';
import { labSignIn, labWorkspace } from './lab';

test('real sandboxed Codex proposes a note that is created only after human review', async ({
  page,
  browser,
}) => {
  test.skip(
    process.env.LYK_UI_REAL_CODEX !== '1',
    'opt in to real provider tokens with LYK_UI_REAL_CODEX=1',
  );
  test.setTimeout(240_000);
  const ws = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': ws };
  const me = await (await page.request.get('/api/me')).json();
  await expect
    .poll(
      async () => {
        const runtimes = await (await page.request.get('/api/runtimes', { headers })).json();
        return runtimes.some(
          (r: any) =>
            r.owner_id === me.id &&
            r.harness === 'codex' &&
            r.status === 'online' &&
            !r.unhealthy_reason,
        );
      },
      { timeout: 90_000 },
    )
    .toBe(true);
  const before = await (await page.request.get('/api/decisions', { headers })).json();
  await page.goto('/#/decisions/new');
  await page.getByRole('button', { name: /Create with AI/ }).click();
  const asked = page.waitForResponse(
    (r) => /\/api\/lab\/chats\/[^/]+\/questions$/.test(r.url()) && r.request().method() === 'POST',
  );
  await page
    .getByRole('textbox', { name: 'Question', exact: true })
    .fill(
      'Draft a private reminder titled "Review assay controls" with context "Check the controls before planning the next run." Keep deadline and study empty. This is a wording proposal, not a factual claim: use no evidence and an empty statements array. Do not create a note.',
    );
  await page.getByRole('button', { name: 'Ask', exact: true }).click();
  const receipt = await asked;
  expect(receipt.status()).toBe(202);
  expect(receipt.request().postDataJSON().harness).toBe('codex');
  const chatId = new URL(receipt.url()).pathname.split('/')[4];
  await expect(page.locator('.proposal')).toBeVisible({ timeout: 120_000 });
  await page.getByRole('button', { name: 'Minimize', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'Title', exact: true })).toHaveValue(
    'Review assay controls',
    { timeout: 30_000 },
  );
  await expect(page.getByRole('textbox', { name: 'Context', exact: true })).toHaveValue(
    'Check the controls before planning the next run.',
  );
  const messages = await (
    await page.request.get(`/api/lab/chats/${chatId}/messages`, { headers })
  ).json();
  const answer = messages.messages.find((m: any) => m.kind === 'answer' && m.proposal);
  expect(answer).toBeTruthy();
  expect(answer.agent_run_id).toBeTruthy();
  expect(
    messages.messages.some(
      (m: any) => m.kind === 'question' && m.agent_run_id === answer.agent_run_id,
    ),
  ).toBe(true);
  expect((await (await page.request.get('/api/decisions', { headers })).json()).length).toBe(
    before.length,
  );
  await page
    .getByRole('textbox', { name: 'Title', exact: true })
    .fill('Human-reviewed Codex reminder');
  await page
    .getByRole('textbox', { name: 'Context', exact: true })
    .fill('My reviewed context: inspect controls on Friday.');
  await expect(page.getByRole('status').filter({ hasText: 'Draft saved' })).toBeVisible();
  await page.reload();
  await expect(page.getByRole('textbox', { name: 'Title', exact: true })).toHaveValue(
    'Human-reviewed Codex reminder',
  );
  const minimize = page.getByRole('button', { name: 'Minimize', exact: true });
  if (await minimize.isVisible()) await minimize.click();
  const created = page.waitForResponse(
    (r) => r.url().endsWith('/api/decisions/manual') && r.request().method() === 'POST',
  );
  await page.getByRole('button', { name: 'Create', exact: true }).click();
  const response = await created;
  expect(response.status()).toBe(201);
  const note = await response.json();
  expect(note.title).toBe('Human-reviewed Codex reminder');
  expect(note.context).toBe('My reviewed context: inspect controls on Friday.');
  await expect(page).toHaveURL(new RegExp(`decisions/${note.id}`));
  await expect
    .poll(
      async () =>
        (await (await page.request.get(`/api/lab/chats/${chatId}/draft`, { headers })).json())
          .archived_at,
    )
    .toBeTruthy();
  const archivedPath = '/api/lab/chats?purpose=manual_decision_draft&archived=true';
  const creatorArchive = await page.request.get(archivedPath, { headers });
  expect(creatorArchive.status()).toBe(200);
  const creatorArchiveText = await creatorArchive.text();
  for (const privateValue of [chatId, note.title, note.context]) {
    expect(creatorArchiveText).toContain(privateValue);
  }
  const contexts = [];
  try {
    for (const role of ['member', 'admin']) {
      const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
      contexts.push(context);
      const email = `online-codex-${role}@lab.test`;
      const invitation = await page.request.post(`/api/workspaces/${ws}/invitations`, {
        headers,
        data: { email, role },
      });
      expect(invitation.status()).toBe(201);
      await labSignIn(context.request, email);
      expect(
        (
          await context.request.post(`/api/invitations/${(await invitation.json()).token}/accept`)
        ).status(),
      ).toBe(200);
      for (const path of ['/api/decisions', '/api/decisions?view=history']) {
        const result = await context.request.get(path, { headers });
        expect(result.status()).toBe(200);
        expect(await result.text()).not.toContain(note.id);
      }
      const archived = await context.request.get(archivedPath, { headers });
      expect(archived.status()).toBe(200);
      const archivedText = await archived.text();
      for (const [field, privateValue] of Object.entries({
        chat_id: chatId,
        draft_title: note.title,
        draft_context: note.context,
      })) {
        expect
          .soft(archivedText, `${role} archived list leaked private ${field}`)
          .not.toContain(privateValue);
      }
      expect(
        (await context.request.get(`/api/lab/chats/${chatId}/draft`, { headers })).status(),
      ).toBe(404);
      expect(
        (await context.request.post(`/api/decisions/${note.id}/resolve`, { headers })).status(),
      ).toBe(404);
    }
  } finally {
    for (const context of contexts) await context.close();
  }
  await page.screenshot({
    path: test.info().outputPath('manual-decisions-online-created.png'),
    fullPage: true,
  });
});
