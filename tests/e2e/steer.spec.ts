import { expect, test, type Page } from '@playwright/test';
import { labWorkspace, newStudy } from './lab';

const stopButton = (page: Page) => page.getByRole('button', { name: 'Stop study', exact: true });

async function actualCard(page: Page, id: string) {
  const response = await page.request.get(`/api/cards/${id}`, {
    headers: { 'X-Workspace-ID': await labWorkspace(page.request) },
  });
  expect(response.status()).toBe(200);
  return response.json();
}

test('Stop ends the study after confirmation, shows its reason and removes the composer', async ({
  page,
}) => {
  const study = await newStudy(page.request, `GLP-1 agonists and alcohol intake ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  await page.getByRole('textbox', { name: 'Comment' }).fill('no usable data in human cohorts');
  page.once('dialog', (d) => d.accept());
  await stopButton(page).click();
  const pane = page.getByRole('complementary', { name: 'Right pane' });
  await pane.getByRole('button', { name: 'Properties', exact: true }).click();
  await expect(pane.locator('.ending-chip')).toHaveText('Stopped');
  await expect(pane.locator('.ending-reason')).toHaveText('no usable data in human cohorts');
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveCount(0);
});

test('Stop cancellation retains text and sends nothing until confirmation', async ({ page }) => {
  const study = await newStudy(page.request, `Alcohol intake and GLP-1 ${Date.now()}?`);
  let stops = 0;
  page.on('request', (r) => {
    if (r.method() === 'POST' && r.url().endsWith(`/api/cards/${study.id}/stop`)) stops++;
  });
  await page.goto(`/#/studies/${study.id}`);
  await page.getByRole('textbox', { name: 'Comment' }).fill('The recorded data cannot answer it.');
  page.once('dialog', async (dialog) => {
    expect(dialog.message()).toContain('Ended studies are never reopened.');
    await dialog.dismiss();
  });
  await stopButton(page).click();
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveValue(
    'The recorded data cannot answer it.',
  );
  expect(stops).toBe(0);
  expect((await actualCard(page, study.id)).ending).toBe('');
  page.once('dialog', (dialog) => dialog.accept());
  await stopButton(page).click();
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveCount(0);
  expect(await actualCard(page, study.id)).toMatchObject({
    ending: 'stopped',
    end_reason: 'The recorded data cannot answer it.',
  });
});

test('a stopped study keeps its full long reason inside the right pane', async ({ page }) => {
  await page.setViewportSize({ width: 1580, height: 1000 });
  const study = await newStudy(page.request, `Primary liver evidence ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  const reason = `No usable primary-source evidence after checking mouse liver cohorts. ${'unbroken'.repeat(40)}`;
  await page.getByRole('textbox', { name: 'Comment' }).fill(reason);
  page.once('dialog', (d) => d.accept());
  await stopButton(page).click();
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
