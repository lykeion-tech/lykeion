import { expect, test } from '@playwright/test';

import type { Page } from '@playwright/test';

// The brand row's toggle sits on every rail; clicking the selected item again
// swaps that rail and the main navigation.
const toggle = (page: Page, name: 'Coresearch' | 'Decisions') =>
  page.locator('.sidebar-brand:visible').getByRole('link', { name, exact: true });
const swap = (page: Page) => page.locator('.sidebar-brand:visible [aria-current=page]').click();
const navigation = (page: Page) =>
  page.getByRole('complementary', { name: 'Workspace navigation' });
const chat = (page: Page) => page.getByRole('complementary', { name: 'Chat', exact: true });
const decisions = (page: Page) => page.locator('.decision-rail');

test('opening Coresearch or Decisions shows its history while manual navigation remains available', async ({
  page,
}) => {
  await page.goto('/#/studies');
  await toggle(page, 'Decisions').click();
  await expect(decisions(page)).toBeVisible();
  await expect(page.locator('.rail-fabs')).toHaveCount(0);

  await swap(page);
  await expect(navigation(page)).toBeVisible();
  await expect(decisions(page)).toBeHidden();
  await toggle(page, 'Coresearch').click();
  await expect(chat(page)).toBeVisible();

  await swap(page);
  await expect(navigation(page)).toBeVisible();
  await toggle(page, 'Decisions').click();
  await expect(decisions(page)).toBeVisible();

  await swap(page);
  await expect(navigation(page)).toBeVisible();
  await page.reload();
  await expect(decisions(page)).toBeVisible();

  await swap(page);
  await toggle(page, 'Coresearch').click();
  await swap(page);
  await expect(navigation(page)).toBeVisible();
  await page.reload();
  await expect(chat(page)).toBeVisible();
});

test('the Chat and Decisions action rows line up with the sidebar search row', async ({ page }) => {
  await page.goto('/#/studies');
  const first = (await navigation(page).locator('.quick-actions .nav-item').first().boundingBox())!;
  await toggle(page, 'Coresearch').click();
  const chatRow = (await chat(page).locator('.rail-actions .nav-item').first().boundingBox())!;
  expect(Math.abs(chatRow.y - first.y)).toBeLessThan(1);
  expect(Math.abs(chatRow.height - first.height)).toBeLessThan(1);
  await toggle(page, 'Decisions').click();
  const decisionsRow = (await decisions(page).locator('.rail-actions .nav-item').first().boundingBox())!;
  expect(Math.abs(decisionsRow.y - first.y)).toBeLessThan(1);
  expect(Math.abs(decisionsRow.height - first.height)).toBeLessThan(1);
});

