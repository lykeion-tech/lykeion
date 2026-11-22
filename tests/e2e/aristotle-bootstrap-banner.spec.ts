import { expect, test } from './test';
import { installFilesFixture } from './coresearch-files-fixture';

test('a failed Aristotle bootstrap does not show an error banner on Projects', async ({ page }) => {
  await installFilesFixture(page);
  await page.route('**/api/coresearch/bootstrap', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'synthetic bootstrap failure' }),
    }),
  );
  await page.goto('/#/coresearch');
  await expect(
    page.getByRole('alert').filter({ hasText: 'synthetic bootstrap failure' }),
  ).toBeVisible();
  await page.evaluate(() => {
    window.location.hash = '/projects';
  });
  await expect(page.getByRole('heading', { name: 'Projects', exact: true })).toBeVisible();
  await expect(
    page.getByRole('alert').filter({ hasText: 'synthetic bootstrap failure' }),
  ).toHaveCount(0);
});
