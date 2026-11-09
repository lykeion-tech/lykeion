import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { chooseCompact, labPack, withBuiltInTeams } from './lab';

async function workspace(request: APIRequestContext) {
  const created = await request.post('/api/workspaces', {
    data: { name: 'Stage Teams lab', slug: `stage-teams-${Date.now()}` },
  });
  expect(created.status()).toBe(201);
  return ((await created.json()) as { id: string }).id;
}
const builtInWorkspace = (request: APIRequestContext) => withBuiltInTeams(() => workspace(request));

async function openIn(page: Page, ws: string, path: string) {
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), ws);
  await page.goto(path);
}

test('built-in stage Teams show and filter by General-purpose domain', async ({ page }) => {
  const ws = await builtInWorkspace(page.request);
  await labPack(page.request, 'astronomy team', ws, { domain: 'astronomy', stage: 'grounding' });
  await openIn(page, ws, '/#/teams');
  const rows = page.locator('.team-table tbody tr');
  await expect(rows.filter({ hasText: 'Grounding Team' })).toContainText('General-purpose');
  await page.getByRole('button', { name: 'Filter', exact: true }).click();
  await chooseCompact(page, 'Domain', 'General-purpose');
  await expect(rows).toHaveCount(4);
  await expect(rows.filter({ hasText: 'astronomy team' })).toHaveCount(0);
  await rows.filter({ hasText: 'Grounding Team' }).getByRole('link').click();
  await expect(page.locator('.agent-meta')).toContainText('General-purpose');
  await page.getByRole('tab', { name: 'Settings' }).click();
  await expect(page.locator('.team-settings')).toContainText('General-purpose');
});

test('a study with no Team picked gets its stage’s built-in Team and the board starts at Grounding', async ({
  page,
}) => {
  const ws = await builtInWorkspace(page.request);
  expect(
    (
      await page.request.post('/api/agents', {
        headers: { 'X-Workspace-ID': ws },
        data: { name: 'Picker lab agent', stage: 'experimentation', harness: 'claude' },
      })
    ).status(),
  ).toBe(201);
  await openIn(page, ws, '/#/studies');
  await expect(page.getByRole('region', { name: 'Grounding', exact: true })).toBeVisible();
  await expect(page.locator('.board-column > header strong')).toHaveText([
    'Grounding',
    'Hypotheses',
    'Experimentation',
    'Validation',
    'Done',
    'Blocked',
  ]);
  await page.getByRole('button', { name: 'New', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'New study' });
  const question = `Which stage Team works an unpicked column? ${Date.now()}`;
  await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
  await expect(dialog.getByRole('button', { name: 'Assignees' })).toBeVisible();
  await dialog.getByRole('button', { name: 'Assignees' }).click();
  const menu = dialog.getByRole('menu');
  await expect(menu.getByRole('menuitem', { name: /^Hypotheses Team/ })).toBeVisible();
  await expect(menu.getByText('Agents', { exact: true })).toBeVisible();
  await expect(menu.getByRole('menuitem', { name: /Picker lab agent/ })).toBeVisible();
  await expect(menu.getByRole('menuitem', { name: 'Unassigned' })).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Assignees' }).click();
  await dialog.getByRole('button', { name: 'Stages: Grounding → Validation' }).click();
  const stages = dialog.getByRole('menu', { name: 'Study stages' });
  await stages.getByRole('menuitemcheckbox', { name: 'Grounding' }).click();
  await stages.getByRole('menuitemcheckbox', { name: 'Validation' }).click();
  await stages.getByRole('menuitemcheckbox', { name: 'Experimentation' }).click();
  const saved = page.waitForResponse(
    (r) => r.url().endsWith('/api/cards') && r.request().method() === 'POST',
  );
  await dialog.getByRole('button', { name: /^Create study/ }).click();
  const response = await saved;
  expect(response.status(), await response.text()).toBe(201);
  expect(response.request().postDataJSON().assignee).toBeUndefined();
  const study = (await response.json()) as {
    id: string;
    board_column: string;
    teams: { hypotheses: { name: string } };
  };
  expect(study.teams.hypotheses.name).toBe('Hypotheses Team');
  expect(study.board_column).toBe('hypotheses');
  await expect(
    page
      .getByRole('region', { name: 'Hypotheses', exact: true })
      .locator(`[data-card="${study.id}"]`),
  ).toBeVisible();
  await page.goto(`/#/studies/${study.id}`);
  const stored = (await (
    await page.request.get(`/api/cards/${study.id}`, { headers: { 'X-Workspace-ID': ws } })
  ).json()) as { teams: { hypotheses: { name: string } } };
  expect(stored.teams.hypotheses.name).toBe('Hypotheses Team');
});