test('the sidebar puts the wordmark and the Coresearch/Decisions toggle on top and the Lab menu at the bottom', async ({
  page,
}) => {
  await page.goto('/#/studies');
  const nav = page.getByRole('complementary', { name: 'Workspace navigation' });
  const brand = nav.locator('.sidebar-brand');
  await expect(brand.locator('.wordmark')).toHaveText('Lykeion');
  const mark = (await brand.locator('.wordmark').boundingBox())!;
  const switchBox = (await brand.locator('.brand-switch').boundingBox())!;
  const side = (await nav.boundingBox())!;
  expect(Math.abs(mark.y + mark.height / 2 - (switchBox.y + switchBox.height / 2))).toBeLessThan(4);
  expect(mark.x - side.x).toBeLessThan(50);
  expect(side.x + side.width - (switchBox.x + switchBox.width)).toBeLessThan(16);
  await expect(nav.locator('nav').getByRole('link', { name: /Coresearch|Decisions/ })).toHaveCount(
    0,
  );
  await expect(nav.locator('nav').getByRole('link', { name: /^(Settings|Lykeion)$/ })).toHaveCount(
    0,
  );

  await brand.getByRole('link', { name: 'Decisions' }).click();
  await expect(page).toHaveURL(/#\/decisions/);
  await expect(toggle(page, 'Decisions')).toHaveAttribute('aria-current', 'page');
  await page.locator('.sidebar-brand:visible').getByRole('link', { name: 'Coresearch' }).click();
  await expect(page).toHaveURL(/#\/coresearch/);

  await page.goto('/#/studies');
  const lab = nav.locator('.workspace-switch');
  const labBox = (await lab.boundingBox())!;
  expect(side.y + side.height - (labBox.y + labBox.height)).toBeLessThan(24);
  await lab.click();
  const menu = page.getByRole('menu', { name: 'Workspace' });
  const items = await menu
    .locator(':scope > button, :scope > small, :scope > .shell-menu-divider')
    .evaluateAll((els) =>
      els.map((e) =>
        e.classList.contains('shell-menu-divider') ? '—' : (e.textContent ?? '').trim(),
      ),
    );
  expect(items.slice(1, 5)).toEqual(['Settings', 'Lykeion', '—', 'Workspaces']);
  expect(items[0]).toContain('@');
  expect(items.at(-1)).toBe('Log out');
  const box = (await menu.boundingBox())!;
  expect(box.y).toBeGreaterThanOrEqual(0);
  expect(box.y + box.height).toBeLessThanOrEqual(labBox.y);
  await menu.getByRole('menuitem', { name: 'Lykeion' }).click();
  await expect(page).toHaveURL(/#\/lab-chart/);
  await lab.click();
  await menu.getByRole('menuitem', { name: 'Settings' }).click();
  await expect(page).toHaveURL(/#\/settings/);
});

test('the wordmark takes Coresearch and Decisions back to Studies, and Decisions shows a bell', async ({
  page,
}) => {
  for (const from of ['/#/coresearch', '/#/decisions']) {
    await page.goto(from);
    await page.locator('.sidebar-brand:visible .brand-home').click();
    await expect(page).toHaveURL(/#\/studies$/);
    await expect(navigation(page)).toBeVisible();
    await expect(
      page.locator('.page-header').getByRole('heading', { name: 'Studies' }),
    ).toBeVisible();
  }
  await expect(toggle(page, 'Decisions').locator('svg.lucide-bell')).toHaveCount(1);
});

test('every rail is 262px wide by default and the sidebar toggle sits left of the wordmark, even when hidden', async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.removeItem('lykeion.sidebar-width'));
  const button = page.getByRole('button', { name: 'Toggle left sidebar', exact: true });
  for (const [path, rail] of [
    ['/#/studies', navigation(page)],
    ['/#/coresearch', chat(page)],
    ['/#/decisions', decisions(page)],
  ] as const) {
    await page.goto(path);
    await expect(rail).toBeVisible();
    expect((await rail.boundingBox())!.width).toBe(262);
    const mark = (await page.locator('.sidebar-brand:visible .wordmark').boundingBox())!;
    const toggle = (await button.boundingBox())!;
    expect(toggle.x + toggle.width).toBeLessThanOrEqual(mark.x);
    expect(mark.x - (toggle.x + toggle.width)).toBeLessThan(16);
    expect(Math.abs(toggle.y + toggle.height / 2 - (mark.y + mark.height / 2))).toBeLessThan(3);
    const forward = (await page.getByRole('button', { name: 'Go forward' }).boundingBox())!;
    const switches = (await page.locator('.sidebar-brand:visible .brand-switch').boundingBox())!;
    // The history arrows sit above the Coresearch/Decisions toggle.
    expect(Math.abs(forward.x + forward.width - (switches.x + switches.width))).toBeLessThan(6);
    expect(forward.y + forward.height).toBeLessThanOrEqual(switches.y);
    await button.click();
    await expect(rail).toBeHidden();
    await expect(button).toBeVisible();
    expect(await button.boundingBox()).toEqual(toggle);
    // The page keeps its full width; the toggle sits inside its top-left corner.
    await expect
      .poll(async () => (await page.locator('.main-pane').boundingBox())!.x)
      .toBeLessThan(toggle.x);
    await button.click();
    await expect(rail).toBeVisible();
  }
});

test('with the sidebar hidden every page header starts past the toggle', async ({ page }) => {
  const toggle = page.getByRole('button', { name: 'Toggle left sidebar', exact: true });
  // Where the first visible text or control of the page's top row starts.
  const headerStart = () =>
    page.evaluate(() => {
      const t = document.querySelector('.sidebar-toggle')!.getBoundingClientRect();
      let left = Infinity;
      for (const el of document.querySelectorAll('.main-pane *')) {
        const b = el.getBoundingClientRect();
        if (!b.width || b.top >= t.bottom || b.bottom <= t.top) continue;
        if (['BUTTON', 'A', 'svg', 'INPUT'].includes(el.tagName)) left = Math.min(left, b.left);
        for (const n of el.childNodes)
          if (n.nodeType === 3 && n.textContent!.trim()) {
            const r = document.createRange();
            r.selectNodeContents(n);
            left = Math.min(left, r.getBoundingClientRect().left);
          }
      }
      return { left, toggleRight: t.right };
    });
  for (const path of ['/#/studies', '/#/agents', '/#/coresearch', '/#/decisions', '/#/settings']) {
    await page.goto(path);
    await expect(page.locator('.main-pane')).toBeVisible();
    if (!(await page.locator('.application.sidebar-hidden').count())) await toggle.click();
    await expect(page.locator('.application.sidebar-hidden')).toHaveCount(1);
    // Wait for the sidebar to finish closing, so the page sits at its full width.
    await expect
      .poll(async () => (await page.locator('.main-pane').boundingBox())!.x)
      .toBeLessThan(10);
    await expect
      .poll(
        async () => {
          const { left, toggleRight } = await headerStart();
          return left - toggleRight;
        },
        { message: path },
      )
      .toBeGreaterThanOrEqual(4);
    await toggle.click();
  }
});
