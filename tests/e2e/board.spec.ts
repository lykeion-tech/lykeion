import { expect, test } from '@playwright/test';
import { LAB, labFixture, newStudy } from './lab';

const artifacts = '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-13-artifacts';

test('the board shows eight columns and a new study moving live to Blocked', async ({ page }) => {
  await page.goto('/#/studies');
  const headers = page.locator('.board-column > header strong');
  await expect(headers).toHaveText([
    'Backlog',
    'Grounding',
    'Hypotheses',
    'Experimentation',
    'Validation',
    'Blocked',
    'Reporting',
    'Done',
  ]);
  await expect(page.locator('.task-card')).toHaveCount(0);
  await page.screenshot({ path: `${artifacts}/empty-board.png`, fullPage: true });
  await page.getByRole('button', { name: 'Board', exact: true }).click();
  await expect(page.locator('.list-row')).toHaveCount(0);
  await page.screenshot({ path: `${artifacts}/empty-list.png`, fullPage: true });
  await page.getByRole('button', { name: 'List', exact: true }).click();
  const question = `Does islet amyloid track IL-1β in human donors? ${Date.now()}`;
  const study = await newStudy(page.request, question);
  // No reload: the study arrives over /ws, starts, and the lab's agent escalates it.
  const blocked = page.getByRole('region', { name: 'Blocked' });
  const shown = blocked.locator('.task-card', { hasText: question.slice(0, 40) });
  await expect(shown).toBeVisible({ timeout: 30_000 });
  await expect(shown).toContainText(`BETA-${study.number}`);
  await expect(shown.locator('.chip-escalated')).toHaveText('escalated');
  await expect(shown.locator('.chip')).toContainText(['Grounding']);
  await expect(shown).toContainText(/Updated (just now|\d+m ago)/);
  await expect(page.getByRole('button', { name: /agents working/ })).toBeVisible();

  await shown.scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${artifacts}/populated-board.png`, fullPage: true });
  await page.getByRole('button', { name: 'Mine' }).click();
  await expect(shown).toBeVisible();

  await page.getByRole('button', { name: 'Board' }).click();
  await expect(page.locator('.list-group', { hasText: 'Blocked' })).toBeVisible();
  await expect(page.locator('.list-row', { hasText: question.slice(0, 40) })).toBeVisible();
  await page.screenshot({ path: `${artifacts}/populated-list.png`, fullPage: true });
  await page.getByRole('button', { name: 'List' }).click();
  await expect(page.locator('.board-scroll')).toBeVisible();
});

test('the filter narrows the board to a column', async ({ page }) => {
  await page.goto('/#/studies');
  await expect(page.getByRole('region', { name: 'Blocked' }).locator('.task-card')).toHaveCount(1);
  await page.getByRole('button', { name: 'Filter' }).click();
  await page.getByRole('menuitem', { name: 'Column' }).click();
  await page.getByRole('menuitemcheckbox', { name: /Backlog/ }).click();
  await expect(page.locator('.filter-chip')).toHaveText(/Column: Backlog/);
  await expect(page.getByRole('region', { name: 'Blocked' }).locator('.task-card')).toHaveCount(0);
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
