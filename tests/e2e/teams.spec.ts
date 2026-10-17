import { expect, test } from '@playwright/test';

test('the sidebar shows Teams between Studies and Colleagues', async ({ page }) => {
  await page.goto('/#/studies');
  const nav = page.getByRole('complementary', { name: 'Workspace navigation' });
  const names = await nav.getByRole('link').allInnerTexts();
  const at = (name: string) => names.findIndex((n) => n.trim() === name);
  expect(at('Teams')).toBeGreaterThan(at('Studies'));
  expect(at('Teams')).toBeLessThan(at('Colleagues'));
  await nav.getByRole('link', { name: 'Teams', exact: true }).click();
  await expect(page).toHaveURL(/#\/teams$/);
  await expect(page.getByRole('heading', { name: 'Teams', level: 1 })).toBeVisible();
  await expect(nav.getByRole('link', { name: 'Teams', exact: true }).locator('svg')).toHaveClass(
    /lucide-network/,
  );
  await expect(page.locator('#page-header-count')).toHaveText('');
});

test('Teams shows the empty state when nothing is shipped or uploaded', async ({ page }) => {
  await page.route('**/api/packs/catalog', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }),
  );
  await page.goto('/#/teams');
  const empty = page.locator('.directory-empty');
  await expect(empty.getByRole('heading', { name: 'No Teams yet' })).toBeVisible();
  await expect(empty).toContainText('A Team is the agents that work each stage of your studies.');
  await expect(empty.locator('svg')).toHaveClass(/lucide-network/);
  const chooser = page.waitForEvent('filechooser');
  await empty.getByRole('button', { name: 'Upload a Team' }).click();
  await chooser;
});

