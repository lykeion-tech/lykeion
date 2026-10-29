import { expect, test } from '@playwright/test';
import { labWorkspace } from './lab';

test('search opens with researches and standalone and nested studies, previews and navigates', async ({
  page,
}) => {
  const workspace = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': workspace };
  const researchResponse = await page.request.post('/api/researches', {
    headers,
    data: { name: 'Palette metabolism research', description: '' },
  });
  expect(researchResponse.status()).toBe(201);
  const research = await researchResponse.json();
  const studies = [];
  for (const [title, research_id] of [
    ['Palette standalone study', null],
    ['Palette nested study', research.id],
  ] as const) {
    const response = await page.request.post('/api/cards', {
      headers,
      data: { title, question: title, research_id, predicates: [{ name: 'effect', text: 'effect > 0' }], },
    });
    expect(response.status()).toBe(201);
    studies.push(await response.json());
  }
  await page.goto('/#/studies');
  const search = page.getByRole('button', { name: 'Search', exact: true });
  await search.click();
  const dialog = page.getByRole('dialog', { name: 'Command palette' });
  const input = dialog.getByRole('combobox');
  await expect(input).toBeFocused();
  await expect(input).toHaveValue('');
  await expect(dialog.getByRole('option', { name: /Palette metabolism research/ })).toBeVisible();
  await expect(dialog.getByRole('option', { name: /Palette standalone study/ })).toBeVisible();
  await expect(dialog.getByRole('option', { name: /Palette nested study/ })).toBeVisible();
  await expect(dialog.getByRole('option').first()).toHaveAttribute('aria-selected', 'true');
  await expect(dialog).toHaveCSS('width', '720px');
  await expect(input).toHaveCSS('height', '46px');
  await expect(dialog.locator('.palette-preview')).toHaveCSS('width', '280px');
  await expect.poll(async () => (await dialog.boundingBox())!.y).toBeCloseTo(982 * 0.12, 0);
  await input.fill('Palette');
  await expect(dialog.getByRole('option')).toHaveCount(3);
  await input.press('ArrowDown');
  await expect(dialog.locator('.palette-preview-subtitle')).toHaveText('Palette nested study');
  await expect(dialog.locator('.palette-preview')).toContainText('Palette metabolism research');
  await input.press('ArrowDown');
  await expect(dialog.locator('.palette-preview-subtitle')).toHaveText('Palette standalone study');
  await input.press('ArrowUp');
  await page.screenshot({ path: 'test-results/search-palette/populated.png' });
  await input.press('Enter');
  await expect(page).toHaveURL(new RegExp(`#/studies/${studies[1].id}$`));
  await expect(dialog).toHaveCount(0);
  await expect(search).toBeFocused();

  await page.keyboard.press('Control+k');
  await expect(input).toHaveValue('');
  await input.fill('does-not-match-anything');
  await expect(dialog.getByRole('option')).toHaveCount(0);
  await expect(dialog).toContainText('No matching results');
  await input.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(search).toBeFocused();

  await search.click();
  await dialog.getByRole('option', { name: /Palette metabolism research/ }).hover();
  await expect(dialog.locator('.palette-preview-title')).toHaveText('Palette metabolism research');
  await dialog.getByRole('option', { name: /Palette metabolism research/ }).click();
  await expect(page).toHaveURL(new RegExp(`#/projects/${research.id}$`));
  await expect(dialog).toHaveCount(0);

  await page.setViewportSize({ width: 560, height: 800 });
  await page.keyboard.press('Control+k');
  await expect(dialog).toHaveCSS('width', '528px');
  await expect(dialog.locator('.palette-preview')).toBeHidden();
});
