import { expect, test, type Page } from './test';
import { installFilesFixture } from './coresearch-files-fixture';

async function openStudies(page: Page) {
  await installFilesFixture(page);
  await page.route(
    (url) => url.pathname === '/api/lab/chats',
    (r) => r.fulfill({ json: { chats: [], next_cursor: '' } }),
  );
  await page.route(
    (url) => url.pathname === '/api/runtimes',
    (r) =>
      r.fulfill({
        json: [
          {
            id: 'machine',
            owner_id: 'me',
            status: 'online',
            harness: 'claude',
            models: Array.from({ length: 32 }, (_, i) => ({
              label: 'Model ' + i,
              value: String(i),
            })),
          },
        ],
      }),
  );
  await page.goto('/#/studies');
  await expect(page.getByRole('button', { name: 'Ask Aristotle', exact: true })).toBeVisible();
}

test('model and context shortcuts focus their controls and Escape restores the invoking control', async ({
  page,
}) => {
  await openStudies(page);
  const outside = page.getByRole('link', { name: 'Coresearch', exact: true });
  await outside.focus();
  await page.keyboard.press('Control+Shift+m');
  await expect(page.getByRole('button', { name: /Claude Code · Default/ })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(outside).toBeFocused();
  const composer = page.getByRole('textbox', { name: 'Question' });
  await composer.focus();
  await page.keyboard.type('@');
  await expect(page.getByRole('searchbox', { name: 'Search context' })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(composer).toBeFocused();
});

test('long model menus scroll and stay inside a 375px-high viewport after moving the window', async ({
  page,
}) => {
  await page.setViewportSize({ width: 800, height: 375 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await openStudies(page);
  await page.keyboard.press('Control+j');
  const model = page.getByRole('button', { name: 'Select model', exact: true });
  const grip = page.getByRole('button', { name: /Drag to move/ });
  for (const side of ['above', 'below']) {
    if (side === 'below') {
      const before = await grip.boundingBox();
      await page.mouse.move(before!.x + 12, before!.y + 12);
      await page.mouse.down();
      await page.mouse.move(before!.x + 12, 16, { steps: 8 });
      await page.mouse.up();
    }
    await model.click();
    const menu = page.locator('.lab-window .menu');
    await expect(menu.getByRole('button', { name: 'Model 31', exact: true })).toBeAttached();
    const m = (await menu.boundingBox())!,
      a = (await model.boundingBox())!;
    expect(m.y).toBeGreaterThanOrEqual(5);
    expect(m.y + m.height).toBeLessThanOrEqual(370);
    if (side === 'above') expect(m.y + m.height).toBeLessThan(a.y);
    else expect(m.y).toBeGreaterThan(a.y + a.height);
    expect(await menu.evaluate((e) => e.scrollHeight > e.clientHeight)).toBe(true);
    await menu.getByRole('button', { name: 'Model 31', exact: true }).scrollIntoViewIfNeeded();
    await page.keyboard.press('Escape');
    await expect(model).toBeFocused();
  }
});
