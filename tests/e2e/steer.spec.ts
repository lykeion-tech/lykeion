import { expect, test, type Page } from '@playwright/test';
import { labWorkspace, newStudy } from './lab';

const stopButton = (page: Page) => page.getByRole('button', { name: 'Stop study', exact: true });
const sendButton = (page: Page) => page.getByRole('button', { name: 'Send', exact: true });

async function actualCard(page: Page, id: string) {
  const response = await page.request.get(`/api/cards/${id}`, {
    headers: { 'X-Workspace-ID': await labWorkspace(page.request) },
  });
  expect(response.status()).toBe(200);
  return response.json();
}

// The lab runs no agents, so the study list reports this study's agents as working.
async function agentsWorking(page: Page, id: string) {
  await page.route('**/api/cards', async (route) => {
    if (route.request().method() !== 'GET') return route.fallback();
    const response = await route.fetch();
    const cards = (await response.json()) as { id: string; agent_running: boolean }[];
    await route.fulfill({
      response,
      json: cards.map((c) => (c.id === id ? { ...c, agent_running: true } : c)),
    });
  });
}

// A cards refetch still in flight when a test ends must not fail it.
test.afterEach(({ page }) => page.unrouteAll({ behavior: 'ignoreErrors' }));

test('Send turns into Stop while agents work, and typing brings Send back', async ({ page }) => {
  const study = await newStudy(page.request, `Working composer ${Date.now()}?`);
  await agentsWorking(page, study.id);
  await page.goto(`/#/studies/${study.id}`);
  await expect(stopButton(page)).toBeVisible();
  // Only the stop icon shows: no text and no tooltip.
  await expect(stopButton(page)).toHaveText('');
  await expect(stopButton(page)).not.toHaveAttribute('title');
  await expect(stopButton(page).locator('svg')).toHaveCount(1);
  await expect(sendButton(page)).toHaveCount(0);
  const comment = page.getByRole('textbox', { name: 'Comment' });
  await comment.fill('try the human cohorts too');
  await expect(sendButton(page)).toBeEnabled();
  await expect(stopButton(page)).toHaveCount(0);
  await comment.fill('');
  await expect(stopButton(page)).toBeVisible();

  // A study whose agents are not working keeps Send.
  const idle = await newStudy(page.request, `Idle composer ${Date.now()}?`);
  await page.goto(`/#/studies/${idle.id}`);
  await expect(page.getByRole('textbox', { name: 'Comment' })).toBeVisible();
  await expect(sendButton(page)).toBeVisible();
  await expect(stopButton(page)).toHaveCount(0);
});

test('Stop ends the study after confirmation and removes the composer', async ({ page }) => {
  const study = await newStudy(page.request, `GLP-1 agonists and alcohol intake ${Date.now()}?`);
  await agentsWorking(page, study.id);
  await page.goto(`/#/studies/${study.id}`);
  page.once('dialog', (d) => d.accept());
  await stopButton(page).click();
  const pane = page.getByRole('complementary', { name: 'Right pane' });
  await pane.getByRole('button', { name: 'Properties', exact: true }).click();
  await expect(pane.locator('.ending-chip')).toHaveText('Stopped');
  await expect(pane.locator('.ending-reason')).toHaveText('Stopped by user');
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveCount(0);
});

test('Stop cancellation sends nothing until confirmation', async ({ page }) => {
  const study = await newStudy(page.request, `Alcohol intake and GLP-1 ${Date.now()}?`);
  let stops = 0;
  page.on('request', (r) => {
    if (r.method() === 'POST' && r.url().endsWith(`/api/cards/${study.id}/stop`)) stops++;
  });
  await agentsWorking(page, study.id);
  await page.goto(`/#/studies/${study.id}`);
  page.once('dialog', async (dialog) => {
    expect(dialog.message()).toContain('Ended studies are never reopened.');
    await dialog.dismiss();
  });
  await stopButton(page).click();
  await expect(stopButton(page)).toBeVisible();
  expect(stops).toBe(0);
  expect((await actualCard(page, study.id)).ending).toBe('');
  page.once('dialog', (dialog) => dialog.accept());
  await stopButton(page).click();
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveCount(0);
  expect(await actualCard(page, study.id)).toMatchObject({
    ending: 'stopped',
    end_reason: 'Stopped by user',
  });
});

test('a stopped study keeps its full long reason inside the right pane', async ({ page }) => {
  await page.setViewportSize({ width: 1580, height: 1000 });
  const study = await newStudy(page.request, `Primary liver evidence ${Date.now()}?`);
  const reason = `No usable primary-source evidence after checking mouse liver cohorts. ${'unbroken'.repeat(40)}`;
  const stopped = await page.request.post(`/api/cards/${study.id}/stop`, {
    headers: { 'X-Workspace-ID': await labWorkspace(page.request) },
    data: { reason },
  });
  expect(stopped.status()).toBeLessThan(300);
  await page.goto(`/#/studies/${study.id}`);
  const meta = page.getByRole('complementary', { name: 'Right pane' });
  await meta.getByRole('button', { name: 'Properties', exact: true }).click();
  const ending = meta.locator('.ending-reason');
  await expect(ending).toHaveText(reason);
  const fits = await ending.evaluate((el) => {
    const pane = el.closest('[aria-label="Right pane"]')!;
    const box = el.getBoundingClientRect();
    const bounds = pane.getBoundingClientRect();
    return {
      insidePane: box.left >= bounds.left && box.right <= bounds.right + 1,
      noHiddenText: el.scrollWidth <= el.clientWidth + 1,
    };
  });
  expect(fits).toEqual({ insidePane: true, noHiddenText: true });
});
