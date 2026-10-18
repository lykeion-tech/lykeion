import { expect, test, type Page } from '@playwright/test';
import { labWorkspace, newStudy } from './lab';

async function choose(page: Page, ...path: string[]) {
  await page.getByRole('button', { name: 'Action' }).click();
  for (const item of path) await page.getByRole('menuitem', { name: item }).click();
}
// The scripted leader stops twice, which files an escalation in the Inbox.
async function escalated(page: Page, cardId: string) {
  const headers = { 'X-Workspace-ID': await labWorkspace(page.request) };
  await expect
    .poll(
      async () =>
        (
          ((await (await page.request.get('/api/inbox', { headers })).json()) ?? []) as {
            card_id: string;
            kind: string;
          }[]
        ).some((i) => i.card_id === cardId && i.kind === 'escalated'),
      { timeout: 30_000 },
    )
    .toBe(true);
}

test('Stop ends the study after confirmation and removes the composer', async ({ page }) => {
  const study = await newStudy(page.request, `GLP-1 agonists and alcohol intake ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  await escalated(page, study.id);
  await choose(page, 'Stop');
  await page.getByRole('textbox', { name: 'Comment' }).fill('no usable data in human cohorts');
  page.once('dialog', (d) => d.accept());
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(
    page
      .getByRole('complementary', { name: 'Study details' })
      .getByText('Stopped · no usable data in human cohorts'),
  ).toBeVisible();
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveCount(0);
});

test('a stopped study keeps its full long reason inside the metadata pane', async ({ page }) => {
  await page.setViewportSize({ width: 1580, height: 1000 });
  const study = await newStudy(page.request, `Primary liver evidence ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  await escalated(page, study.id);
  const reason = `No usable primary-source evidence after checking mouse liver cohorts. ${'unbroken'.repeat(40)}`;
  await choose(page, 'Stop');
  await page.getByRole('textbox', { name: 'Comment' }).fill(reason);
  page.once('dialog', (d) => d.accept());
  await page.getByRole('button', { name: 'Send' }).click();
  const meta = page.getByRole('complementary', { name: 'Study details' });
  const ending = meta.getByText(`Stopped · ${reason}`, { exact: true });
  await expect(ending).toBeVisible();
  const fits = await ending.evaluate((el) => {
    const pane = el.closest('[aria-label="Study details"]')!;
    const chip = el.getBoundingClientRect();
    const bounds = pane.getBoundingClientRect();
    const text = document.createRange();
    text.selectNodeContents(el);
    return {
      chipInsidePane: chip.left >= bounds.left && chip.right <= bounds.right + 1,
      chipInsideViewport: chip.right <= window.innerWidth + 1,
      textInsideChip: [...text.getClientRects()].every(
        (line) =>
          line.left >= chip.left - 1 &&
          line.right <= chip.right + 1 &&
          line.top >= chip.top - 1 &&
          line.bottom <= chip.bottom + 1,
      ),
      noHiddenText: el.scrollWidth <= el.clientWidth + 1 && el.scrollHeight <= el.clientHeight + 1,
    };
  });
  expect(fits).toEqual({
    chipInsidePane: true,
    chipInsideViewport: true,
    textInsideChip: true,
    noHiddenText: true,
  });
});

import { labFixture } from './lab';

const artifacts = '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-17-artifacts';

async function recordedStudy(page: Page) {
  const f = await labFixture(page.request);
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  await page.goto(`/#/studies/${f.card_id}`);
  await expect(page.getByRole('heading', { name: 'Transcript fixture' })).toBeVisible();
  return f;
}
async function actualCard(page: Page, ws: string, id: string) {
  const response = await page.request.get(`/api/cards/${id}`, {
    headers: { 'X-Workspace-ID': ws },
  });
  expect(response.status()).toBe(200);
  return response.json();
}
test('Stop cancellation retains text and sends nothing until confirmation', async ({ page }) => {
  const f = await recordedStudy(page);
  let stops = 0;
  page.on('request', (r) => {
    if (r.method() === 'POST' && r.url().endsWith(`/api/cards/${f.card_id}/stop`)) stops++;
  });
  await choose(page, 'Stop');
  await page.getByRole('textbox', { name: 'Comment' }).fill('The recorded data cannot answer it.');
  page.once('dialog', async (dialog) => {
    expect(dialog.message()).toContain('Ended studies are never reopened.');
    await dialog.dismiss();
  });
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveValue(
    'The recorded data cannot answer it.',
  );
  expect(stops).toBe(0);
  expect((await actualCard(page, f.workspace_id, f.card_id)).ending).toBe('');
  await page.screenshot({ path: `${artifacts}/stop-confirmation-retained.png`, fullPage: true });
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveCount(0);
  expect(await actualCard(page, f.workspace_id, f.card_id)).toMatchObject({
    ending: 'stopped',
    end_reason: 'The recorded data cannot answer it.',
  });
});

test('the action picker offers no Resume', async ({ page }) => {
  await recordedStudy(page);
  await page.getByRole('button', { name: 'Action' }).click();
  const menu = page.getByRole('menu', { name: 'Actions' });
  await expect(menu.getByRole('menuitem', { name: /Stop/ })).toBeVisible();
  await expect(menu.getByRole('menuitem', { name: /Resume/ })).toHaveCount(0);
});
