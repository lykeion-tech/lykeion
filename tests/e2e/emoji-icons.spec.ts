import { expect, test } from '@playwright/test';
import { labFixture, labTeam } from './lab';

test('Agent and Team emoji persist after reload and appear in their lists', async ({ page }) => {
  const fixture = await labFixture(page.request);
  const headers = { 'X-Workspace-ID': fixture.workspace_id };
  const created = await page.request.post('/api/agents', {
    headers,
    data: { name: 'Emoji researcher', harness: 'claude', visibility: 'workspace' },
  });
  expect(created.status()).toBe(201);
  const agent = await created.json();
  const team = await labTeam(page.request, fixture.workspace_id);
  await page.addInitScript(
    (ws) => localStorage.setItem('lykeion.workspace', ws),
    fixture.workspace_id,
  );
  await page.goto(`/#/agents/${agent.id}`);
  await page.getByRole('button', { name: 'Change avatar' }).click();
  await expect(page.getByRole('button', { name: 'Upload image' })).toHaveCount(0);
  await page.getByRole('button', { name: '🦉', exact: true }).click();
  await expect(page.locator('.agent-identity .avatar-emoji')).toHaveText('🦉');
  await page.reload();
  await expect(page.locator('.agent-identity .avatar-emoji')).toHaveText('🦉');
  await page.goto('/#/agents');
  await expect(
    page.locator('.agent-row').filter({ hasText: 'Emoji researcher' }).locator('.avatar-emoji'),
  ).toHaveText('🦉');
  await page.goto(`/#/teams/${team}`);
  await page.getByRole('button', { name: 'Change avatar' }).click();
  await page.getByRole('button', { name: '🦊', exact: true }).click();
  await expect(page.locator('.agent-identity .avatar-emoji')).toHaveText('🦊');
  await page.reload();
  await expect(page.locator('.agent-identity .avatar-emoji')).toHaveText('🦊');
  await page.goto('/#/teams');
  await expect(
    page.locator('tr').filter({ hasText: 'ui-demo' }).locator('.avatar-emoji'),
  ).toHaveText('🦊');
  await page.goto(`/#/teams/${team}`);
  await page.getByRole('button', { name: 'Change avatar' }).click();
  await page.getByRole('button', { name: 'Remove emoji' }).click();
  await expect(page.locator('.agent-identity .avatar-emoji')).toHaveCount(0);
  await page.reload();
  await expect(page.locator('.agent-identity .avatar-emoji')).toHaveCount(0);
});
