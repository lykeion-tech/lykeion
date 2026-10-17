import { expect, test, type Page } from '@playwright/test';
import { labFixture, labHypotheses, labRecords, labWorkspace, newStudy } from './lab';

test('an escalation reaches the Inbox and is read on opening', async ({ page }) => {
  const title = `Sleep loss and microglial pruning ${Date.now()}`;
  const { item } = await escalation(page, title);
  await page.goto('/#/inbox');
  const list = page.getByRole('navigation', { name: 'Inbox items' });
  const link = list.getByRole('link', { name: new RegExp(title) }).first();
  await expect(link).toBeVisible();
  await expect(link).toContainText('Escalated');
  await expect(link.locator('.unread')).toHaveCount(1);

  await page.screenshot({ path: `${artifacts}/open-unread.png`, fullPage: true });
  await link.click();
  await expect(
    page.getByRole('region', { name: 'Study' }).getByRole('heading', { name: title }),
  ).toBeVisible();
  await expect(link.locator('.unread')).toHaveCount(0);
  await expect.poll(async () => (await inbox(page)).find((i) => i.id === item.id)?.read_at).toBeTruthy();
  await page.screenshot({ path: `${artifacts}/selected-read.png`, fullPage: true });
});

const artifacts = '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-18-artifacts';
type Item = { id: string; card_id: string; read_at: string | null; resolved_at: string | null };
async function inbox(page: Page, archived = false) {
  const response = await page.request.get(`/api/inbox${archived ? '?archived=1' : ''}`, {
    headers: { 'X-Workspace-ID': await labWorkspace(page.request) },
  });
  expect(response.status()).toBe(200);
  return ((await response.json()) ?? []) as Item[];
}
async function escalation(page: Page, name: string) {
  const card = await newStudy(page.request, `${name} ${Date.now()}?`);
  let item!: Item;
  await expect
    .poll(async () => {
      item = (await inbox(page)).find((i) => i.card_id === card.id)!;
      return Boolean(item);
    })
    .toBe(true);
  return { card, item };
}
const row = (page: Page, id: string) =>
  page.getByRole('navigation', { name: 'Inbox items' }).locator(`a[href="#/inbox/${id}"]`);

test('a failed read shows an inline error and keeps the actual unread item', async ({ page }) => {
  const { item } = await escalation(page, 'Transport read failure');
  await page.route(`**/api/inbox/${item.id}/read`, (route) => route.abort('failed'));
  await page.goto('/#/inbox');
  await row(page, item.id).click();
  await expect(page.getByRole('alert')).toContainText(/Failed to fetch/);
  await expect(row(page, item.id).locator('.unread')).toHaveCount(1);
  expect((await inbox(page)).find((i) => i.id === item.id)?.read_at).toBeNull();
  await page.screenshot({ path: `${artifacts}/read-error.png`, fullPage: true });
});

