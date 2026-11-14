import { expect, test } from '@playwright/test';
import { newStudy } from './lab';

test('the study page has no Overview and Environment tabs', async ({ page }) => {
  const study = await newStudy(page.request, 'Which tab opens first?', { assignee: null });
  await page.goto(`/#/studies/${study.id}`);
  const region = page.getByRole('region', { name: 'Study' });
  await expect(region.locator('.study-title')).toHaveText('Which tab opens first?');
  await expect(region.getByRole('tablist')).toHaveCount(0);
  await expect(region.getByRole('tab', { name: 'Overview' })).toHaveCount(0);
  await expect(region.getByRole('tab', { name: 'Environment' })).toHaveCount(0);
});
