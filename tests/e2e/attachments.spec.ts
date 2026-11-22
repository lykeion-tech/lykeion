import { readFileSync } from 'node:fs';
import { expect, test } from './test';
import { labWorkspace, openStudyDetails } from './lab';

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
  await openStudyDetails(page);
  await page
    .getByRole('complementary', { name: 'Right pane' })
    .getByRole('button', { name: 'Properties', exact: true })
    .click();
  const list = page.getByRole('list', { name: 'Attached files' });
  await expect(list).toContainText('expression.csv');
  const filesRow = await list.boundingBox();
  const label = await page.locator('.details-body > small:text-is("Attached files")').boundingBox();
  expect(Math.abs(filesRow!.y - label!.y)).toBeLessThan(1);
  expect(filesRow!.x).toBeGreaterThanOrEqual(label!.x + label!.width);
  // Opening the file previews it in the Files surface, which downloads it.
  await list.getByRole('button', { name: 'expression.csv' }).click();
  const pane = page.getByRole('complementary', { name: 'Right pane' });
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    pane.getByRole('button', { name: 'Download', exact: true }).click(),
  ]);
  expect(download.suggestedFilename()).toBe('expression.csv');
  expect(readFileSync((await download.path())!).equals(csv)).toBe(true);
});
