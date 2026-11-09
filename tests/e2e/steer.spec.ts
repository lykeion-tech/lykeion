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
test('a stopped study shows Stopped in Properties and loses the composer', async ({ page }) => {
  const study = await newStudy(page.request, `GLP-1 agonists and alcohol intake ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  await stop(page, study.id, 'no usable data in human cohorts');
  const pane = page.getByRole('complementary', { name: 'Right pane' });
  await pane.getByRole('button', { name: 'Properties', exact: true }).click();
  await expect(pane.locator('.ending-chip')).toHaveText('Stopped');
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveCount(0);
});
