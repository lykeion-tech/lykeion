import { expect, test } from '@playwright/test';

test('the Lab Chart page sits above Settings and draws the lab', async ({ page }) => {
  await page.goto('/#/studies');
  const nav = page.getByRole('complementary', { name: 'Workspace navigation' });
  const settings = nav.getByRole('link', { name: 'Settings', exact: true });
  await expect(settings).toBeVisible();
  const above = settings.locator('xpath=preceding::*[self::a or self::button][1]');
  await expect(above).toHaveText('Lab Chart');
  await above.click();
  await expect(page).toHaveURL(/#\/lab-chart$/);
  await expect(page.locator('canvas')).toBeVisible();
  const list = page.getByRole('region', { name: 'Lab Chart rooms' });
  await expect(
    list.locator('.lab-chart-list-room').filter({ has: page.locator('li') }),
  ).not.toHaveCount(0);
  await expect(list.locator('.lab-chart-list-room')).toHaveCount(5);
  // The e2e lab installs a trimmed shipped pack (4 agents per Team); the full 18 is covered server-side.
  await expect(list.locator('.lab-chart-list-room:not(:last-child) li')).toHaveCount(16);
  await expect(page.getByText('Orbit')).toHaveCount(0);
  await expect(page.getByText('Night')).toHaveCount(0);
});
