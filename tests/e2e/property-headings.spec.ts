import { expect, test } from '@playwright/test';
import { LAB, labFixture, openStudyDetails } from './lab';

test('Execution shows Runs directly and Properties shows the details directly', async ({
  page,
}) => {
  const fixture = await labFixture(page.request);
  await page.addInitScript(
    (id) => localStorage.setItem('lykeion.workspace', id),
    fixture.workspace_id,
  );
  await page.goto(`/#/studies/${fixture.card_id}`);
  const meta = page.getByRole('complementary', { name: 'Right pane', exact: true });
  const choose = async (surface: string) => {
    const back = meta.getByRole('button', { name: 'Back to surfaces', exact: true });
    if (await back.isVisible()) await back.click();
    await meta.getByRole('button', { name: surface, exact: true }).click();
  };
  await openStudyDetails(page);
  await choose('Properties');
  const properties = meta.getByRole('heading', { name: 'Properties', level: 2, exact: true });
  await expect(properties).toBeVisible();
  await choose('Timeline');
  await expect(meta.getByRole('heading', { name: 'Execution log', exact: true })).toBeVisible();
  await expect(meta.locator('.run-metrics')).toBeVisible();
  await expect(page.getByRole('dialog', { name: 'Runs', exact: true })).toHaveCount(0);
  // Properties carries the study's details directly, with no disclosure.
  await choose('Properties');
  await expect(meta.locator('.details-body')).toBeVisible();
  await expect(meta.getByRole('button', { name: 'Details', exact: true })).toHaveCount(0);
});

test('a truncated workspace tab keeps its close icon centered beside its title and still closes', async ({
  page,
}) => {
  const fixture = await labFixture(page.request);
  const response = await page.request.post(`${LAB}/lab/idle-study`, {
    headers: { 'X-Workspace-ID': fixture.workspace_id },
    data: {
      question: 'Can a long study title remain readable?',
      title:
        'A deliberately long study title that must truncate inside the workspace tab while its close icon stays centered',
    },
  });
  expect(response.status()).toBe(201);
  const card = await response.json();
  await page.addInitScript(
    (id) => localStorage.setItem('lykeion.workspace', id),
    fixture.workspace_id,
  );
  await page.goto('/#/studies');
  await page.getByRole('button', { name: 'New tab', exact: true }).click();
  await page.goto(`/#/studies/${card.id}`);
  const tab = page.locator('.workspace-tab.active');
  // only short titles widen a tab; a long one stays at the 94px default
  await expect.poll(async () => (await tab.boundingBox())!.width).toBe(94);
  await expect(tab.getByRole('tab')).toHaveAccessibleName(new RegExp(card.title));
  await tab.hover();
  const title = tab.locator('[role="tab"] > span');
  await expect.poll(() => title.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
  const close = tab.getByRole('button', { name: 'Close tab', exact: true });
  await expect(close).toBeVisible();
  const textBox = (await title.boundingBox())!;
  const iconBox = (await close.locator('svg').boundingBox())!;
  expect(
    Math.abs(textBox.y + textBox.height / 2 - iconBox.y - iconBox.height / 2),
  ).toBeLessThanOrEqual(1);
  expect(iconBox.x).toBeGreaterThan(textBox.x + textBox.width);
  const count = await page.getByRole('tab').count();
  await close.click();
  await expect(page.getByRole('tab')).toHaveCount(count - 1);
});
