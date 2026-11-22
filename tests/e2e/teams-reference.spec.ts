import { expect, test } from './test';
import { labPack, labWorkspace } from './lab';

test('Teams uses the reference table and opens the reference-size scientific creation modal', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  const ws = await labWorkspace(page.request);
  await labPack(page.request, 'reference-layout-team', ws, {
    domain: 'biology',
    stage: 'grounding',
  });
  await page.goto('/#/teams');
  const search = page.getByRole('textbox', { name: 'Search Teams', exact: true });
  await expect(search).toHaveAttribute('placeholder', 'Search Teams...');
  await expect(search.locator('..')).toHaveClass('agent-search');
  await expect(search.locator('..')).toHaveCSS('width', '224px');
  await expect(search.locator('..')).toHaveCSS('height', '32px');
  const table = page.getByRole('table', { name: 'Teams', exact: true });
  await expect(table.getByRole('columnheader', { name: 'Name', exact: true })).toHaveCSS(
    'font-size',
    '13px',
  );
  const columns = ['Name', 'Agents', 'Stage', 'Source', 'Last updated'];
  for (const [i, name] of columns.entries())
    await expect(table.getByRole('columnheader').nth(i)).toHaveAccessibleName(name);
  await expect(table.locator('thead')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
  await expect(table.getByRole('combobox')).toHaveCount(0);
  await expect(page.locator('.store-filters')).toHaveCount(0);
  const filter = page.getByRole('button', { name: 'Filter', exact: true });
  await expect(filter).toHaveCSS('height', '32px');
  await filter.click();
  const popup = page.getByRole('dialog', { name: 'Filter Teams' });
  await expect(popup.getByRole('combobox', { name: 'Domain', exact: true })).toHaveAttribute(
    'data-value',
    '',
  );
  await expect(popup.getByRole('combobox', { name: 'Stage', exact: true })).toHaveAttribute(
    'data-value',
    '',
  );
  await expect(popup.getByText('Built for', { exact: true })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(popup).toHaveCount(0);
  await expect(filter).toBeFocused();
  const row = table.getByRole('row').filter({ hasText: 'reference-layout-team' });
  await expect(row).toContainText('Grounding');
  expect((await row.boundingBox())!.height).toBe(54);
  await expect(table.getByRole('columnheader').first()).toHaveCSS('padding-top', '6px');
  await expect(table.getByRole('columnheader').first()).toHaveCSS('font-weight', '400');
  await expect(page.getByRole('button', { name: 'Upload a Team' })).toHaveCount(0);
  await expect(page.locator('input[type="file"]')).toHaveCount(0);
  await page.screenshot({ path: 'test-results/teams-reference/list-light.png' });
  await page.locator('#page-header-actions').getByRole('link', { name: 'New Team' }).click();
  await expect(page).toHaveURL(/#\/teams\/new$/);
  const modal = page.getByRole('dialog', { name: 'Create Team', exact: true });
  await expect(modal).toBeVisible();
  expect((await modal.boundingBox())!.width).toBe(676);
  expect((await modal.boundingBox())!.height).toBeGreaterThanOrEqual(820);
  await expect(modal.getByRole('textbox', { name: 'Team name', exact: true })).toBeFocused();
  await expect(modal.getByRole('textbox', { name: 'Team name', exact: true })).toHaveAttribute(
    'maxlength',
    '64',
  );
  await expect(
    modal.getByRole('textbox', { name: 'Description', exact: true }),
  ).not.toHaveAttribute('maxlength');
  await expect(modal.getByRole('heading', { name: 'Leader Agent', exact: true })).toBeVisible();
  const leader = modal.getByRole('combobox', { name: 'Leader agent', exact: true });
  await leader.click();
  const choices = modal.getByRole('listbox', { name: 'Leader agent', exact: true });
  await expect(choices).toBeVisible();
  const triggerBounds = (await leader.boundingBox())!;
  const menuBounds = (await choices.boundingBox())!;
  expect(menuBounds.x + menuBounds.width).toBeCloseTo(triggerBounds.x + triggerBounds.width, 0);
  await page.keyboard.press('Escape');
  await expect(
    modal.getByRole('heading', { name: 'Additional Members', exact: true }),
  ).toBeVisible();
  await expect(modal.getByRole('combobox', { name: 'Auditor agent', exact: true })).toHaveCount(1);
  await expect(modal.getByLabel('Auditor role')).toHaveCount(1);
  await expect(modal.getByLabel('Steps per column')).toHaveAttribute('max', '50');
  await expect(modal.getByRole('button', { name: 'Create Team', exact: true })).toBeDisabled();
  await page.screenshot({ path: 'test-results/teams-reference/modal-top-light.png' });
  const footer = await modal.locator('footer').boundingBox();
  await modal.getByLabel('Steps per column').scrollIntoViewIfNeeded();
  expect((await modal.locator('footer').boundingBox())!.y).toBe(footer!.y);
  await page.screenshot({ path: 'test-results/teams-reference/modal-light.png' });
  await page.keyboard.press('Escape');
  await expect(page).toHaveURL(/#\/teams$/);
  await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('direct Team creation keeps the footer accessible on a narrow screen', async ({ page }) => {
  await page.setViewportSize({ width: 600, height: 800 });
  await page.goto('/#/teams/new');
  const modal = page.getByRole('dialog', { name: 'Create Team', exact: true });
  await expect(modal).toBeVisible();
  const bounds = await modal.boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(16);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(584);
  await modal.getByLabel('Steps per column').scrollIntoViewIfNeeded();
  await expect(modal.getByLabel('Steps per column')).toBeVisible();
  await expect(modal.getByRole('button', { name: 'Cancel', exact: true })).toBeVisible();
  expect(
    await modal.locator('.team-editor-body').evaluate((el) => el.scrollWidth <= el.clientWidth),
  ).toBe(true);
  await page.screenshot({ path: 'test-results/teams-reference/modal-narrow.png' });
  await modal.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page).toHaveURL(/#\/teams$/);
});

test('Teams sorts by creation and shares the first column and Projects heading style', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  const ws = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': ws };
  expect(
    (
      await page.request.post('/api/agents', {
        headers,
        data: { name: 'Directory layout agent', stage: 'experimentation', harness: 'claude' },
      })
    ).status(),
  ).toBe(201);
  expect(
    (
      await page.request.post('/api/researches', {
        headers,
        data: { name: 'Directory layout project' },
      })
    ).status(),
  ).toBe(201);
  const first = await labPack(page.request, 'sort-earlier-team', ws);
  const second = await labPack(page.request, 'sort-later-team', ws);
  const response = await page.request.get('/api/teams', { headers: { 'X-Workspace-ID': ws } });
  expect(response.status()).toBe(200);
  const records = (await response.json()) as { id: string; created_at: string }[];
  expect(
    records.find((t) => t.id === first.id)!.created_at <
      records.find((t) => t.id === second.id)!.created_at,
  ).toBe(true);
  await page.goto('/#/teams');
  await page.getByRole('textbox', { name: 'Search Teams', exact: true }).fill('sort-');
  const rows = page.locator('.team-table tbody tr');
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toContainText('sort-later-team');
  const sort = page.getByRole('button', { name: 'Last updated', exact: true });
  await expect(sort).toHaveCSS('height', '32px');
  await sort.click();
  await expect(rows.first()).toContainText('sort-earlier-team');
  await sort.click();
  await expect(rows.first()).toContainText('sort-later-team');
  await expect(page.locator('.team-table th').first()).toHaveCSS('padding-bottom', '6px');
  const teamSort = (await sort.boundingBox())!;
  const teamNew = (await page.locator('#page-header-actions > *').first().boundingBox())!;
  expect(teamSort.x + teamSort.width).toBe(teamNew.x + teamNew.width);
  expect((await page.locator('.agent-search').boundingBox())!.x).toBe(
    (await page.locator('.page-header h1').boundingBox())!.x,
  );
  await expect(page.locator('.team-table th').last()).toHaveCSS('text-align', 'right');
  const lastValue = rows.first().locator('td').last().locator('.directory-last-value');
  await expect(lastValue).toHaveCSS('text-align', 'center');
  await expect(lastValue).toHaveCSS('width', '80px');
  await expect(lastValue).toHaveCSS('white-space', 'nowrap');
  const lastCell = (await rows.first().locator('td').last().boundingBox())!;
  const lastBox = (await lastValue.boundingBox())!;
  expect(lastBox.x + lastBox.width).toBe(lastCell.x + lastCell.width);
  const teamIcon = (await rows.first().locator('.team-icon').boundingBox())!;
  const teamGlyph = (await rows.first().locator('.team-icon svg').boundingBox())!;
  const teamLabelX = (await rows.first().locator('.team-label').boundingBox())!.x;
  expect(teamIcon.width).toBe(32);
  expect(teamIcon.height).toBe(32);
  expect(teamGlyph.x + teamGlyph.width / 2).toBe(teamIcon.x + teamIcon.width / 2);
  const teamNameStyle = await rows
    .first()
    .locator('.team-label')
    .evaluate((el) => {
      const css = getComputedStyle(el);
      return { size: css.fontSize, weight: css.fontWeight, color: css.color };
    });
  const teamMetadataStyle = await rows
    .first()
    .locator('td')
    .nth(1)
    .evaluate((el) => {
      const css = getComputedStyle(el);
      return { size: css.fontSize, weight: css.fontWeight, color: css.color };
    });
  const teamHeaderY = (await page.locator('.team-table th').first().boundingBox())!.y;
  const teamPositions = await page
    .locator('.team-table th')
    .evaluateAll((els) => els.map((el) => el.getBoundingClientRect().x));
  await page.goto('/#/agents');
  await expect(page.locator('.agent-head')).toBeVisible();
  await expect(page.locator('#page-header-count')).toBeEmpty();
  const agentPositions = await page
    .locator('.agent-head > span')
    .evaluateAll((els) => els.map((el) => el.getBoundingClientRect().x));
  // The Teams table shares the Agents table's first column; its four trailing
  // columns are evenly spaced.
  expect(teamPositions[0]).toBe(agentPositions[0]);
  expect(teamPositions.slice(2).map((x, i) => x - teamPositions[i + 1]!)).toEqual([132, 132, 132]);
  const agentIcon = (await page.locator('.agent-cell > .avatar').first().boundingBox())!;
  expect(agentIcon.x + agentIcon.width / 2).toBe(teamIcon.x + teamIcon.width / 2);
  expect((await page.locator('.agent-cell > span:last-child').first().boundingBox())!.x).toBe(
    teamLabelX,
  );
  const agentNameStyle = await page
    .locator('.agent-cell b')
    .first()
    .evaluate((el) => {
      const css = getComputedStyle(el);
      return { size: css.fontSize, weight: css.fontWeight, color: css.color };
    });
  const agentMetadataStyle = await page
    .locator('a.agent-row > span')
    .nth(3)
    .evaluate((el) => {
      const css = getComputedStyle(el);
      return { size: css.fontSize, weight: css.fontWeight, color: css.color };
    });
  expect(teamNameStyle).toEqual(agentNameStyle);
  expect(teamMetadataStyle).toEqual(agentMetadataStyle);
  const agentSort = (await page
    .getByRole('button', { name: 'Last updated', exact: true })
    .boundingBox())!;
  const agentNew = (await page
    .getByRole('button', { name: 'New agent', exact: true })
    .boundingBox())!;
  expect(agentSort.x + agentSort.width).toBe(agentNew.x + agentNew.width);
  expect((await page.locator('.agent-search').boundingBox())!.x).toBe(
    (await page.locator('.page-header h1').boundingBox())!.x,
  );
  await expect(page.locator('.agent-head > span').last()).toHaveCSS('text-align', 'right');
  expect(teamPositions[0]).toBe((await page.locator('.agent-search').boundingBox())!.x);
  expect((await page.locator('.agent-cell').first().boundingBox())!.x).toBe(agentPositions[0]);
  const style = await page
    .locator('.agent-head > span')
    .first()
    .evaluate((el) => {
      const css = getComputedStyle(el);
      return { size: css.fontSize, weight: css.fontWeight, color: css.color };
    });
  await page.goto('/#/projects');
  const heading = page.locator('.projects-table th').first();
  await expect(heading).toBeVisible();
  const projectPositions = await page
    .locator('.projects-table th')
    .evaluateAll((els) => els.map((el) => el.getBoundingClientRect().x));
  expect(projectPositions[0]).toBe((await page.locator('.page-header h1').boundingBox())!.x);
  const lastHeading = await page.locator('.projects-table th').last().boundingBox();
  const newProject = await page
    .getByRole('button', { name: 'New Project', exact: true })
    .boundingBox();
  expect(lastHeading!.x + lastHeading!.width).toBe(newProject!.x + newProject!.width);
  await expect(page.locator('.projects-table th').last()).toHaveCSS('text-align', 'right');
  // Priority, Status, Progress and Lead share the spacing; Created and the row actions are narrower.
  for (let i = 2; i <= 4; i++) expect(projectPositions[i]! - projectPositions[i - 1]!).toBe(134);
  const valuePositions = await page
    .locator('.projects-table tbody tr')
    .first()
    .locator('td')
    .evaluateAll((els) => els.map((el) => el.getBoundingClientRect().x));
  expect(valuePositions).toEqual(projectPositions);
  expect((await heading.boundingBox())!.y).toBe(teamHeaderY - 46);
  await expect(heading).toHaveCSS('border-bottom-width', '0px');
  await expect(heading).toHaveCSS('padding-bottom', '0px');
  expect((await heading.boundingBox())!.height).toBe(32);
  await expect(heading).toHaveCSS('font-size', style.size);
  await expect(heading).toHaveCSS('font-weight', style.weight);
  await expect(heading).toHaveCSS('color', style.color);
});

test('Teams toolbar aligns with the table right edge', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto('/#/teams');
  const last = (await page.locator('.team-table th').last().boundingBox())!;
  const sort = (await page
    .getByRole('button', { name: 'Last updated', exact: true })
    .boundingBox())!;
  expect(Math.round(sort.x + sort.width - (last.x + last.width))).toBe(0);
});
