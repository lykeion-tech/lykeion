import { expect, test } from '@playwright/test';
import { installAristotleFixture } from './aristotle-fixture';
import { installFilesFixture } from './coresearch-files-fixture';

test('sidebar New opens the study composer even from Coresearch', async ({ page }) => {
  await installAristotleFixture(page);
  await page.goto('/#/coresearch');
  await page.getByRole('button', { name: 'Show navigation' }).click();
  await page
    .getByRole('complementary', { name: 'Workspace navigation' })
    .getByRole('button', { name: 'New', exact: true })
    .click();
  const composer = page.getByRole('dialog');
  await expect(composer.getByRole('textbox', { name: 'Study title', exact: true })).toBeVisible();
  await expect(page).toHaveURL(/#\/coresearch\/conversations\/welcome$/);
  await expect(composer.locator('.composer-hint')).toHaveCount(0);
});

for (const theme of ['ash', 'midnight']) {
  test(`${theme}: starter and rename retain source focus styles`, async ({ page }) => {
    await installAristotleFixture(page);
    await page.addInitScript(
      (theme) => localStorage.setItem('lykeion.preferences', JSON.stringify({ theme })),
      theme,
    );
    await page.route('**/api/coresearch/projects', (route) =>
      route.fulfill({
        json: [
          {
            id: 'p',
            name: 'Focus project',
            role: 'owner',
            created_by: 'me',
            updated_at: '2026-10-27',
          },
        ],
      }),
    );
    await page.goto('/#/coresearch/new');
    const composer = page.getByRole('textbox', { name: 'Message the agent' });
    await composer.fill('A nonempty draft without a runtime');
    await composer.focus();
    await expect(composer).toHaveCSS('outline-style', 'none');
    await expect(page.getByRole('button', { name: 'Send', exact: true })).toBeDisabled();
    await page.goto('/#/coresearch/projects');
    await page.getByRole('button', { name: 'Rename Focus project' }).click();
    const rename = page.getByRole('textbox', { name: 'Rename Focus project' });
    await expect(rename).toBeFocused();
    await expect(rename).toHaveCSS('outline-style', 'none');
    await page.screenshot({ path: `/tmp/coresearch-final-focus-${theme}.png` });
  });
}
test('Experiment project routes use only the conversation rail and three surfaces', async ({
  page,
}) => {
  await installFilesFixture(page);
  await page.goto('/#/coresearch/p/selected');
  await expect(page.getByRole('complementary', { name: 'Chat', exact: true })).toBeVisible();
  await expect(page.getByRole('complementary', { name: 'Project sidebar' })).toHaveCount(0);
  const surfaces = page.getByRole('button', { name: 'Open experiment surfaces', exact: true });
  await expect(surfaces).toBeVisible();
  await surfaces.click();
  await expect(page.getByRole('heading', { name: 'Open a surface' })).toBeVisible();
  await expect(page.getByRole('button', { name: /^(Notebook|Files|Compute)$/ })).toHaveText([
    'Notebook',
    'Files',
    'Compute',
  ]);
  await expect(page.getByText('Runs', { exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Close pane', exact: true }).click();
  await surfaces.click();
  await page.getByRole('button', { name: 'Compute', exact: true }).click();
  await expect(page.getByRole('tabpanel', { name: 'Compute' })).toBeVisible();
});
