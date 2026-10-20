import { expect, test } from '@playwright/test';
import { labPack, labWorkspace } from './lab';

test('Teams uses the reference table and opens the reference-size scientific creation modal', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  const ws = await labWorkspace(page.request);
  await labPack(page.request, 'reference-layout-team', ws, {
    domain: 'biology',
    stage: 'grounding',
  });
  await page.goto('/#/teams');
  const table = page.getByRole('table', { name: 'Teams', exact: true });
  await expect(table.getByRole('columnheader', { name: 'Name', exact: true })).toHaveCSS(
    'font-size',
    '14px',
  );
  const columns = ['Name', 'Domain', 'Built for', 'Agents', 'Version', 'Availability'];
  for (const [i, name] of columns.entries())
    await expect(table.getByRole('columnheader').nth(i)).toHaveAccessibleName(name);
  await expect(table.locator('thead')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
  await expect(
    table.getByRole('columnheader', { name: 'Domain', exact: true }).getByLabel('Domain'),
  ).toHaveValue('');
  await expect(
    table.getByRole('columnheader', { name: 'Built for', exact: true }).getByLabel('Stage'),
  ).toHaveValue('');
  await expect(page.locator('.store-filters')).toHaveCount(0);
  const row = table.getByRole('row').filter({ hasText: 'reference-layout-team' });
  await expect(row).toContainText('biology');
  await expect(row).toContainText('Grounding');
  expect((await row.boundingBox())!.height).toBe(44);
  expect((await table.locator('thead').boundingBox())!.height).toBe(32);
  await expect(page.getByRole('button', { name: 'Upload a Team' })).toHaveCount(0);
  await expect(page.locator('input[type="file"]')).toHaveCount(0);
  await page.screenshot({ path: 'test-results/teams-reference/list-light.png' });
  await page.locator('#page-header-actions').getByRole('link', { name: 'New Team' }).click();
  await expect(page).toHaveURL(/#\/teams\/new$/);
  const modal = page.getByRole('dialog', { name: 'Create Team', exact: true });
  await expect(modal).toBeVisible();
  expect((await modal.boundingBox())!.width).toBe(676);
  expect((await modal.boundingBox())!.height).toBeGreaterThanOrEqual(820);
  await expect(modal.getByRole('textbox', { name: 'Team name', exact: true })).toBeFocused();
  await expect(modal.getByRole('textbox', { name: 'Team name', exact: true })).toHaveAttribute(
    'maxlength',
    '64',
  );
  await expect(
    modal.getByRole('textbox', { name: 'Description', exact: true }),
  ).not.toHaveAttribute('maxlength');
  await expect(modal.getByRole('heading', { name: 'Leader Agent', exact: true })).toBeVisible();
  await expect(
    modal.getByRole('heading', { name: 'Additional Members', exact: true }),
  ).toBeVisible();
  await expect(modal.getByLabel('Auditor agent')).toHaveCount(1);
  await expect(modal.getByLabel('Auditor role')).toHaveCount(1);
  await expect(modal.getByLabel('Steps per column')).toHaveAttribute('max', '50');
  await expect(modal.getByRole('button', { name: 'Create Team', exact: true })).toBeDisabled();
  await page.screenshot({ path: 'test-results/teams-reference/modal-top-light.png' });
  const footer = await modal.locator('footer').boundingBox();
  await modal.getByLabel('Steps per column').scrollIntoViewIfNeeded();
  expect((await modal.locator('footer').boundingBox())!.y).toBe(footer!.y);
  await page.screenshot({ path: 'test-results/teams-reference/modal-light.png' });
  await page.keyboard.press('Escape');
  await expect(page).toHaveURL(/#\/teams$/);
  await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('direct Team creation keeps the footer accessible on a narrow screen', async ({ page }) => {
  await page.setViewportSize({ width: 600, height: 800 });
  await page.goto('/#/teams/new');
  const modal = page.getByRole('dialog', { name: 'Create Team', exact: true });
  await expect(modal).toBeVisible();
  const bounds = await modal.boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(16);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(584);
  await modal.getByLabel('Steps per column').scrollIntoViewIfNeeded();
  await expect(modal.getByLabel('Steps per column')).toBeVisible();
  await expect(modal.getByRole('button', { name: 'Cancel', exact: true })).toBeVisible();
  expect(
    await modal.locator('.team-editor-body').evaluate((el) => el.scrollWidth <= el.clientWidth),
  ).toBe(true);
  await page.screenshot({ path: 'test-results/teams-reference/modal-narrow.png' });
  await modal.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page).toHaveURL(/#\/teams$/);
});
