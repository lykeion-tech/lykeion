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
  test(`Team detail uses exact Agent profile geometry at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1080 });
    const { pack, agent } = await seed(page, `geometry-team-${width}`);
    await page.goto(`/#/agents/${agent.id}`);
    await expect(page.locator('.agent-panel')).toBeVisible();
    const selectors = [
      '.agent-hero',
      '.agent-identity',
      '.agent-avatar',
      '.agent-tabs-bar',
      '.agent-overview',
      '.agent-panel',
    ];
    const reference = await Promise.all(
      selectors.map((selector) => page.locator(selector).boundingBox()),
    );
    const styles = await page
      .locator('.agent-hero')
      .evaluate((el) => ({ padding: getComputedStyle(el).padding }));
    await page.goto(`/#/teams/${pack.id}`);
    await expect(page.getByRole('heading', { name: pack.name, exact: true })).toBeVisible();
    await expect(page.locator('.page-header')).toHaveCount(0);
    for (const [i, selector] of selectors.entries()) {
      const bounds = (await page.locator(selector).boundingBox())!;
      expect(bounds.x, selector + ' x').toBe(reference[i]!.x);
      expect(bounds.y, selector + ' y').toBe(reference[i]!.y);
      expect(bounds.width, selector + ' width').toBe(reference[i]!.width);
      if (!['.agent-overview', '.agent-panel'].includes(selector))
        expect(bounds.height, selector + ' height').toBe(reference[i]!.height);
    }
    await expect(page.locator('.agent-hero')).toHaveCSS('padding', styles.padding);
    await expect(page.getByRole('heading', { name: 'Members', exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Leader', exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Auditor', exact: true })).toBeVisible();
    await expect(page.getByRole('tabpanel')).toContainText('Skills: search');
    await page.getByRole('tab', { name: 'Overview', exact: true }).focus();
    await page.keyboard.press('ArrowRight');
    await expect(page.getByRole('tab', { name: 'Agents', exact: true })).toBeFocused();
    await expect(page.getByRole('tabpanel').locator('.team-member-profile')).toHaveCount(4);
    await page.getByRole('tab', { name: 'Scientific settings', exact: true }).click();
    await expect(page.getByRole('tabpanel')).toContainText('biology');
    await expect(page.getByRole('tabpanel')).toContainText('12 steps per column');
    await expect(page.getByRole('tabpanel')).toContainText("Does it say what it couldn't do?");
    await page.getByRole('tab', { name: 'Versions', exact: true }).click();
    await expect(page.getByRole('table', { name: pack.name + ' versions' })).toContainText('v1');
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
    }
  });
}

test('Team profile tabs and roles remain readable on a narrow viewport', async ({ page }) => {
  const { pack } = await seed(page, 'narrow-team-profile');
  await page.setViewportSize({ width: 600, height: 900 });
  await page.goto(`/#/teams/${pack.id}`);
  await expect(page.locator('.agent-panel')).toBeVisible();
  expect(
    await page.locator('.team-detail-page').evaluate((el) => el.scrollWidth <= el.clientWidth),
  ).toBe(true);
  await page.getByRole('tab', { name: 'Versions', exact: true }).click();
  await expect(page.getByRole('table', { name: pack.name + ' versions' })).toBeVisible();
  await page.getByRole('tab', { name: 'Overview', exact: true }).click();
  await page.screenshot({ path: 'test-results/team-detail/narrow.png', fullPage: true });
});
