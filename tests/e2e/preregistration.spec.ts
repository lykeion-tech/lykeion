import { expect, test } from '@playwright/test';
import { labWorkspace } from './lab';

const question = `Does the screen hold above 0.9 accuracy? ${Date.now()}`;

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

test('a pre-registered result is created with the study and listed as not decided', async ({
  page,
}) => {
  await page.goto('/#/studies');
  await page.getByRole('button', { name: 'New', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'New study' });
  await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
  const results = dialog.getByRole('region', { name: 'Pre-registered results' });
  await expect(results.getByRole('textbox', { name: 'Result name 1' })).toHaveValue('');
  await expect(dialog.getByRole('button', { name: /^Create study/ })).toBeDisabled();
  await results.getByRole('textbox', { name: 'Result name 1' }).fill('Accurate');
  await results
    .getByRole('textbox', { name: 'Result condition 1' })
    .fill('accuracy >= 0.9 and loss < 1');
  await expect(results).toContainText('The scripts will have to write: accuracy, loss');
  const saved = page.waitForResponse(
    (r) => r.url().endsWith('/api/cards') && r.request().method() === 'POST',
  );
  await dialog.getByRole('button', { name: /^Create study/ }).click();
  const response = await saved;
  expect(response.status(), await response.text()).toBe(201);
  await expect(dialog).toHaveCount(0);

  await page.getByText(question).first().click();
  const listed = page.locator('dl[aria-label="Pre-registered results"]');
  await expect(listed).toContainText('Accurate');
  await expect(listed).toContainText('accuracy >= 0.9 and loss < 1');
  await expect(listed).toContainText('Not decided yet');
});
