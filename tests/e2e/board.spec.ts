import { expect, test } from '@playwright/test';
import { LAB, labFixture, newStudy } from './lab';

const artifacts = '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-13-artifacts';

test('each board column opens the new study composer', async ({ page }) => {
  await page.goto('/#/studies');
  for (const name of [
    'Grounding',
    'Hypotheses',
    'Experimentation',
    'Validation',
    'Done',
    'Blocked',
  ]) {
    const column = page.getByRole('region', { name, exact: true });
    await column.getByRole('button', { name: 'New study', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'New study' });
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  }
});

test('the board shows six columns and a new study arriving live in its column', async ({
  page,
}) => {
  await page.goto('/#/studies');
  const headers = page.locator('.board-column > header strong');
  await expect(headers).toHaveText([
    'Grounding',
    'Hypotheses',
    'Experimentation',
    'Validation',
    'Done',
    'Blocked',
  ]);
  await expect(page.locator('.task-card')).toHaveCount(0);
  await page.screenshot({ path: `${artifacts}/empty-board.png`, fullPage: true });
  await page.getByRole('button', { name: 'Board', exact: true }).click();
  await expect(page.locator('.list-row')).toHaveCount(0);
  await page.screenshot({ path: `${artifacts}/empty-list.png`, fullPage: true });
  await page.getByRole('button', { name: 'List', exact: true }).click();
  const question = `Does islet amyloid track IL-1β in human donors? ${Date.now()}`;
  const study = await newStudy(page.request, question);
  // No reload: the study arrives over /ws and starts in Grounding.
  const grounding = page.getByRole('region', { name: 'Grounding' });
  const shown = grounding.locator('.task-card', { hasText: question.slice(0, 40) });
  await expect(shown).toBeVisible({ timeout: 30_000 });
  await expect(shown).toContainText(`BETA-${study.number}`);
  await expect(shown).toContainText(/Updated (just now|\d+m ago)/);
  await expect(page.getByRole('button', { name: /agents working/ })).toBeVisible();

  await shown.scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${artifacts}/populated-board.png`, fullPage: true });
  // The researcher's own study also shows on their personal board.
  await page.goto('/#/my-studies');
  await expect(shown).toBeVisible();
  await page.goto('/#/studies');
  await expect(shown).toBeVisible();

  await page.getByRole('button', { name: 'Board' }).click();
  await expect(page.locator('.list-group', { hasText: 'Grounding' })).toBeVisible();
  await expect(page.locator('.list-row', { hasText: question.slice(0, 40) })).toBeVisible();
  await page.screenshot({ path: `${artifacts}/populated-list.png`, fullPage: true });
  await page.getByRole('button', { name: 'List' }).click();
  await expect(page.locator('.board-scroll')).toBeVisible();
});

test('the filter narrows the board to a column', async ({ page }) => {
  const fixture = await labFixture(page.request);
  await page.addInitScript(
    (id) => localStorage.setItem('lykeion.workspace', id),
    fixture.workspace_id,
  );
  await page.goto('/#/studies');
  await expect(
    page.getByRole('region', { name: 'Blocked' }).locator(`[data-card="${fixture.card_id}"]`),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Filter' }).click();
  await page.getByRole('menuitem', { name: 'Column' }).click();
  await page.getByRole('menuitemcheckbox', { name: /Hypotheses/ }).click();
  await expect(page.locator('.filter-chip')).toHaveText(/Column: Hypotheses/);
  await expect(page.locator(`[data-card="${fixture.card_id}"]`)).toHaveCount(0);
  await page.keyboard.press('Escape');
  await page.keyboard.press('Escape');
  await page.screenshot({ path: `${artifacts}/filtered-board.png`, fullPage: true });
});

test('clearing a real blocked study flag moves it live and display preferences persist', async ({
  page,
}) => {
  const fixture = await labFixture(page.request);
  await page.addInitScript(
    (id) => localStorage.setItem('lykeion.workspace', id),
    fixture.workspace_id,
  );
  await page.goto('/#/studies');
  const blocked = page.getByRole('region', { name: 'Blocked' });
  const hypotheses = page.getByRole('region', { name: 'Hypotheses' });
  const card = page.locator(`[data-card="${fixture.card_id}"]`);
  await expect(blocked.locator(`[data-card="${fixture.card_id}"]`)).toBeVisible();
  await expect(card.locator('.chip-escalated')).toBeVisible();
  await expect(hypotheses.locator('.task-card')).toHaveCount(0);
  const document = await page.evaluate(() => performance.timeOrigin);
  expect((await page.request.post(`${LAB}/lab/hypotheses/${fixture.card_id}`)).status()).toBe(204);
  await expect(hypotheses.locator(`[data-card="${fixture.card_id}"]`)).toBeVisible();
  await expect(blocked.locator('.task-card')).toHaveCount(0);
  await expect(card.locator('.chip-escalated')).toHaveCount(0);
  await expect(card.locator('.chip-working')).toHaveText('Working');
  expect(await page.evaluate(() => performance.timeOrigin)).toBe(document);
  await page.screenshot({ path: `${artifacts}/cleared-flag-board.png`, fullPage: true });
  await page.getByRole('button', { name: 'Display', exact: true }).click();
  await expect(page.locator('.property-chips button')).toHaveText(['Role', 'Updated']);
  await page
    .locator('.property-chips')
    .getByRole('button', { name: 'Updated', exact: true })
    .click();
  await expect(card).not.toContainText('Updated');
  await expect(card).toContainText('Ana Ruiz');
  await page.locator('.property-chips').getByRole('button', { name: 'Role', exact: true }).click();
  await expect(card).not.toContainText('Ana Ruiz');
  await page.screenshot({ path: `${artifacts}/display-properties.png`, fullPage: true });
  await page.reload();
  await expect(card).toBeVisible();
  await expect(card).not.toContainText('Updated');
  await page.getByRole('button', { name: 'Display', exact: true }).click();
  await expect(page.locator('.property-chips button[aria-pressed="false"]')).toHaveCount(2);
  await page.keyboard.press('Escape');
  await expect(page.locator('.display-popover')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Display', exact: true })).toBeFocused();
});

test('Display and filter keyboard paths retain focus', async ({ page }) => {
  await page.goto('/#/studies');
  await page.getByRole('button', { name: 'Display', exact: true }).focus();
  await page.keyboard.press('ArrowDown');
  await expect(
    page.locator('.property-chips').getByRole('button', { name: 'Role', exact: true }),
  ).toBeFocused();
  await page.keyboard.press('End');
  await expect(
    page.locator('.property-chips').getByRole('button', { name: 'Updated', exact: true }),
  ).toBeFocused();
  await page.keyboard.press('Home');
  await expect(
    page.locator('.property-chips').getByRole('button', { name: 'Role', exact: true }),
  ).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(
    page.locator('.property-chips').getByRole('button', { name: 'Updated', exact: true }),
  ).toBeFocused();
  await page.keyboard.press('ArrowUp');
  await expect(
    page.locator('.property-chips').getByRole('button', { name: 'Role', exact: true }),
  ).toBeFocused();
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Filter', exact: true }).focus();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowRight');
  await expect(
    page.getByRole('menuitemcheckbox', { name: 'Grounding', exact: true }),
  ).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('menuitem', { name: 'Column', exact: true })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('button', { name: 'Filter', exact: true })).toBeFocused();
});

test('initial cards refusal shows a local retry and cached studies survive a refresh refusal', async ({
  page,
}) => {
  const fixture = await labFixture(page.request);
  await page.addInitScript(
    (id) => localStorage.setItem('lykeion.workspace', id),
    fixture.workspace_id,
  );
  let refuse = true;
  await page.route('**/api/cards', async (route) => {
    if (!refuse) return route.continue();
    const response = await route.fetch({
      headers: {
        ...route.request().headers(),
        'X-Workspace-ID': '00000000-0000-0000-0000-000000000001',
      },
    });
    expect(response.status()).toBe(404);
    await route.fulfill({ response });
  });
  await page.goto('/#/studies');
  const failure = page.locator('.board-read-error');
  await expect(failure).toBeVisible();
  await expect(page.getByText('No studies', { exact: true })).toHaveCount(0);
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/final-fix-artifacts/board-read-error.png',
    fullPage: true,
  });
  refuse = false;
  await failure.getByRole('button', { name: 'Retry studies' }).click();
  await expect(page.locator(`[data-card="${fixture.card_id}"]`)).toBeVisible();
  refuse = true;
  await page.evaluate(async () => {
    const path = '/src/api/session.ts';
    const { workspaceId } = await import(path);
    const app = document.querySelector('#app') as Element & {
      __vue_app__: {
        _context: {
          provides: Record<string, { invalidateQueries: (options: unknown) => Promise<void> }>;
        };
      };
    };
    await app.__vue_app__._context.provides.VUE_QUERY_CLIENT!.invalidateQueries({
      queryKey: ['ws', workspaceId.value, 'cards'],
    });
  });
  await expect(failure).toBeVisible();
  await expect(page.locator(`[data-card="${fixture.card_id}"]`)).toBeVisible();
  refuse = false;
  await failure.getByRole('button', { name: 'Retry studies' }).click();
  await expect(failure).toHaveCount(0);
});

test('long board columns keep their header and cards inside the column while scrolling', async ({
  page,
}) => {
  const fixture = await labFixture(page.request);
  const headers = { 'X-Workspace-ID': fixture.workspace_id };
  for (let index = 0; index < 10; index++) {
    const response = await page.request.post(`${LAB}/lab/idle-study`, {
      headers,
      data: {
        title: `Long board study ${index}: a question that wraps across several lines`,
        question: `Board scrolling regression ${fixture.workspace_id} ${index}`,
      },
    });
    expect(response.status()).toBe(201);
  }
  await page.setViewportSize({ width: 1000, height: 600 });
  await page.addInitScript(
    (id) => localStorage.setItem('lykeion.workspace', id),
    fixture.workspace_id,
  );
  await page.goto('/#/studies');
  const board = page.locator('.board-scroll');
  const column = page.getByRole('region', { name: 'Grounding', exact: true });
  await expect(column.locator('.task-card')).toHaveCount(10);
  const headerBefore = await column.locator('header').boundingBox();
  const last = column.locator('.task-card').last();
  await last.scrollIntoViewIfNeeded();
  await expect(last).toBeInViewport();
  const geometry = await column.evaluate((column) => {
    const board = column.closest('.board-scroll')!;
    const bounds = column.getBoundingClientRect();
    const header = column.querySelector('header')!.getBoundingClientRect();
    const last = column
      .querySelector('.study-card:last-of-type .task-card')!
      .getBoundingClientRect();
    return {
      boardTop: board.getBoundingClientRect().top,
      headerTop: header.top,
      columnBottom: bounds.bottom,
      lastBottom: last.bottom,
      boardClientHeight: board.clientHeight,
      boardScrollHeight: board.scrollHeight,
    };
  });
  expect(geometry.headerTop).toBeCloseTo(headerBefore!.y, 0);
  expect(geometry.headerTop).toBeGreaterThanOrEqual(geometry.boardTop);
  expect(geometry.lastBottom).toBeLessThanOrEqual(geometry.columnBottom);
  expect(geometry.boardScrollHeight).toBeLessThanOrEqual(geometry.boardClientHeight + 1);
  const done = page.getByRole('region', { name: 'Done', exact: true });
  await done.scrollIntoViewIfNeeded();
  await expect(done).toBeInViewport();
  expect(await board.evaluate((element) => element.scrollLeft)).toBeGreaterThan(0);
  await expect(done.locator('header')).toBeInViewport();
});
