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


test('desktop navigation disclosure remains operable by keyboard and pointer', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(`${origin}/docs/`);
  const disclosure = page.locator('.docs-sidebar summary');
  const navigation = page.getByRole('navigation', { name: 'Documentation', exact: true });
  await disclosure.focus();
  await page.keyboard.press('Space');
  await expect(navigation).not.toBeVisible();
  await disclosure.click();
  await expect(navigation).toBeVisible();
  await disclosure.focus();
  await page.keyboard.press('Enter');
  await expect(navigation).not.toBeVisible();
  await page.keyboard.press('Enter');
  await expect(navigation).toBeVisible();
});

test('article actions copy exact Markdown and links, and report clipboard denial', async ({ page }) => {
  const { articles } = await import('../../apps/web/src/app/docs/content');
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: async (text: string) => {
      (window as unknown as { copied: string }).copied = text;
    } }, configurable: true });
  });
  await page.goto(`${origin}/docs/welcome/`);
  const actions = page.getByRole('group', { name: 'Article actions' });
  await actions.locator('summary').focus();
  await page.keyboard.press('Enter');
  await page.keyboard.press('Tab');
  await expect(actions.getByRole('button', { name: 'Copy page', exact: true })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('status')).toHaveText('Page copied.');
  expect(await page.evaluate(() => (window as unknown as { copied: string }).copied)).toBe(articles[0].markdown);
  await actions.getByRole('button', { name: 'Copy Markdown link' }).click();
  expect(await page.evaluate(() => (window as unknown as { copied: string }).copied)).toBe('https://app.lykeion.tech/docs/welcome/markdown/');
  await expect(actions.getByRole('link', { name: 'View Markdown' })).toHaveAttribute('href', 'https://app.lykeion.tech/docs/welcome/markdown/');
  await expect(actions).not.toContainText(/MCP|Cursor|VS Code/);
  await page.evaluate(() => { Object.defineProperty(navigator, 'clipboard', { value: { writeText: async () => { throw new DOMException('Denied', 'NotAllowedError'); } }, configurable: true }); });
  await actions.getByRole('button', { name: 'Copy page', exact: true }).click();
  await expect(page.getByRole('status')).toHaveText('Could not copy. Open View Markdown and copy the text or URL from your browser.');
  await page.screenshot({ path: '.superpowers/sdd/2026-11-16-lykeion-documentation/task-3-actions-desktop.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.screenshot({ path: '.superpowers/sdd/2026-11-16-lykeion-documentation/task-3-actions-mobile.png', fullPage: true });
});
