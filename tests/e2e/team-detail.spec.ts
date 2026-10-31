import { expect, test, type Page } from '@playwright/test';
import { labPack, labWorkspace } from './lab';

async function seed(page: Page, name: string) {
  const ws = await labWorkspace(page.request);
  const pack = await labPack(page.request, name, ws, { domain: 'biology', stage: 'grounding' });
  const response = await page.request.post(`/api/teams/${pack.id}/add`, {
    headers: { 'X-Workspace-ID': ws },
  });
  expect(response.status()).toBe(200);
  const agents = (await response.json()) as { id: string; name: string }[];
  return { pack, agent: agents.find((a) => a.name.startsWith('pi ·'))! };
}

for (const width of [1512, 1920]) {
  test(`Team studio displays and inspects its agents at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1080 });
    const { pack } = await seed(page, `geometry-team-${width}`);
    await page.goto(`/#/teams/${pack.id}`);
    await expect(page.getByRole('heading', { name: pack.name, exact: true })).toBeVisible();
    await expect(page.locator('.canvas-agent')).toHaveCount(4);
    await expect(page.getByRole('tab', { name: 'Agents', exact: true })).toHaveCount(0);
    const inspector = page.getByLabel('Agent details');
    await expect(inspector).toContainText('pi');
    await page.locator('.canvas-agent').last().click();
    await expect(page.locator('.canvas-agent').last()).toHaveAttribute('aria-pressed', 'true');
    await expect(inspector).toContainText('Edit skills');
    await page.getByRole('tab', { name: 'Overview', exact: true }).focus();
    await page.keyboard.press('ArrowRight');
    await expect(page.getByRole('tab', { name: 'Work', exact: true })).toBeFocused();
    await expect(page.getByRole('textbox', { name: 'Search studies' })).toBeVisible();
    await page.getByRole('tab', { name: 'Settings', exact: true }).click();
    await expect(page.getByRole('tabpanel')).toContainText('biology');
    await expect(page.getByRole('tabpanel')).toContainText('12 steps per column');
    await expect(page.getByRole('tabpanel')).toContainText("Does it say what it couldn't do?");
    await expect(page.getByRole('tab', { name: 'Versions' })).toHaveCount(0);
    await expect(page.locator('.team-detail-page')).not.toContainText(/version/i);
    await page.getByRole('tab', { name: 'Overview', exact: true }).click();
    await page.screenshot({ path: `test-results/team-detail/overview-${width}.png` });
    if (width === 1512) {
      await page.getByRole('button', { name: 'New study', exact: true }).click();
      const composer = page.getByRole('dialog', { name: 'New study', exact: true });
      await expect(
        composer.getByRole('button', { name: `Assignee: ${pack.name}`, exact: true }),
      ).toBeVisible();
      await composer
        .getByRole('textbox', { name: 'Study title', exact: true })
        .fill('Team profile study');
      await composer
        .getByRole('textbox', { name: 'Describe your study', exact: true })
        .fill('Which recorded sources establish this Team profile fixture?');
      const saved = page.waitForResponse(
        (r) => r.url().endsWith('/api/cards') && r.request().method() === 'POST',
      );
      await composer.getByRole('button', { name: /^Create study/ }).click();
      const response = await saved;
      expect(response.status()).toBe(201);
      expect(response.request().postDataJSON().assignee).toEqual({
        type: 'team',
        teams: { grounding: pack.id },
      });
      const study = await response.json();
      expect(study.teams.grounding.pack_id).toBe(pack.id);
      expect(study.scope_from).toBe('grounding');
      expect(study.scope_to).toBe('grounding');
      await page.goto(`/#/teams/${pack.id}`);
      await page.getByRole('tab', { name: 'Work', exact: true }).click();
      await expect(page.getByRole('link', { name: /Team profile study/ })).toBeVisible();
    }
  });
}

test('Team profile tabs and roles remain readable on a narrow viewport', async ({ page }) => {
  const { pack } = await seed(page, 'narrow-team-profile');
  await page.setViewportSize({ width: 600, height: 900 });
  await page.goto(`/#/teams/${pack.id}`);
  await expect(page.getByLabel('Agent details')).toBeVisible();
  expect(
    await page.locator('.team-detail-page').evaluate((el) => el.scrollWidth <= el.clientWidth),
  ).toBe(true);
  await page.getByRole('tab', { name: 'Settings', exact: true }).click();
  await expect(page.getByRole('tabpanel')).toContainText('12 steps per column');
  await page.getByRole('tab', { name: 'Overview', exact: true }).click();
  await page.screenshot({ path: 'test-results/team-detail/narrow.png', fullPage: true });
});
