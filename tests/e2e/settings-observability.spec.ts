import { selectPicker } from './select-picker';
import { expect, test } from './test';
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
  const ownCost = async () => {
    const response = await page.request.get(`/api/cards/${study.id}/runs`, { headers });
    const runs: { cost_usd: number }[] = await response.json();
    return runs.reduce((sum, run) => sum + run.cost_usd, 0);
  };
  await expect.poll(ownCost, { timeout: 30_000 }).toBeGreaterThan(0);
  const dollars = (n: number) =>
    `$${n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: n > 0 && n < 0.01 ? 6 : 2 })}`;
  const reportCost = async (scope: string, cadence = 'daily', range = '30d') => {
    const response = await page.request.get(
      `/api/analytics?scope=${scope}&cadence=${cadence}&range=${range}`,
      { headers },
    );
    return (await response.json()).summary.cost_usd as number;
  };
  await page.goto('/#/settings?tab=profile');
  const panel = page.getByTestId('analytics-panel');
  await expect(panel).toBeVisible();
  await expect.poll(async () => (await reportCost('all')) >= (await ownCost())).toBe(true);
  await expect
    .poll(async () => {
      const shown = await panel.getByTestId('analytics-summary').innerText();
      return shown.replace(/\s+/g, '').includes(`Cost${dollars(await reportCost('all'))}`);
    })
    .toBe(true);
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
  await selectPicker(panel.getByRole('combobox', { name: 'Research scope' }), 'standalone');
  await expect
    .poll(async () => {
      const shown = await panel.getByTestId('analytics-summary').innerText();
      return shown
        .replace(/\s+/g, '')
        .includes(`Cost${dollars(await reportCost('standalone', 'weekly', '7d'))}`);
    })
    .toBe(true);
  expect(await reportCost('standalone', 'weekly', '7d')).toBeGreaterThanOrEqual(await ownCost());
  await page.screenshot({
    path: 'test-results/settings-observability/analytics.png',
    fullPage: true,
  });
});
