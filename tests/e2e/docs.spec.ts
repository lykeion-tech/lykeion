import { expect, test } from '@playwright/test';

const origin = process.env.LYK_DOCS_URL || 'http://127.0.0.1:1440';

test('documentation search works with keyboard, escape and empty results', async ({ page }) => {
  await page.goto(`${origin}/docs/`);
  await page.keyboard.press('Control+k');
  const search = page.getByRole('dialog', { name: 'Search documentation' });
  await expect(search).toBeVisible();
  await expect(search.getByRole('searchbox')).toBeFocused();
  await page.keyboard.type('zzzzzz');
  await expect(search.getByRole('status')).toHaveText('No results. Try another search.');
  await page.keyboard.press('Escape');
  await expect(search).not.toBeVisible();
  await expect(page.getByRole('button', { name: /Search documentation/ })).toBeFocused();
  await page.keyboard.press('Enter');
  await search.getByRole('searchbox').fill('Welcome');
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(`${origin}/docs/welcome/`);
  await expect(page.getByRole('navigation', { name: 'Documentation', exact: true }).getByRole('link', { name: 'Welcome to Lykeion' })).toHaveAttribute('aria-current', 'page');
  await page.getByRole('navigation', { name: 'On this page' }).getByRole('link', { name: 'Open your workspace' }).click();
  await expect(page.locator('#open-your-workspace')).toBeInViewport();
});

test('documentation fits narrow screens with reduced motion', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto(`${origin}/docs/welcome/`);
  await expect(page.getByRole('heading', { name: 'Welcome to Lykeion' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.getByText('Explore documentation', { exact: true }).click();
  await expect(page.getByRole('navigation', { name: 'Documentation', exact: true })).not.toBeVisible();
  await page.getByText('Explore documentation', { exact: true }).click();
  await expect(page.getByRole('navigation', { name: 'Documentation', exact: true })).toBeVisible();
});