test('a built-in Team is read-only and Duplicate this Team makes an editable copy', async ({
  page,
}) => {
  const ws = await builtInWorkspace(page.request);
  const headers = { 'X-Workspace-ID': ws };
  const teams = (await (await page.request.get('/api/teams', { headers })).json()) as {
    id: string;
    name: string;
    source: string;
  }[];
  const builtIn = teams.find((t) => t.name === 'Grounding Team')!;
  expect(builtIn.source).toBe('shipped');
  await openIn(page, ws, '/#/teams');
  const row = page.locator('.team-table tbody tr').filter({ hasText: 'Grounding Team' });
  await expect(row).toContainText('Built in');
  await expect(page.locator('.team-table')).not.toContainText(/version/i);
  await row.getByRole('link', { name: 'Grounding Team' }).click();
  await expect(page.getByRole('heading', { name: 'Grounding Team', exact: true })).toBeVisible();
  await expect(page.locator('.team-detail-page')).not.toContainText(/version/i);
  await expect(page.getByRole('button', { name: 'Add Team' })).toHaveCount(0);
  await expect(page.locator('.canvas-agent')).toHaveCount(4);
  await expect(page.getByRole('button', { name: /^Edit skills/ })).toHaveCount(0);
  const refused = await page.request.post(`/api/teams/${builtIn.id}/skills`, {
    headers,
    data: { member: 'pi', skills: [] },
  });
  expect(refused.status()).toBe(403);

  await page.getByRole('button', { name: 'Duplicate', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Duplicate this Team' });
  const name = dialog.getByRole('textbox', { name: 'Name of the duplicate' });
  await expect(name).toHaveValue('Grounding Team (duplicate)');
  await name.fill('My grounding Team');
  await dialog.getByRole('button', { name: 'Duplicate', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'My grounding Team', exact: true })).toBeVisible();
  await expect(page).not.toHaveURL(new RegExp(builtIn.id));
  await expect(page.getByRole('button', { name: 'Duplicate', exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Edit skills for pi', exact: true }).click();
  const picker = page.getByRole('dialog', { name: 'Skills for pi', exact: true });
  await picker.getByRole('checkbox', { name: 'aeon', exact: true }).check();
  await picker.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(picker).toHaveCount(0);
  await page.getByRole('button', { name: 'Edit skills for pi', exact: true }).click();
  await expect(picker.getByRole('checkbox', { name: 'aeon', exact: true })).toBeChecked();
  await page.keyboard.press('Escape');
  await expect(picker).toHaveCount(0);
});

test('a workspace without a built-in Team shows why a study can’t start', async ({ page }) => {
  const ws = await workspace(page.request);
  await openIn(page, ws, '/#/studies');
  await page.getByRole('button', { name: 'New', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'New study' });
  await dialog
    .getByRole('textbox', { name: 'Describe your study' })
    .fill(`Who works this without built-in Teams? ${Date.now()}`);

  await dialog.getByRole('button', { name: /^Create study/ }).click();
  await expect(dialog.getByRole('alert')).toHaveText('No built-in Team for Grounding');
});

test('custom Team settings save profile and workspace access', async ({ page }) => {
  const ws = await workspace(page.request);
  const team = await labPack(page.request, 'Team settings test', ws, { stage: 'grounding' });
  const headers = { 'X-Workspace-ID': ws };
  await openIn(page, ws, `/#/teams/${team.id}`);
  await page.getByRole('tab', { name: 'Settings' }).click();
  await page.getByRole('textbox', { name: 'Team name' }).fill('Renamed Team');
  await page.getByRole('textbox', { name: 'Team description' }).fill('Updated Team description');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByRole('heading', { name: 'Renamed Team' })).toBeVisible();
  await page.getByRole('button', { name: 'Access', exact: true }).click();
  await page.getByRole('radio', { name: /Entire workspace/ }).check();
  const saved = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/api/teams/${team.id}`) && response.request().method() === 'PATCH',
  );
  await page.getByRole('button', { name: 'Save changes' }).click();
  expect((await saved).status()).toBe(200);
  await expect(page.getByRole('button', { name: 'Save changes' })).toBeDisabled();
  const result = await page.request.get(`/api/teams/${team.id}`, { headers });
  expect(result.ok()).toBeTruthy();
  expect(await result.json()).toMatchObject({
    visibility: 'workspace',
    manifest: { name: 'Renamed Team', description: 'Updated Team description' },
  });
});
