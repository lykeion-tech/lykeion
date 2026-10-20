import { expect, test } from '@playwright/test';
import { labFixture, labWorkspace } from './lab';

test('project editor follows the reference layout at the New study modal size and saves real fields', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto('/#/studies');
  await page.getByRole('button', { name: 'New', exact: true }).click();
  const study = page.getByRole('dialog', { name: 'New study', exact: true });
  await expect(study).toBeVisible();
  const studyBox = await study.boundingBox();
  await study.getByRole('button', { name: 'Close', exact: true }).click();
  await page.goto('/#/projects');
  const open = page.getByRole('button', { name: 'New Project', exact: true });
  await open.click();
  const dialog = page.getByRole('dialog', { name: 'New Project', exact: true });
  await expect(dialog.locator('.project-breadcrumb')).toContainText('Beta-cell lab');
  const box = await dialog.boundingBox();
  expect(box!.width).toBeCloseTo(studyBox!.width, 0);
  expect(box!.height).toBeCloseTo(studyBox!.height, 0);
  const title = dialog.getByRole('textbox', { name: 'Project name', exact: true });
  const description = dialog.getByRole('textbox', { name: 'Project description', exact: true });
  for (const label of ['Planned', 'No priority', 'Lead'])
    await expect(dialog.getByRole('button', { name: label, exact: true })).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Repos', exact: true })).toHaveCount(0);
  await expect(title).toHaveAttribute('placeholder', 'Project title');
  await expect(title).toHaveCSS('border-top-width', '0px');
  await expect(title).toBeFocused();
  await title.press('Tab');
  await expect(description).toBeFocused();
  await expect(description).toHaveCSS('box-shadow', 'none');
  await title.focus();
  await expect(title).toHaveCSS('box-shadow', 'none');
  await expect(description).toHaveAttribute('placeholder', 'Add description...');
  await expect(dialog.getByRole('button', { name: 'Create Project', exact: true })).toBeDisabled();
  await page.screenshot({ path: 'test-results/l2-transcript/new-project-empty-light.png' });
  await dialog.getByRole('button', { name: 'Expand project editor' }).click();
  await expect(dialog).toHaveCSS('width', '1100px');
  await dialog.getByRole('button', { name: 'Collapse project editor' }).click();
  expect((await dialog.boundingBox())!.height).toBeCloseTo(studyBox!.height, 0);
  const projectName = `Reference-style project ${Date.now()}`;
  const workspaceId = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': workspaceId };
  const me = await (await page.request.get('/api/me')).json();
  await dialog.getByRole('button', { name: 'Planned', exact: true }).click();
  await expect(
    dialog.getByRole('menu', { name: 'Project status' }).getByRole('menuitemradio'),
  ).toHaveText(['Planned', 'In Progress', 'Paused', 'Completed', 'Cancelled']);
  await page.keyboard.press('Escape');
  await expect(dialog.getByRole('menu', { name: 'Project status' })).toHaveCount(0);
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Planned', exact: true })).toBeFocused();
  await dialog.getByRole('button', { name: 'Planned', exact: true }).click();
  await dialog.getByRole('menuitemradio', { name: 'In Progress', exact: true }).click();
  await dialog.getByRole('button', { name: 'No priority', exact: true }).click();
  await expect(
    dialog.getByRole('menu', { name: 'Project priority' }).getByRole('menuitemradio'),
  ).toHaveText(['Urgent', 'High', 'Medium', 'Low', '—No priority']);
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await expect(dialog.getByRole('button', { name: 'High', exact: true })).toBeVisible();
  await dialog.getByRole('button', { name: 'Lead', exact: true }).click();
  await dialog.getByRole('textbox', { name: 'Search project leads' }).fill(me.name);
  await dialog.getByRole('menuitemradio', { name: me.name, exact: true }).click();
  await expect(dialog.getByRole('button', { name: me.name, exact: true })).toBeVisible();
  await title.fill(projectName);
  await description.fill('A recorded description for the new project.');
  await dialog.getByRole('button', { name: 'Create Project', exact: true }).click();
  await expect(page).toHaveURL(/#\/projects\/[a-f0-9-]+$/);
  const projectId = page.url().split('/').at(-1)!;
  const saved = await (await page.request.get(`/api/researches/${projectId}`, { headers })).json();
  expect(saved).toMatchObject({
    status: 'in_progress',
    priority: 'high',
    lead_type: 'member',
    lead_id: me.id,
  });
  await page.reload();
  await expect(page.getByRole('heading', { name: projectName, exact: true })).toBeVisible();
  await expect(page.locator('.description')).toHaveText(
    'A recorded description for the new project.',
  );
  await page.getByRole('button', { name: 'Edit Project', exact: true }).click();
  const edit = page.getByRole('dialog', { name: 'Edit Project', exact: true });
  await expect(edit.getByRole('textbox', { name: 'Project name' })).toHaveValue(projectName);
  await expect(edit.getByRole('button', { name: 'In Progress', exact: true })).toBeVisible();
  await expect(edit.getByRole('button', { name: 'High', exact: true })).toBeVisible();
  await edit.getByRole('button', { name: me.name, exact: true }).click();
  await expect(edit.getByRole('menuitemradio', { name: me.name, exact: true })).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await edit.getByRole('menuitemradio', { name: 'No lead', exact: true }).click();
  await edit.getByRole('textbox', { name: 'Project description' }).fill('Edited description.');
  await edit.getByRole('button', { name: 'Save Project', exact: true }).click();
  await expect(page.locator('.description')).toHaveText('Edited description.');
  await page.reload();
  await page.getByRole('button', { name: 'Edit Project', exact: true }).click();
  await expect(edit.getByRole('button', { name: 'Lead', exact: true })).toBeVisible();
  await expect(edit.getByRole('button', { name: 'In Progress', exact: true })).toBeVisible();
  await expect(edit.getByRole('button', { name: 'High', exact: true })).toBeVisible();
  const cleared = await (
    await page.request.get(`/api/researches/${projectId}`, { headers })
  ).json();
  expect(cleared).toMatchObject({
    lead_type: null,
    lead_id: null,
    status: 'in_progress',
    priority: 'high',
  });
  await edit.getByRole('button', { name: 'Close', exact: true }).click();
  await page.goto('/#/projects');
  await open.click();
  for (const label of ['Planned', 'No priority', 'Lead'])
    await expect(dialog.getByRole('button', { name: label, exact: true })).toBeVisible();
});

