import { expect, test } from '@playwright/test';
import { labPack, labSignIn, labWorkspace } from './lab';

const shots = 'test-results/teams-scoring';

test('the sidebar shows Teams between Studies and Colleagues', async ({ page }) => {
  await page.goto('/#/studies');
  const nav = page.getByRole('complementary', { name: 'Workspace navigation' });
  const names = await nav.getByRole('link').allInnerTexts();
  const at = (name: string) => names.findIndex((n) => n.trim() === name);
  expect(at('Teams')).toBeGreaterThan(at('Studies'));
  expect(at('Teams')).toBeLessThan(at('Colleagues'));
  await nav.getByRole('link', { name: 'Teams', exact: true }).click();
  await expect(page).toHaveURL(/#\/teams$/);
  await expect(page.getByRole('heading', { name: 'Teams', level: 1 })).toBeVisible();
  await expect(nav.getByRole('link', { name: 'Teams', exact: true }).locator('svg')).toHaveClass(
    /lucide-network/,
  );
  await expect(page.locator('#page-header-count')).toHaveText('');
});

test('Teams shows the empty state when nothing is shipped or uploaded', async ({ page }) => {
  await page.route(/\/api\/teams\?/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }),
  );
  await page.goto('/#/teams');
  const empty = page.locator('.directory-empty');
  await expect(empty.getByRole('heading', { name: 'No Teams yet' })).toBeVisible();
  await expect(empty).toContainText('A Team is the agents that work each stage of your studies.');
  await expect(empty.locator('svg')).toHaveClass(/lucide-network/);
  const chooser = page.waitForEvent('filechooser');
  await empty.getByRole('button', { name: 'Upload a Team' }).click();
  await chooser;
});

test('the store filters by domain and stage', async ({ page }) => {
  const ws = await labWorkspace(page.request);
  await labPack(page.request, 'store-neuro', ws, { domain: 'neuroscience', stage: 'grounding' });
  await labPack(page.request, 'store-chem', ws, { domain: 'chemistry', stage: 'validation' });
  await page.goto('/#/teams');
  const rows = page.locator('.directory-list a');
  await expect(rows.filter({ hasText: 'store-neuro' })).toContainText('Built for Grounding');
  await expect(rows.filter({ hasText: 'store-chem' })).toContainText('chemistry');
  await page.screenshot({ path: `${shots}/store.png` });

  await page.getByLabel('Domain').selectOption('chemistry');
  await expect(rows.filter({ hasText: 'store-chem' })).toHaveCount(1);
  await expect(rows.filter({ hasText: 'store-neuro' })).toHaveCount(0);
  await page.getByLabel('Domain').selectOption('');
  await page.getByLabel('Stage').selectOption('grounding');
  await expect(rows.filter({ hasText: 'store-neuro' })).toHaveCount(1);
  await expect(rows.filter({ hasText: 'store-chem' })).toHaveCount(0);
  await page.screenshot({ path: `${shots}/store-filtered.png` });
});

test('adding a Team creates its agents', async ({ page }) => {
  const ws = await labWorkspace(page.request);
  await labPack(page.request, 'store-add', ws, { domain: 'neuroscience', stage: 'grounding' });
  await page.goto('/#/teams');
  const row = page.locator('.directory-list a').filter({ hasText: 'store-add' });
  await expect(row).not.toContainText('Added');
  await row.click();
  await expect(page.getByRole('heading', { name: 'store-add', level: 1 })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Leader' })).toBeVisible();
  await expect(page.getByText('scout', { exact: true })).toBeVisible();
  await page.screenshot({ path: `${shots}/team-detail.png` });
  await page.getByRole('button', { name: 'Add Team' }).click();
  await expect(page.getByRole('status')).toContainText('Created 4 agents in your Agents page');
  await page.screenshot({ path: `${shots}/team-added.png` });
  await page.getByRole('link', { name: 'Agents', exact: true }).first().click();
  await expect(page.getByRole('table', { name: 'Agents' })).toContainText('pi · store-add');
  await page.goto('/#/teams');
  await expect(page.locator('.directory-list a').filter({ hasText: 'store-add' })).toContainText(
    'Added',
  );
});

test("a colleague's Team is not in my store", async ({ page, browser }) => {
  const ws = await labWorkspace(page.request);
  const email = `store.${Date.now()}@ki.se`;
  const invite = await page.request.post(`/api/workspaces/${ws}/invitations`, {
    headers: { 'X-Workspace-ID': ws },
    data: { email, role: 'member' },
  });
  expect(invite.status()).toBe(201);
  const other = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  try {
    await labSignIn(other.request, email);
    const token = (await invite.json()).token;
    expect((await other.request.post(`/api/invitations/${token}/accept`)).status()).toBe(200);
    await labPack(other.request, 'store-theirs', ws, {
      domain: 'neuroscience',
      stage: 'grounding',
    });
    await page.goto('/#/teams');
    await expect(page.locator('.directory-list a').first()).toBeVisible();
    await expect(page.locator('.directory-list a').filter({ hasText: 'store-theirs' })).toHaveCount(
      0,
    );
  } finally {
    await other.close();
  }
});
