import { expect, test } from '@playwright/test';

test('opening Coresearch or Decisions shows its history while manual navigation remains available', async ({ page }) => {
  await page.goto('/#/studies');
  await page.getByRole('link', { name: 'Decisions', exact: false }).click();
  await expect(page.getByRole('button', { name: 'Show decisions' })).toHaveAttribute('aria-pressed', 'true');

  await page.getByRole('button', { name: 'Show navigation' }).click();
  await page.getByRole('link', { name: 'Coresearch', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Show conversations' })).toHaveAttribute('aria-pressed', 'true');

  await page.getByRole('button', { name: 'Show navigation' }).click();
  await page.getByRole('link', { name: 'Decisions', exact: false }).click();
  await expect(page.getByRole('button', { name: 'Show decisions' })).toHaveAttribute('aria-pressed', 'true');

  await page.getByRole('button', { name: 'Show navigation' }).click();
  await page.getByRole('link', { name: 'Decisions', exact: false }).click();
  await expect(page.getByRole('button', { name: 'Show decisions' })).toHaveAttribute('aria-pressed', 'true');

  await page.getByRole('button', { name: 'Show navigation' }).click();
  await page.reload();
  await expect(page.getByRole('button', { name: 'Show decisions' })).toHaveAttribute('aria-pressed', 'true');

  await page.getByRole('button', { name: 'Show navigation' }).click();
  await page.getByRole('link', { name: 'Coresearch', exact: true }).click();
  await page.getByRole('button', { name: 'Show navigation' }).click();
  await page.reload();
  await expect(page.getByRole('button', { name: 'Show conversations' })).toHaveAttribute('aria-pressed', 'true');
});
