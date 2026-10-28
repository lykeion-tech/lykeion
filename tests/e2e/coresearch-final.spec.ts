import { expect, test } from '@playwright/test';
import { installFilesFixture, recorded } from './coresearch-files-fixture';
for (const theme of ['ash', 'midnight']) {
  test(`${theme}: starter and rename retain source focus styles`, async ({ page }) => {
    await installFilesFixture(page);
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
    await page.goto('/#/coresearch');
    await page.getByRole('button', { name: 'Rename Focus project' }).click();
    const rename = page.getByRole('textbox', { name: 'Rename Focus project' });
    await expect(rename).toBeFocused();
    await expect(rename).toHaveCSS('outline-style', 'none');
    await page.screenshot({ path: `/tmp/coresearch-final-focus-${theme}.png` });
  });
}
test('failed run without files exposes inert diagnostics and blank owners fall back', async ({
  page,
}) => {
  await installFilesFixture(page);
  await page.route('**/api/workspaces/lab/members', (route) =>
    route.fulfill({
      json: [{ user_id: 'me', name: '  ', email: 'ada@example.test', role: 'owner' }],
    }),
  );
  await page.route('**/api/coresearch/projects/p/runs', (route) =>
    route.fulfill({
      json: [
        recorded({
          status: 'failed',
          facts: {
            error: 'Interpreter failed',
            stdout_tail: 'before failure',
            stderr_tail: '<img src=x onerror="window.bad=true">',
          },
        }),
      ],
    }),
  );
  await page.goto('/#/coresearch/p/selected');
  await expect(page.getByRole('complementary', { name: 'Project sidebar' })).toContainText(
    'ada@example.test',
  );
  await page.getByRole('button', { name: 'Runs', exact: true }).click();
  await expect(page.getByRole('tabpanel', { name: 'Runs' })).toContainText('Interpreter failed');
  await page.getByRole('button', { name: 'Execution log R1' }).click();
  await expect(page.getByLabel('Log R1', { exact: true })).toContainText('before failure');
  await expect(page.getByLabel('Log R1', { exact: true })).toContainText('<img src=x');
  await expect(page.getByLabel('Log R1', { exact: true }).locator('img')).toHaveCount(0);
});
