import { expect, test } from '@playwright/test';
import { labFixture } from './lab';

const artifacts =
  '.superpowers/sdd/2026-10-15-study-inspector-refinements/task-layout-corrections-artifacts';

test('Properties follows the study with its left-edge resizer and preserves collapsed/narrow geometry', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  await page.addInitScript((ws) => {
    localStorage.setItem('lykeion.workspace', ws);
    localStorage.setItem('lykeion.theme', 'light');
  }, f.workspace_id);
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto(`/#/studies/${f.card_id}`);
  const meta = page.locator('aside[aria-label="Right pane"]');
  await meta.getByRole('button', { name: 'Properties', exact: true }).click();
  const splitter = page.getByRole('separator', { name: 'Resize right pane' });
  const main = page.locator('.task-main');
  await expect(meta).toHaveCSS('width', '240px');
  const rail = (await page.locator('.sidebar').boundingBox())!;
  const properties = (await meta.boundingBox())!;
  const content = (await main.boundingBox())!;
  const separator = (await splitter.boundingBox())!;
  expect(content.x).toBeCloseTo(rail.x + rail.width, 0);
  expect(properties.x).toBeCloseTo(content.x + content.width, 0);
  // the resizer straddles the pane's left edge
  expect(Math.abs(separator.x + separator.width / 2 - properties.x)).toBeLessThanOrEqual(1);
  expect(
    await meta.evaluate((el) =>
      Boolean(
        el.compareDocumentPosition(el.parentElement!.querySelector('.task-main')!) &
        Node.DOCUMENT_POSITION_PRECEDING,
      ),
    ),
  ).toBe(true);
  await splitter.focus();
  await page.keyboard.press('ArrowLeft');
  await expect(meta).toHaveCSS('width', '250px');
  await expect(splitter).toHaveAttribute('aria-valuenow', '250');
  const drag = (await splitter.boundingBox())!;
  await page.mouse.move(drag.x + drag.width / 2, drag.y + 60);
  await page.mouse.down();
  await page.mouse.move(drag.x + drag.width / 2 - 50, drag.y + 60);
  await page.mouse.up();
  await expect(meta).toHaveCSS('width', '300px');
  const shrink = (await splitter.boundingBox())!;
  await page.mouse.move(shrink.x + shrink.width / 2, shrink.y + 60);
  await page.mouse.down();
  await page.mouse.move(shrink.x + shrink.width / 2 + 60, shrink.y + 60);
  await page.mouse.up();
  await expect(meta).toHaveCSS('width', '240px');
  await page.reload();
  await expect(meta).toHaveCSS('width', '240px');
  await page.screenshot({ path: `${artifacts}/left-properties-1920.png` });
  await page.getByRole('button', { name: 'Toggle left sidebar', exact: true }).first().click();
  await expect(page.locator('.sidebar')).toBeHidden();
  const open = (await main.boundingBox())!;
  expect(open.x).toBeLessThan(20);
  expect((await meta.boundingBox())!.x).toBeGreaterThanOrEqual(open.x + open.width - 1);
  await page.getByRole('button', { name: 'Close pane', exact: true }).click();
  await expect(meta).toHaveCount(0);
  await expect(splitter).toHaveCount(0);
  expect((await main.boundingBox())!.x).toBeLessThan(20);
  await page.getByRole('button', { name: 'Toggle details', exact: true }).click();
  await expect(meta).toBeVisible();
  // At narrow widths the pane floats over the study instead of squeezing it.
  await page.setViewportSize({ width: 900, height: 982 });
  const narrow = (await main.boundingBox())!;
  const narrowMeta = (await meta.boundingBox())!;
  await expect(meta).toHaveAttribute('role', 'dialog');
  expect(narrow.width).toBeGreaterThan(860);
  expect(narrowMeta.x + narrowMeta.width).toBeLessThanOrEqual(900);
  await page.screenshot({ path: `${artifacts}/collapsed-rail-900.png` });
  await page.setViewportSize({ width: 800, height: 982 });
  await expect(meta).toHaveAttribute('role', 'dialog');
  await expect(splitter).toBeHidden();
  expect((await main.boundingBox())!.x).toBeLessThan(20);
  expect((await main.boundingBox())!.width).toBeGreaterThan(760);
  const mobileMeta = (await meta.boundingBox())!;
  expect(mobileMeta.x + mobileMeta.width).toBeLessThanOrEqual(800);
  await page.screenshot({ path: `${artifacts}/mobile-800.png` });
});

