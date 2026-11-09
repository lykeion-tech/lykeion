import { expect, test, type Page } from '@playwright/test';
import { labWorkspace, newStudy } from './lab';

// The dashboard has no Stop control, so the ending is made through the API.
async function stop(page: Page, cardId: string, reason: string) {
  const headers = { 'X-Workspace-ID': await labWorkspace(page.request) };
  const response = await page.request.post(`/api/cards/${cardId}/stop`, {
    headers,
    data: { reason },
  });
  expect(response.status()).toBeLessThan(300);
}
// The scripted leader stops twice, which files an escalation in the Inbox.
async function escalated(page: Page, cardId: string) {
  const headers = { 'X-Workspace-ID': await labWorkspace(page.request) };
  await expect
    .poll(
      async () =>
        (
          ((await (await page.request.get('/api/decisions', { headers })).json()) ?? []) as {
            card_id: string;
            kind: string;
          }[]
        ).some((i) => i.card_id === cardId && i.kind === 'blocked'),
      { timeout: 30_000 },
    )
    .toBe(true);
}

test('a stopped study shows its reason and loses the composer', async ({ page }) => {
  const study = await newStudy(page.request, `GLP-1 agonists and alcohol intake ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  await escalated(page, study.id);
  await stop(page, study.id, 'no usable data in human cohorts');
  const pane = page.getByRole('complementary', { name: 'Right pane' });
  await pane.getByRole('button', { name: 'Properties', exact: true }).click();
  await expect(pane.getByText('Stopped · no usable data in human cohorts')).toBeVisible();
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveCount(0);
});

test('a stopped study keeps its full long reason inside the metadata pane', async ({ page }) => {
  await page.setViewportSize({ width: 1580, height: 1000 });
  const study = await newStudy(page.request, `Primary liver evidence ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  await escalated(page, study.id);
  const reason = `No usable primary-source evidence after checking mouse liver cohorts. ${'unbroken'.repeat(40)}`;
  await stop(page, study.id, reason);
  const meta = page.getByRole('complementary', { name: 'Right pane' });
  await meta.getByRole('button', { name: 'Properties', exact: true }).click();
  const ending = meta.getByText(`Stopped · ${reason}`, { exact: true });
  await expect(ending).toBeVisible();
  const fits = await ending.evaluate((el) => {
    const pane = el.closest('[aria-label="Right pane"]')!;
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