test('project editor fits narrow and dark views and closes with Escape', async ({ page }) => {
  await page.goto('/#/projects');
  const open = page.getByRole('button', { name: 'New Project', exact: true });
  await open.click();
  const dialog = page.getByRole('dialog', { name: 'New Project', exact: true });
  await expect(dialog.locator('.project-breadcrumb')).toBeVisible();
  await page.setViewportSize({ width: 600, height: 800 });
  const box = await dialog.boundingBox();
  expect(box!.x).toBeGreaterThanOrEqual(24);
  expect(box!.x + box!.width).toBeLessThanOrEqual(576);
  expect(await dialog.evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(0);
  await page.screenshot({ path: 'test-results/l2-transcript/new-project-narrow.png' });
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(open).toBeFocused();
  await page.evaluate(() =>
    localStorage.setItem('lykeion.preferences', JSON.stringify({ theme: 'midnight' })),
  );
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-palette', 'midnight');
  await open.click();
  await expect(dialog).toBeVisible();
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.screenshot({ path: 'test-results/l2-transcript/new-project-empty-dark.png' });
});

test('project leads include active agents, persist their selection and preserve an archived lead during edits', async ({
  page,
}) => {
  const fixture = await labFixture(page.request);
  const headers = { 'X-Workspace-ID': fixture.workspace_id };
  await page.addInitScript(
    (ws) => localStorage.setItem('lykeion.workspace', ws),
    fixture.workspace_id,
  );
  const created = await page.request.post('/api/agents', {
    headers,
    data: { name: 'Project curator', harness: 'claude', visibility: 'private' },
  });
  expect(created.status()).toBe(201);
  const agent = await created.json();
  await page.goto('/#/projects');
  await page.getByRole('button', { name: 'New Project', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'New Project', exact: true });
  await dialog
    .getByRole('textbox', { name: 'Project name', exact: true })
    .fill('Agent-led project');
  await dialog.getByRole('button', { name: 'Lead', exact: true }).click();
  await dialog.getByRole('textbox', { name: 'Search project leads' }).fill('Project curator');
  await page.keyboard.press('ArrowUp');
  await expect(
    dialog.getByRole('menuitemradio', { name: 'Project curator', exact: true }),
  ).toBeFocused();
  await dialog.getByRole('menuitemradio', { name: 'Project curator', exact: true }).click();
  await dialog.getByRole('button', { name: 'Create Project', exact: true }).click();
  await expect(page).toHaveURL(/#\/projects\/[a-f0-9-]+$/);
  const projectId = page.url().split('/').at(-1)!;
  await page.reload();
  await page.getByRole('button', { name: 'Edit Project', exact: true }).click();
  const edit = page.getByRole('dialog', { name: 'Edit Project', exact: true });
  await expect(edit.getByRole('button', { name: 'Project curator', exact: true })).toBeVisible();
  await edit.getByRole('button', { name: 'Close', exact: true }).click();
  expect((await page.request.post(`/api/agents/${agent.id}/archive`, { headers })).status()).toBe(
    200,
  );
  await page.reload();
  await page.getByRole('button', { name: 'Edit Project', exact: true }).click();
  await edit.getByRole('button', { name: 'Project curator', exact: true }).click();
  await expect(
    edit.getByRole('menuitemradio', { name: 'Project curator', exact: true }),
  ).toHaveCount(0);
  await edit.getByRole('textbox', { name: 'Project name', exact: true }).click();
  await expect(edit.getByRole('menu', { name: 'Project lead' })).toHaveCount(0);
  await edit
    .getByRole('textbox', { name: 'Project name', exact: true })
    .fill('Renamed agent-led project');
  await edit.getByRole('button', { name: 'Save Project', exact: true }).click();
  await expect(edit).toHaveCount(0);
  const saved = await (await page.request.get(`/api/researches/${projectId}`, { headers })).json();
  expect(saved).toMatchObject({
    name: 'Renamed agent-led project',
    lead_type: 'agent',
    lead_id: agent.id,
  });
});
