import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { preregister, withBuiltInTeams } from './lab';

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

test('a study with no Team picked gets its stage’s built-in Team and the board starts at Grounding', async ({
  page,
}) => {
  const ws = await builtInWorkspace(page.request);
  await openIn(page, ws, '/#/studies');
  await expect(page.getByRole('region', { name: 'Grounding', exact: true })).toBeVisible();
  await expect(page.locator('.board-column > header strong')).toHaveText([
    'Grounding',
    'Hypotheses',
    'Experimentation',
    'Validation',
    'Blocked',
    'Done',
  ]);
  await page.getByRole('button', { name: 'New', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'New study' });
  const question = `Which stage Team works an unpicked column? ${Date.now()}`;
  await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
  await expect(dialog.getByRole('button', { name: 'Assignee: Built-in Teams' })).toBeVisible();
  await dialog.getByRole('button', { name: 'Assignee: Built-in Teams' }).click();
  const menu = dialog.getByRole('menu');
  await expect(menu.getByRole('menuitem', { name: 'Hypotheses Team' })).toBeVisible();
  await expect(menu.getByText('Members', { exact: true })).toHaveCount(0);
  await expect(menu.getByText('Agents', { exact: true })).toHaveCount(0);
  await expect(menu.getByRole('menuitem', { name: 'Unassigned' })).toHaveCount(0);
  await menu.getByRole('menuitem', { name: 'Built-in Teams' }).click();
  await dialog.getByRole('button', { name: 'Stages: Grounding → Validation' }).click();
  await dialog.getByRole('combobox', { name: 'From stage' }).selectOption('hypotheses');
  await dialog.getByRole('combobox', { name: 'To stage' }).selectOption('hypotheses');
  const saved = page.waitForResponse(
    (r) => r.url().endsWith('/api/cards') && r.request().method() === 'POST',
  );
  await dialog.getByRole('button', { name: /^Create study/ }).click();
  const response = await saved;
  expect(response.status(), await response.text()).toBe(201);
  expect(response.request().postDataJSON().assignee).toBeUndefined();
  const study = (await response.json()) as { id: string; board_column: string };
  expect(study.board_column).toBe('hypotheses');
  await expect(
    page
      .getByRole('region', { name: 'Hypotheses', exact: true })
      .locator(`[data-card="${study.id}"]`),
  ).toBeVisible();
  await page.goto(`/#/studies/${study.id}`);
  await expect(page.locator('.chip-team')).toHaveText('Hypotheses Team');
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
  await page.getByRole('tab', { name: 'Agents', exact: true }).click();
  await expect(page.locator('.team-member-profile')).toHaveCount(4);
  await expect(page.getByRole('button', { name: /^Edit skills/ })).toHaveCount(0);
  const refused = await page.request.post(`/api/teams/${builtIn.id}/skills`, {
    headers,
    data: { member: 'pi', skills: [] },
  });
  expect(refused.status()).toBe(403);

  await page.getByRole('button', { name: 'Duplicate this Team', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Duplicate this Team' });
  const name = dialog.getByRole('textbox', { name: 'Name of the duplicate' });
  await expect(name).toHaveValue('Grounding Team (duplicate)');
  await name.fill('My grounding Team');
  await dialog.getByRole('button', { name: 'Duplicate', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'My grounding Team', exact: true })).toBeVisible();
  await expect(page).not.toHaveURL(new RegExp(builtIn.id));
  await expect(page.getByRole('button', { name: 'Duplicate this Team' })).toHaveCount(0);
  await page.getByRole('tab', { name: 'Agents', exact: true }).click();
  await page.getByRole('button', { name: 'Edit skills for pi', exact: true }).click();
  const picker = page.getByRole('dialog', { name: 'Skills for pi', exact: true });
  await picker.getByRole('checkbox', { name: 'aeon', exact: true }).check();
  await picker.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(picker).toHaveCount(0);
  await expect(page.locator('.team-member-profile').first()).toContainText('aeon');
});

test('a workspace without a built-in Team shows why a study can’t start', async ({ page }) => {
  const ws = await workspace(page.request);
  await openIn(page, ws, '/#/studies');
  await page.getByRole('button', { name: 'New', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'New study' });
  await dialog
    .getByRole('textbox', { name: 'Describe your study' })
    .fill(`Who works this without built-in Teams? ${Date.now()}`);
  await preregister(dialog);
  await dialog.getByRole('button', { name: /^Create study/ }).click();
  await expect(dialog.getByRole('alert')).toHaveText('No built-in Team for Grounding');
});
