import { expect, test } from '@playwright/test';
import { LAB, labFixture } from './lab';

test('Properties contains smaller semantic Execution log and Details headings with independent disclosures', async ({
  page,
}) => {
  const fixture = await labFixture(page.request);
  await page.addInitScript(
    (id) => localStorage.setItem('lykeion.workspace', id),
    fixture.workspace_id,
  );
  await page.goto(`/#/studies/${fixture.card_id}`);
  const meta = page.getByRole('complementary', { name: 'Study details', exact: true });
  const properties = meta.getByRole('heading', { name: 'Properties', level: 2, exact: true });
  await expect(properties).toBeVisible();
  const propertiesSize = await properties.evaluate((el) =>
    parseFloat(getComputedStyle(el).fontSize),
  );
  for (const name of ['Execution log', 'Details']) {
    const heading = meta.getByRole('heading', { name, level: 3, exact: true });
    await expect(heading).toBeVisible();
    await expect(heading).toHaveCSS('font-size', '14px');
    await expect(heading).toHaveCSS('font-weight', '600');
    expect(await heading.evaluate((el) => parseFloat(getComputedStyle(el).fontSize))).toBeLessThan(
      propertiesSize,
    );
    const button = heading.getByRole('button', { name, exact: true });
    await expect(button).toHaveAttribute('aria-expanded', 'true');
    await button.click();
    await expect(button).toHaveAttribute('aria-expanded', 'false');
    await expect(
      meta.locator(name === 'Execution log' ? '.execution-body' : '.details-body'),
    ).toHaveCount(0);
    await expect(
      meta.locator(name === 'Execution log' ? '.details-body' : '.execution-body'),
    ).toBeVisible();
    await button.focus();
    await page.keyboard.press('Enter');
    await expect(button).toHaveAttribute('aria-expanded', 'true');
    await expect(
      meta.locator(name === 'Execution log' ? '.execution-body' : '.details-body'),
    ).toBeVisible();
  }
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
