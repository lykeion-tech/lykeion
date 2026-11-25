import { expect, test } from './test';
import { installAristotleFixture } from './aristotle-fixture';
import { installFilesFixture } from './coresearch-files-fixture';

test('New study starts with no explicit stage selection', async ({ page }) => {
  await installFilesFixture(page);
  await page.goto('/#/studies');
  await page.getByRole('button', { name: 'New', exact: true }).click();
  const composer = page.getByRole('dialog', { name: 'New study' });
  const stages = composer.getByRole('menu', { name: 'Study stages' });
  await composer.getByRole('button', { name: 'Stages', exact: true }).click();
  await expect(stages.getByRole('menuitemcheckbox', { checked: false })).toHaveCount(4);
  await stages.getByRole('menuitemcheckbox', { name: 'Hypotheses' }).click();
  await expect(stages.getByRole('menuitemcheckbox', { checked: true })).toHaveText(['Hypotheses']);
  const pill = composer.getByRole('button', { name: 'Stages: Hypotheses', exact: true });
  await pill.click();
  await pill.click();
  await expect(stages.getByRole('menuitemcheckbox', { checked: true })).toHaveText(['Hypotheses']);
  await stages.getByRole('menuitemcheckbox', { name: 'Grounding' }).click();
  await expect(stages.getByRole('menuitemcheckbox', { checked: true })).toHaveText([
    'Grounding',
    'Hypotheses',
  ]);
});

test('sidebar New opens the study composer even from Coresearch', async ({ page }) => {
  await installAristotleFixture(page);
  await page.goto('/#/coresearch');
  await page.locator('.sidebar-brand:visible [aria-current=page]').click();
  await page
    .getByRole('complementary', { name: 'Workspace navigation' })
    .getByRole('button', { name: 'New', exact: true })
    .click();
  const composer = page.getByRole('dialog');
  await expect(composer.getByRole('textbox', { name: 'Study title', exact: true })).toBeVisible();
  await expect(page).toHaveURL(/#\/coresearch\/conversations\/welcome$/);
  const bestPractices = composer.getByText('Best practices', { exact: true });
  await expect(bestPractices).toBeVisible();
  const dataTip = composer.getByText(/attach it as a file so Hypotheses can cite it/);
  await expect(dataTip).toBeHidden();
  await bestPractices.focus();
  await bestPractices.press('Enter');
  await expect(dataTip).toBeVisible();
  await expect(
    composer.getByRole('listitem').filter({ hasText: /Validation needs a recorded statistical test/ }),
  ).toBeVisible();
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
  await page.getByRole('button', { name: 'Close experiment surfaces', exact: true }).click();
  await surfaces.click();
  await page.getByRole('button', { name: 'Compute', exact: true }).click();
  await expect(page.getByRole('tabpanel', { name: 'Compute' })).toBeVisible();
});
