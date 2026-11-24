import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';

const { version } = JSON.parse(
  readFileSync(new URL('../../package.json', import.meta.url), 'utf8'),
);

for (const { renderer, previous } of [
  { renderer: 'web', previous: null },
  { renderer: 'web', previous: '0.0.0' },
  { renderer: 'desktop', previous: null },
]) {
  test(`opens centered release highlights on ${renderer} with ${previous ? 'an older version' : 'no release history'}`, async ({
    page,
  }) => {
    if (renderer === 'desktop')
      await page.addInitScript(() =>
        Object.defineProperty(window, 'lykeionDesktop', { value: {} }),
      );
    await page.goto('/#/studies');
    const { id } = await (await page.request.get('/api/me')).json();
    const key = `lykeion.releaseHighlights:${id}`;
    await page.evaluate(
      ({ key, previous }) => {
        if (previous === null) localStorage.removeItem(key);
        else localStorage.setItem(key, previous);
      },
      { key, previous },
    );
    await page.reload();

    const dialog = page.getByRole('dialog', { name: 'What’s new in Lykeion' });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText(`Version ${version}`, { exact: true })).toBeVisible();
    await expect(dialog.getByRole('heading', { name: 'Lykeion is coming to your phone' })).toBeVisible();
    const bounds = (await dialog.boundingBox())!;
    const viewport = page.viewportSize()!;
    expect(bounds.x + bounds.width / 2).toBeCloseTo(viewport.width / 2, 0);
    expect(bounds.y + bounds.height / 2).toBeCloseTo(viewport.height / 2, 0);

    await dialog.getByRole('button', { name: 'Close release highlights' }).click();
    await expect(dialog).toBeHidden();
    expect(await page.evaluate((key) => localStorage.getItem(key), key)).toBe(version);
    await page.reload();
    await expect(page.getByRole('heading', { name: 'Studies', exact: true })).toBeVisible();
    await expect(dialog).toBeHidden();
  });
}
