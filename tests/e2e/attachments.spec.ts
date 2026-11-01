import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { labWorkspace, groundingCriterion } from './lab';

const question = `Does ALDH1A3 track with insulin loss? ${Date.now()}`;

// The study is deleted once the test ends, pass or fail, so the shared
// workspace's board is left as it was for the specs after this one.
test.afterEach(async ({ page }) => {
  const headers = { 'X-Workspace-ID': await labWorkspace(page.request) };
  const cards = (await (await page.request.get('/api/cards', { headers })).json()) as {
    id: string;
    question: string;
  }[];
  for (const c of cards.filter((c) => c.question === question)) {
    expect((await page.request.delete(`/api/cards/${c.id}`, { headers })).status()).toBe(204);
  }
});

test('attach a CSV to a new study, see it listed, download the same bytes', async ({ page }) => {
  const csv = Buffer.from('gene,expression\nALDH1A3,4.2\nINS,9.9\n');
  await page.goto('/#/studies');
  await page.getByRole('button', { name: 'New', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'New study' });
  await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
  await groundingCriterion(dialog);
  await dialog.getByLabel('Attach files').setInputFiles({
    name: 'expression.csv',
    mimeType: 'text/csv',
    buffer: csv,
  });
  const files = dialog.getByRole('list', { name: 'Files to attach' });
  await expect(files).toContainText('expression.csv');
  await expect(dialog.getByRole('button', { name: 'Create study' })).toBeEnabled();
  await dialog.getByRole('textbox', { name: 'Describe your study' }).press('ControlOrMeta+Enter');
  await expect(dialog).toHaveCount(0);

  await page.getByText(question).first().click();
  await page
    .getByRole('complementary', { name: 'Right pane' })
    .getByRole('button', { name: 'Details', exact: true })
    .click();
  const list = page.getByRole('list', { name: 'Attached files' });
  await expect(list).toContainText('expression.csv');
  // the file names start where the other details' values do
  const file = await list.getByRole('button', { name: 'expression.csv' }).boundingBox();
  const created = await page.locator('.details-body > small:text-is("Created") + .v').boundingBox();
  expect(Math.abs(file!.x - created!.x)).toBeLessThan(1);
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    list.getByRole('button', { name: 'expression.csv' }).click(),
  ]);
  expect(download.suggestedFilename()).toBe('expression.csv');
  expect(readFileSync((await download.path())!).equals(csv)).toBe(true);
});
