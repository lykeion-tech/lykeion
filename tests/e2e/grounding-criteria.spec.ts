import { expect, test } from '@playwright/test';
import { labWorkspace } from './lab';

const question = `Which cohorts link lactate to ICU mortality? ${Date.now()}`;

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

test('Grounding criteria are created with the study and listed on its page', async ({ page }) => {
  await page.goto('/#/studies');
  await page.getByRole('button', { name: 'New', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'New study' });
  await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
  await dialog.getByRole('button', { name: 'Grounding criteria · 0', exact: true }).click();
  const criteria = dialog.getByRole('region', { name: 'Grounding criteria' });
  await expect(criteria.getByRole('textbox', { name: 'Criterion 1' })).toHaveValue('');
  await expect(dialog.getByRole('button', { name: /^Create study/ })).toBeDisabled();
  const lines = ['Prospective cohort', 'Adult ICU patients', 'Lactate measured within 6 hours'];
  for (const [i, line] of lines.entries()) {
    if (i) await criteria.getByRole('button', { name: 'Add criterion', exact: true }).click();
    await criteria.getByRole('textbox', { name: `Criterion ${i + 1}` }).fill(line);
  }
  const saved = page.waitForResponse(
    (r) => r.url().endsWith('/api/cards') && r.request().method() === 'POST',
  );
  await dialog.getByRole('button', { name: /^Create study/ }).click();
  const response = await saved;
  expect(response.status(), await response.text()).toBe(201);
  expect((await response.json()).grounding_criteria).toEqual(lines);
  await expect(dialog).toHaveCount(0);

  await page.getByText(question).first().click();
  await page
    .getByRole('complementary', { name: 'Right pane' })
    .getByRole('button', { name: 'Details', exact: true })
    .click();
  const listed = page.locator('ul[aria-label="Grounding criteria"]');
  for (const line of lines) await expect(listed).toContainText(line);
});
