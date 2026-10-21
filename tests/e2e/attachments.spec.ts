import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

test('attach a CSV to a new study, see it listed, download the same bytes', async ({ page }) => {
  const csv = Buffer.from('gene,expression\nALDH1A3,4.2\nINS,9.9\n');
  const question = `Does ALDH1A3 track with insulin loss? ${Date.now()}`;
  await page.goto('/#/studies');
  await page.getByRole('button', { name: 'New', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'New study' });
  await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
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
  const list = page.getByRole('list', { name: 'Attached files' });
  await expect(list).toContainText('expression.csv');
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    list.getByRole('button', { name: 'expression.csv' }).click(),
  ]);
  expect(download.suggestedFilename()).toBe('expression.csv');
  expect(readFileSync((await download.path())!).equals(csv)).toBe(true);
});
