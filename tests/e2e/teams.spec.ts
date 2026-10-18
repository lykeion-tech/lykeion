import { expect, test, type Page } from '@playwright/test';
import { labPack, labSignIn, labWorkspace } from './lab';

const shots = 'test-results/teams-scoring';

test('the sidebar shows the final IT Department order', async ({ page }) => {
  await page.goto('/#/studies');
  const nav = page.getByRole('complementary', { name: 'Workspace navigation' });
  const names = await nav.getByRole('link').allInnerTexts();
  const at = (name: string) => names.findIndex((n) => n.trim() === name);
  expect(at('Teams')).toBeGreaterThan(at('Studies'));
  expect(at('Teams')).toBeGreaterThan(at('Colleagues'));
  expect(
    names
      .map((name) => name.trim())
      .filter((name) => ['Teams', 'Agents', 'Autoresearch', 'Machines'].includes(name)),
  ).toEqual(['Teams', 'Agents', 'Autoresearch', 'Machines']);
  await expect(nav.getByText('IT Department', { exact: true })).toBeVisible();
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

// A Team's agents come from the Agents page; the form refers to them by id.
async function formAgents(page: Page) {
  const ws = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': ws };
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), ws);
  const make = async (name: string, harness: string, model: string) =>
    (
      await (
        await page.request.post('/api/agents', {
          headers,
          data: { name: `${name} ${Date.now()}`, harness, model, instructions: `${name} works` },
        })
      ).json()
    ).name as string;
  return {
    lead: await make('Form lead', 'claude', 'opus'),
    worker: await make('Form worker', 'codex', ''),
    checker: await make('Form checker', 'claude', 'sonnet'),
  };
}

test('creating a Team from the template', async ({ page }) => {
  const a = await formAgents(page);
  const name = `scrna-${Date.now()}`;
  await page.goto('/#/teams/new');
  await page.getByLabel('Team name').fill(name);
  await page.getByLabel('Description', { exact: true }).fill('Evidence briefs for scRNA-seq');
  await page.getByLabel('Domain').fill('Computational biology');
  await page.getByLabel('Built for').selectOption('grounding');
  await page.getByLabel('Leader agent').selectOption({ label: a.lead });
  await page.getByLabel('Leader role').fill('Reads the question and names a member for each step');
  const member = page.locator('.team-member').first();
  await member.getByLabel('Member name').fill('bioinformatician');
  await member.getByLabel('Member agent').selectOption({ label: a.worker });
  await member.getByLabel('Member role').fill('Python scripts and alignment');
  await member.getByLabel('Add skill').selectOption('aeon');
  await member.getByLabel('Add connector').selectOption('uniprot');
  await member.getByLabel('Add connector').selectOption('url');
  await member.getByLabel('Connector name').fill('lab-mcp');
  await member.getByLabel('Connector URL').fill('https://mcp.example.org/sse');
  await page.getByRole('button', { name: '+ Add member' }).click();
  const second = page.locator('.team-member').nth(1);
  await second.getByLabel('Member name').fill('data-specialist');
  await second.getByLabel('Member agent').selectOption({ label: a.lead });
  await second.getByLabel('Member role').fill('Fetches records');
  await page.getByLabel('Auditor agent').selectOption({ label: a.checker });
  await page.getByLabel('Auditor role').fill('Checks every quote against its recorded call');
  await page.getByRole('button', { name: "+ This Team's own criterion" }).click();
  await page
    .getByLabel('Criterion 1', { exact: true })
    .fill('Every dataset named has its accession');
  await page.getByLabel('Steps per column').fill('20');
  await page.screenshot({ path: `${shots}/team-form.png`, fullPage: true });
  await page.getByRole('button', { name: 'Create Team' }).click();
  await expect(page.getByRole('heading', { name, level: 1 })).toBeVisible();
  await expect(page.getByText('bioinformatician', { exact: true })).toBeVisible();
  await expect(page.getByText('data-specialist', { exact: true })).toBeVisible();
  await expect(page.getByText('Skills: aeon')).toBeVisible();
  await expect(page.getByText('Every dataset named has its accession')).toBeVisible();
  await expect(page.getByText('20 steps per column')).toBeVisible();
});

test('the auditor must use another model', async ({ page }) => {
  const a = await formAgents(page);
  await page.goto('/#/teams/new');
  await page.getByLabel('Team name').fill('auditor-check');
  await page.getByLabel('Leader agent').selectOption({ label: a.lead });
  await page.getByLabel('Leader role').fill('Leads');
  const member = page.locator('.team-member').first();
  await member.getByLabel('Member name').fill('worker');
  await member.getByLabel('Member agent').selectOption({ label: a.worker });
  await member.getByLabel('Member role').fill('Works');
  await page.getByLabel('Auditor agent').selectOption({ label: a.checker });
  await page.getByLabel('Auditor role').fill('Audits');
  await expect(page.getByRole('button', { name: 'Create Team' })).toBeEnabled();
  await page.getByLabel('Auditor agent').selectOption({ label: a.lead });
  await expect(page.getByRole('alert')).toHaveText(
    'The auditor must run on a different model from the leader and every member',
  );
  await expect(page.getByRole('button', { name: 'Create Team' })).toBeDisabled();
  await page.screenshot({ path: `${shots}/team-form-auditor.png`, fullPage: true });
});

test('the core criteria cannot be removed', async ({ page }) => {
  await page.goto('/#/teams/new');
  for (const c of [
    'Does the work cite where its facts came from?',
    'Does it answer what the researcher asked?',
    "Does it say what it couldn't do?",
  ]) {
    const row = page.locator('.core-criterion').filter({ hasText: c });
    await expect(row).toBeVisible();
    await expect(row.getByRole('button')).toHaveCount(0);
  }
  await page.getByRole('button', { name: "+ This Team's own criterion" }).click();
  await expect(page.getByRole('button', { name: 'Remove criterion 1' })).toBeVisible();
  await page.getByRole('button', { name: 'Remove criterion 1' }).click();
  await expect(page.locator('.core-criterion')).toHaveCount(3);
  await expect(page.getByLabel('Criterion 1', { exact: true })).toHaveCount(0);
});

test('Team slots show all four real agent backends', async ({ page }) => {
  const ws = await labWorkspace(page.request);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), ws);
  const agents: { id: string; label: string }[] = [];
  for (const [harness, label] of [
    ['claude', 'Claude Code'],
    ['codex', 'Codex'],
    ['cursor', 'Cursor'],
    ['antigravity', 'Antigravity'],
  ]) {
    const response = await page.request.post('/api/agents', {
      headers: { 'X-Workspace-ID': ws },
      data: { name: `Team ${label}`, harness },
    });
    expect(response.status()).toBe(201);
    agents.push({ id: (await response.json()).id, label: label! });
  }
  await page.goto('/#/teams/new');
  const slot = page.locator('.slot').filter({ has: page.getByLabel('Leader agent') });
  for (const agent of agents) {
    await page.getByLabel('Leader agent').selectOption(agent.id);
    await expect(slot.locator('small')).toHaveText(agent.label);
  }
});