for (const destination of [
  'another item',
  'same item after a return',
  'another workspace',
] as const) {
  test(`a delayed read refusal cannot affect ${destination}`, async ({ page }) => {
    const a = await escalation(page, 'Old read visit');
    const b = await escalation(page, 'Current read visit');
    const other = await labFixture(page.request);
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    let accepted!: (status: number) => void;
    const refused = new Promise<number>((resolve) => {
      accepted = resolve;
    });
    let drain!: () => void;
    const drained = new Promise<void>((resolve) => {
      drain = resolve;
    });
    let first = true;
    await page.route(`**/api/inbox/${a.item.id}/read`, async (route) => {
      if (!first) return route.continue();
      first = false;
      try {
        const response = await route.fetch({
          headers: { ...route.request().headers(), 'X-Workspace-ID': other.workspace_id },
        });
        accepted(response.status());
        await gate;
        await route.fulfill({ response });
      } finally {
        drain();
      }
    });
    try {
      await page.goto(`/#/inbox/${a.item.id}`);
      expect(await refused).toBe(404);
      if (destination === 'another workspace') {
        await page
          .getByRole('complementary', { name: 'Workspace navigation' })
          .getByRole('button', { name: /Beta-cell lab/ })
          .click();
        await page
          .getByRole('menu', { name: 'Workspace' })
          .getByRole('menuitem', { name: /Browser fixtures/ })
          .first()
          .click();
        await expect(page).toHaveURL(/#\/studies$/);
        await page.goto('/#/inbox');
        await expect(page.getByText('Nothing needs you right now')).toBeVisible();
      } else {
        await row(page, b.item.id).click();
        await expect(
          page.getByRole('region', { name: 'Study' }).getByRole('heading', { name: b.card.title }),
        ).toBeVisible();
        await page.getByRole('textbox', { name: 'Comment' }).fill('Current visit draft');
        if (destination === 'same item after a return') {
          await row(page, a.item.id).click();
          await expect(row(page, a.item.id).locator('.unread')).toHaveCount(0);
          await page.getByRole('textbox', { name: 'Comment' }).fill('Return visit draft');
        }
      }
      const browserResponse = page.waitForResponse(
        (r) => r.request().method() === 'POST' && r.url().endsWith(`/api/inbox/${a.item.id}/read`),
      );
      release();
      await drained;
      await (await browserResponse).finished();
      await page.evaluate(() => new Promise(requestAnimationFrame));
      await expect(page.getByRole('alert')).toHaveCount(0);
      if (destination !== 'another workspace')
        await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveValue(
          destination === 'another item' ? 'Current visit draft' : 'Return visit draft',
        );
    } finally {
      release();
      await drained;
    }
  });
}

test('the embedded study fits long evidence and resets drafts on selection', async ({
  page,
}) => {
  const a = await escalation(page, 'Embedded long evidence');
  const b = await escalation(page, 'Embedded next selection');
  await labHypotheses(page.request, a.card.id);
  await labRecords(page.request, a.card.id, 25);
  await page.goto(`/#/inbox/${a.item.id}`);
  const study = page.getByRole('region', { name: 'Study' });
  await expect(study.getByRole('heading', { name: a.card.title })).toBeVisible();
  await expect(study.locator('.record-line')).toHaveCount(25);
  const meta = page.getByRole('complementary', { name: 'Study details' });
  expect((await meta.boundingBox())!.width).toBe(260);
  await page.getByRole('textbox', { name: 'Comment' }).fill('Embedded draft must reset');
  await page.screenshot({ path: `${artifacts}/embedded-long-content.png`, fullPage: true });
  const bounds = await study.evaluate((element) => ({
    width: element.clientWidth,
    scrollWidth: element.scrollWidth,
    body: document.documentElement.scrollWidth,
    viewport: innerWidth,
  }));
  expect(bounds.scrollWidth).toBeLessThanOrEqual(bounds.width);
  expect(bounds.body).toBeLessThanOrEqual(bounds.viewport);
  await page.getByRole('textbox', { name: 'Comment' }).fill('Unsaved previous study draft');
  await row(page, b.item.id).click();
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveValue('');
  await expect(page.getByRole('button', { name: 'Action' })).toHaveText('Comment');
});

test('Stop in the embedded study archives the original item and keeps the ended study selected', async ({
  page,
}) => {
  const { card, item } = await escalation(page, 'Embedded Stop resolution');
  await page.goto(`/#/inbox/${item.id}`);
  await expect(
    page.getByRole('region', { name: 'Study' }).getByRole('heading', { name: card.title }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Action' }).click();
  await page.getByRole('menuitem', { name: 'Stop' }).click();
  await page.getByRole('textbox', { name: 'Comment' }).fill('No usable primary data');
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Send' }).click();
  await expect.poll(async () => (await inbox(page, true)).some((i) => i.id === item.id)).toBe(true);
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveCount(0);
  await expect(page.getByRole('heading', { name: card.title })).toBeVisible();
  await page
    .getByRole('navigation', { name: 'Inbox items' })
    .getByRole('button', { name: 'Archived' })
    .click();
  await expect(row(page, item.id)).toBeVisible();
  await page.getByRole('button', { name: 'Back to Inbox' }).click();
  await expect(row(page, item.id)).toHaveCount(0);
});

test('embedded Properties uses its own compact width and responds to the actual Inbox container', async ({
  page,
}) => {
  const { card, item } = await escalation(page, 'Embedded responsive Properties');
  await page.addInitScript(() => {
    if (!localStorage.getItem('lykeion.study-inspector-width'))
      localStorage.setItem('lykeion.study-inspector-width', '424');
  });
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto(`/#/inbox/${item.id}`);
  const study = page.getByRole('region', { name: 'Study' });
  const meta = study.getByRole('complementary', { name: 'Study details' });
  const splitter = study.getByRole('separator', { name: 'Resize study inspector' });
  await expect(study.getByRole('heading', { name: card.title })).toBeVisible();
  await page.setViewportSize({ width: 1100, height: 982 });
  await expect(meta).toBeHidden();
  const railToggle = page.getByRole('button', { name: 'Toggle left sidebar', exact: true }).first();
  await railToggle.click();
  await expect(meta).toHaveCSS('width', '260px');
  await expect(meta).toBeVisible();
  expect(await page.evaluate(() => innerWidth)).toBe(1100);
  await railToggle.click();
  await expect(meta).toBeHidden();
  await page.setViewportSize({ width: 1920, height: 1080 });
  await expect(meta).toHaveCSS('width', '260px');
  await splitter.focus();
  await page.keyboard.press('ArrowRight');
  await expect(meta).toHaveCSS('width', '270px');
  expect(await page.evaluate(() => localStorage.getItem('lykeion.study-inspector-width'))).toBe(
    '424',
  );
  const compactPreference = await page.evaluate(() =>
    localStorage.getItem('lykeion.embedded-study-inspector-width'),
  );
  expect(compactPreference).toBe('270');
  await page.keyboard.press('End');
  await expect(meta).toHaveCSS('width', '480px');
  await page.setViewportSize({ width: 1300, height: 982 });
  const available = (await study.boundingBox())!.width - 400;
  await expect(splitter).toHaveAttribute('aria-valuemax', String(available));
  await expect(meta).toHaveCSS('width', `${available}px`);
  await page.setViewportSize({ width: 1100, height: 982 });
  await expect(meta).toBeHidden();
  expect(
    await page.evaluate(() => localStorage.getItem('lykeion.embedded-study-inspector-width')),
  ).toBe('480');
  await page.setViewportSize({ width: 1920, height: 1080 });
  await expect(meta).toHaveCSS('width', '480px');
  await splitter.focus();
  await page.keyboard.press('Home');
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  await expect(meta).toHaveCSS('width', '260px');
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-study-inspector-refinements/final-fix-artifacts/embedded-1920.png',
  });
  for (const width of [1100, 900]) {
    await page.setViewportSize({ width, height: 982 });
    await expect(meta).toBeHidden();
    await expect(splitter).toBeHidden();
    const main = await study.locator('.task-main').boundingBox();
    const area = await study.boundingBox();
    expect(main!.width).toBeCloseTo(area!.width, 0);
    await page.getByRole('textbox', { name: 'Comment' }).fill(`Usable at ${width}`);
    const composer = await study.locator('.pinned-composer').boundingBox();
    const evidence = await study.locator('.transcript-scroll').boundingBox();
    expect(evidence!.y + evidence!.height).toBeLessThanOrEqual(composer!.y + 1);
    expect(await study.evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(0);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
    ).toBeLessThanOrEqual(0);
    await page.screenshot({
      path: `.superpowers/sdd/2026-10-15-study-inspector-refinements/final-fix-artifacts/embedded-${width}.png`,
    });
  }
  await page.setViewportSize({ width: 1920, height: 1080 });
  await expect(meta).toHaveCSS('width', '260px');
  expect(
    await page.evaluate(() => localStorage.getItem('lykeion.embedded-study-inspector-width')),
  ).toBe('260');
  await page.goto(`/#/studies/${card.id}`);
  await expect(meta).toHaveCSS('width', '424px');
  await splitter.focus();
  await page.keyboard.press('ArrowRight');
  await expect(meta).toHaveCSS('width', '434px');
  expect(
    await page.evaluate(() => localStorage.getItem('lykeion.embedded-study-inspector-width')),
  ).toBe('260');
  await page.goto(`/#/inbox/${item.id}`);
  await expect(meta).toHaveCSS('width', '260px');
  expect(await page.evaluate(() => localStorage.getItem('lykeion.study-inspector-width'))).toBe(
    '434',
  );
});
