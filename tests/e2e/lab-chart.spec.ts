import { expect, test } from './test';

test('the Lab menu lists Lykeion after Settings and opens the lab chart', async ({ page }) => {
  await page.goto('/#/studies');
  const nav = page.getByRole('complementary', { name: 'Workspace navigation' });
  await nav.locator('.workspace-switch').click();
  const settings = page.getByRole('menuitem', { name: 'Settings', exact: true });
  const after = settings.locator('xpath=following-sibling::button[1]');
  await expect(after).toHaveText('Lykeion');
  const chart = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/lab-chart');
  await after.click();
  const rooms: { agents: { source: string }[] }[] = (await (await chart).json()).rooms;
  const agents = rooms.flatMap((r) => r.agents);
  await expect(page).toHaveURL(/#\/lab-chart$/);
  await expect(page.locator('canvas')).toBeVisible();
  const list = page.getByRole('region', { name: 'Lykeion rooms' });
  await expect(
    list.locator('.lab-chart-list-room').filter({ has: page.locator('li') }),
  ).not.toHaveCount(0);
  await expect(list.locator('.lab-chart-list-room')).toHaveCount(5);
  // The e2e lab installs a trimmed shipped pack (4 agents per Team); the full 18 is covered server-side.
  // Agents other specs create with a stage join their rooms too.
  expect(agents.filter((a) => a.source !== 'researcher')).toHaveLength(16);
  await expect(list.locator('.lab-chart-list-room:not(:last-child) li')).toHaveCount(agents.length);
  await expect(page.getByText('Orbit')).toHaveCount(0);
  await expect(page.getByText('Night')).toHaveCount(0);
});
