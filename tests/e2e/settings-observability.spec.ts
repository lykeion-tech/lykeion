import { expect, test } from '@playwright/test';
import { labWorkspace, newStudy } from './lab';

test('Storage uses live owner-only measurements and omits unsupported controls', async ({
  page,
}) => {
  await page.goto('/#/settings?tab=storage');
  await expect(page.getByRole('button', { name: 'Storage', exact: true })).toBeVisible();
  const nav = await page.locator('.settings-rail button').allTextContents();
  expect(nav.indexOf('Storage')).toBe(nav.indexOf('API Tokens') + 1);
  const panel = page.getByTestId('storage-panel');
  await expect(panel).toBeVisible();
  await expect(panel).not.toContainText('Change location');
  await expect(panel).not.toContainText('Keep a copy');
  await expect
    .poll(
      async () => {
        const response = await page.request.get('/api/storage', {
          headers: { 'X-Workspace-ID': await labWorkspace(page.request) },
        });
        if (!response.ok()) return false;
        const data = await response.json();
        return data.machines.some((machine: { snapshot: unknown }) => machine.snapshot !== null);
      },
      { timeout: 45_000 },
    )
    .toBe(true);
  await page.reload();
  for (const category of ['Workspaces', 'Environments', 'Daemon state']) {
    await expect(panel.locator('dt').filter({ hasText: category }).first()).toBeVisible();
  }
  await page.screenshot({
    path: 'test-results/settings-observability/storage.png',
    fullPage: true,
  });
});

test('Profile analytics filters real study usage and retains Settings geometry', async ({
  page,
}) => {
  const study = await newStudy(page.request, 'Recorded usage for Profile Analytics');
  const workspace = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': workspace };
  await expect
    .poll(
      async () => {
        const response = await page.request.get(`/api/cards/${study.id}/runs`, { headers });
        const runs = await response.json();
        return runs.some((run: { cost_usd: number }) => run.cost_usd > 0);
      },
      { timeout: 30_000 },
    )
    .toBe(true);
  await page.goto('/#/settings?tab=profile');
  const panel = page.getByTestId('analytics-panel');
  await expect(panel).toBeVisible();
  await expect(panel.getByTestId('analytics-summary')).toContainText('$0.01');
  await expect(panel.getByRole('table', { name: 'Agent usage' })).toBeVisible();
  await panel.getByRole('button', { name: 'Weekly', exact: true }).click();
  await expect(panel.getByRole('button', { name: 'Weekly', exact: true })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await panel.getByRole('button', { name: '7d', exact: true }).click();
  await panel
    .getByRole('group', { name: 'Chart metric' })
    .getByRole('button', { name: 'Cost', exact: true })
    .click();
  await expect(panel.getByTestId('analytics-chart')).toBeVisible();
  await expect
    .poll(() =>
      panel
        .locator('.bar')
        .evaluateAll((bars) =>
          bars.some(
            (bar) =>
              bar.getBoundingClientRect().height > 0 &&
              getComputedStyle(bar).backgroundColor !== 'rgba(0, 0, 0, 0)',
          ),
        ),
    )
    .toBe(true);
  await panel.getByRole('combobox', { name: 'Research scope' }).selectOption('standalone');
  await expect(panel.getByTestId('analytics-summary')).toContainText('$0.01');
  await page.screenshot({
    path: 'test-results/settings-observability/analytics.png',
    fullPage: true,
  });
});