test('Projects creates from the title header without an introductory toolbar or actions on other routes', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  await page.addInitScript((ws) => {
    localStorage.setItem('lykeion.workspace', ws);
    localStorage.setItem('lykeion.theme', 'light');
  }, f.workspace_id);
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto('/#/projects');
  const header = page.locator('.page-header');
  const create = header.getByRole('button', { name: 'New Project', exact: true });
  await expect(create).toBeVisible();
  await expect(
    page.getByText('Organize studies around a shared research question.', { exact: true }),
  ).toHaveCount(0);
  await expect(page.locator('.directory-toolbar')).toHaveCount(0);
  const title = (await header
    .getByRole('heading', { name: 'Projects', exact: true })
    .boundingBox())!;
  const button = (await create.boundingBox())!;
  const heading = (await header.boundingBox())!;
  expect(Math.abs(title.y + title.height / 2 - button.y - button.height / 2)).toBeLessThan(2);
  expect(heading.x + heading.width - button.x - button.width).toBeLessThanOrEqual(24);
  await page.screenshot({ path: `${artifacts}/researches-header-1920.png` });
  await create.click();
  const dialog = page.getByRole('dialog', { name: 'New Project', exact: true });
  await dialog.getByRole('textbox', { name: 'Project name' }).fill('Header-created Project');
  await dialog.getByRole('button', { name: 'Create Project', exact: true }).click();
  await expect(page).toHaveURL(/#\/projects\/[a-f0-9-]+$/);
  await expect(
    page.getByRole('heading', { name: 'Header-created Project', exact: true }),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'New Project', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Edit Project', exact: true })).toBeVisible();
  await page.goto('/#/agents');
  await expect(page.getByRole('button', { name: 'New Project', exact: true })).toHaveCount(0);
  await page.goto('/#/projects');
  await expect(create).toBeVisible();
});

test('Agents and Projects create from scoped title actions with decorative plus icons', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto('/#/agents');
  const header = page.locator('.page-header');
  const create = header.getByRole('button', { name: 'New agent', exact: true });
  await expect(create).toBeVisible();
  await expect(create.locator('svg[aria-hidden="true"]')).toHaveCount(1);
  await expect(
    page.getByText('Agent profiles for study responsibility.', { exact: true }),
  ).toHaveCount(0);
  await expect(page.locator('.directory-toolbar')).toHaveCount(0);
  await page.screenshot({ path: `${artifacts}/agents-header-plus-1920.png` });
  await create.click();
  const dialog = page.getByRole('dialog', { name: 'Create Agent', exact: true });
  await dialog
    .getByRole('textbox', { name: 'Agent name', exact: true })
    .fill('Header-created agent');
  await dialog.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(page).toHaveURL(/#\/agents\/[a-f0-9-]+$/);
  await expect(
    page.getByRole('heading', { name: 'Header-created agent', exact: true }),
  ).toBeVisible();
  await expect(header.getByRole('button', { name: 'New agent', exact: true })).toHaveCount(0);
  await page.goto('/#/projects');
  const research = header.getByRole('button', { name: 'New Project', exact: true });
  await expect(research).toBeVisible();
  await expect(research.locator('svg[aria-hidden="true"]')).toHaveCount(1);
  await expect(header.getByRole('button', { name: 'New agent', exact: true })).toHaveCount(0);
  await page.screenshot({ path: `${artifacts}/researches-header-plus-1920.png` });
  await page.goto('/#/studies');
  await expect(header.getByRole('button', { name: /New Project|New agent/ })).toHaveCount(0);
});
