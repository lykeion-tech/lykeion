import { expect, test, type Page } from '@playwright/test';
import { LAB, labFixture, labPack } from './lab';

const artifacts = 'test-results/ui-lab/agents';

// Seeds a study the agent works in the fixture's workspace, as an Autoresearch
// would open one; a researcher's studies are always worked by Teams.
async function assignNewStudy(page: Page, headers: Record<string, string>, agentId: string) {
  const res = await page.request.post(`${LAB}/lab/idle-study`, {
    headers,
    data: { question: `Assigned study ${Date.now()}?`, agent_id: agentId },
  });
  expect(res.status()).toBe(201);
  return ((await res.json()) as { id: string }).id;
}

async function inFixture(page: Page) {
  const f = await labFixture(page.request);
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  return { f, headers: { 'X-Workspace-ID': f.workspace_id } };
}

test('the Agent list shows each Agent source', async ({ page }) => {
  const { headers } = await inFixture(page);
  expect((await page.request.post('/api/coresearch/bootstrap', { headers })).status()).toBe(200);
  expect(
    (
      await page.request.post('/api/agents', {
        headers,
        data: { name: 'Custom source agent', stage: 'experimentation', harness: 'claude' },
      })
    ).status(),
  ).toBe(201);
  await page.goto('/#/agents');
  const table = page.getByRole('table', { name: 'Agents' });
  await expect(table.getByRole('columnheader', { name: 'Source' })).toBeVisible();
  await expect(table.getByRole('row').filter({ hasText: 'Aristotle' })).toContainText('Built-in');
  await expect(table.getByRole('row').filter({ hasText: 'Custom source agent' })).toContainText(
    'Custom',
  );
});

test('the Agents list filters Mine, All and Archived through one compact Filter button', async ({
  page,
}) => {
  const { headers } = await inFixture(page);
  const post = async (data: object) =>
    (await page.request.post('/api/agents', {
        headers,
        data: { stage: 'experimentation', ...data },
      })).json();
  await post({
    name: 'Evidence curator',
    harness: 'claude',
    description: 'Keeps sources traceable',
  });
  await post({ name: 'Private reviewer', harness: 'codex', visibility: 'private' });
  const retired = await post({ name: 'Retired analyst', harness: 'claude' });
  await page.request.post(`/api/agents/${retired.id}/archive`, { headers });

  await page.goto('/#/agents');
  await expect(page.locator('#page-header-count')).toBeEmpty();
  const filter = page.getByRole('button', { name: 'Filter', exact: true });
  await expect(filter).toBeVisible();
  const sort = page.getByRole('button', { name: 'Last updated', exact: true });
  expect((await filter.boundingBox())!.height).toBe((await sort.boundingBox())!.height);
  for (const property of [
    'padding-left',
    'padding-right',
    'font-size',
    'font-weight',
    'border-radius',
  ]) {
    await expect(filter).toHaveCSS(
      property,
      await sort.evaluate((el, p) => getComputedStyle(el).getPropertyValue(p), property),
    );
  }
  await expect(filter.locator('svg')).toHaveClass(/lucide-funnel/);
  await expect(page.locator('.agent-tabs')).toHaveCount(0);
  await filter.click();
  const menu = page.getByRole('menu', { name: 'Filter agents' });
  await expect(menu.getByRole('menuitemradio', { name: 'Mine 3' })).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await expect(menu.getByRole('menuitemradio', { name: 'All 3' })).toBeVisible();
  await expect(menu.getByRole('menuitemradio', { name: 'Archived 1' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(menu).toHaveCount(0);
  await expect(filter).toBeFocused();
  const table = page.getByRole('table', { name: 'Agents' });
  const curatorRow = table.getByRole('row').filter({ hasText: 'Evidence curator' });
  await expect(curatorRow).toContainText('Keeps sources traceable');
  await expect(curatorRow).toContainText('Custom');
  await expect(curatorRow).toContainText('Claude Code');
  await expect(curatorRow).toContainText('You');
  await expect(curatorRow.getByRole('cell')).toHaveCount(6);
  const privateRow = table.getByRole('row').filter({ hasText: 'Private reviewer' });
  await expect(privateRow).toContainText('Custom');
  await expect(privateRow).toContainText('Codex');
  await expect(privateRow.getByRole('cell')).toHaveCount(6);
  await expect(table.getByRole('columnheader')).toHaveText([
    'Name',
    'Status',
    'Owner',
    'Source',
    'Harness',
    'Last updated',
  ]);
  await expect(table.getByRole('columnheader', { name: 'Studies', exact: true })).toHaveCount(0);
  await expect(table.getByRole('row').filter({ hasText: 'Retired analyst' })).toHaveCount(0);
  await page.screenshot({ path: `${artifacts}/agents-list.png` });

  await page.getByRole('textbox', { name: 'Search agents' }).fill('traceable');
  await expect(table.getByRole('row')).toHaveCount(2);
  await page.getByRole('textbox', { name: 'Search agents' }).fill('');
  await filter.click();
  await menu.getByRole('menuitemradio', { name: 'Archived 1' }).click();
  await expect(menu).toHaveCount(0);
  await expect(filter).toBeFocused();
  await expect(table.getByRole('row').filter({ hasText: 'Retired analyst' })).toContainText(
    'Archived',
  );
  await expect(table.getByRole('row')).toHaveCount(2);
  await filter.press('ArrowDown');
  await page.keyboard.press('Home');
  await expect(menu.getByRole('menuitemradio', { name: 'Mine 3' })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(menu).toHaveCount(0);
  await expect(table.getByRole('row').filter({ hasText: 'Retired analyst' })).toHaveCount(0);
  await filter.click();
  await page.getByRole('textbox', { name: 'Search agents' }).click();
  await expect(menu).toHaveCount(0);
});

test('New agent opens the starting-point page, and Start blank opens the form', async ({
  page,
}) => {
  await inFixture(page);
  await page.goto('/#/agents');
  await page.getByRole('button', { name: 'New agent', exact: true }).click();
  await expect(page).toHaveURL(/#\/agents\/new$/);
  await expect(page.getByRole('heading', { name: 'Create an agent' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'How would you like to start?' })).toBeVisible();
  await expect(page.getByRole('button', { name: /Build with AI/ })).toBeDisabled();
  await page.screenshot({ path: `${artifacts}/agent-create.png` });
  await page.getByRole('button', { name: /Start blank/ }).click();
  const dialog = page.getByRole('dialog', { name: 'New agent', exact: true });
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page).toHaveURL(/#\/agents\/new$/);
  await page.getByRole('link', { name: 'Back to agents' }).click();
  await expect(page).toHaveURL(/#\/agents$/);
});

test('deletes an unused agent from its detail page', async ({ page }) => {
  const { headers } = await inFixture(page);
  const response = await page.request.post('/api/agents', {
    headers,
    data: { name: 'Agent to delete', stage: 'experimentation', harness: 'claude' },
  });
  expect(response.status()).toBe(201);
  const agent = await response.json();
  await page.goto('/#/agents/' + agent.id);
  await expect(page.getByRole('heading', { name: 'Agent to delete' })).toBeVisible();
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Delete agent' }).click();
  await expect(page).toHaveURL(/#\/agents$/);
  await expect(page.getByRole('row').filter({ hasText: 'Agent to delete' })).toHaveCount(0);
  expect((await page.request.get(`/api/agents/${agent.id}`, { headers })).status()).toBe(404);
});

test('starts a new study with the viewed agent selected', async ({ page }) => {
  const { headers } = await inFixture(page);
  const response = await page.request.post('/api/agents', {
    headers,
    data: { name: 'Study agent', stage: 'experimentation', harness: 'claude' },
  });
  expect(response.status()).toBe(201);
  const agent = await response.json();
  await page.goto('/#/agents/' + agent.id);
  await page.getByRole('button', { name: 'New study' }).click();
  await expect(page.getByRole('dialog')).toContainText('Study agent');
});

test('starts a new study with a Team copy or built-in Agent selected', async ({ page }) => {
  const { f, headers } = await inFixture(page);
  const team = await labPack(page.request, 'agent-study-team', f.workspace_id, {
    stage: 'grounding',
  });
  const added = await page.request.post(`/api/teams/${team.id}/add`, { headers });
  expect(added.status()).toBe(200);
  const copies = await added.json();
  const bootstrap = await page.request.post('/api/coresearch/bootstrap', { headers });
  expect(bootstrap.status()).toBe(200);
  const builtin = (await bootstrap.json()).agent;
  for (const agent of [copies[0], builtin]) {
    await page.goto('/#/agents/' + agent.id);
    await page.getByRole('button', { name: 'New study' }).click();
    await expect(
      page.getByRole('dialog').getByRole('button', { name: `Assignee: ${agent.name}` }),
    ).toBeVisible();
    await page.getByRole('dialog').getByRole('button', { name: 'Close' }).click();
  }
});

test('the agent page shows its studies and edits instructions, profile and access', async ({
  page,
}) => {
  const { headers } = await inFixture(page);
  const meId = (await (await page.request.get('/api/me')).json()).id;
  await page.route('**/api/runtimes', (r) =>
    r.fulfill({
      json: [
        {
          id: 'own',
          owner_id: meId,
          harness: 'codex',
          status: 'online',
          models: [{ value: 'gpt-lab', label: 'GPT Lab' }],
        },
      ],
    }),
  );
  const agent = await (
    await page.request.post('/api/agents', {
      headers,
      data: { name: 'Methods reviewer', stage: 'experimentation', harness: 'claude', description: 'Reviews methods' },
    })
  ).json();
  const studyId = await assignNewStudy(page, headers, agent.id);
  const card = await (await page.request.get(`/api/cards/${studyId}`, { headers })).json();
  const read = async () => (await page.request.get(`/api/agents/${agent.id}`, { headers })).json();

  await page.goto('/#/agents/' + agent.id);
  await expect(page.getByRole('heading', { name: 'Methods reviewer', exact: true })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Breadcrumb' })).toHaveCount(0);
  await expect(page.locator('.agent-meta')).toContainText('Harness default');
  await expect(page.locator('.agent-meta')).toContainText('Claude Code');
  await expect(page.locator('.agent-meta')).toContainText('Workspace');
  await expect(page.locator('.agent-activity')).toContainText(card.title || card.question);
  await page.screenshot({ path: `${artifacts}/agent-overview.png` });

  await page.getByRole('tab', { name: 'Work' }).click();
  const column = card.flag === 'escalated' ? 'Blocked' : null;
  const group = page
    .locator('.agent-group')
    .filter({ has: page.locator('.agent-study', { hasText: card.title || card.question }) });
  await expect(group).toHaveCount(1);
  if (column) await expect(group.locator('.agent-group-head')).toContainText(column);
  await page.getByRole('textbox', { name: 'Search studies' }).fill('no study matches this');
  await expect(page.locator('.agent-study')).toHaveCount(0);
  await page.screenshot({ path: `${artifacts}/agent-work.png` });

  await page.getByRole('tab', { name: 'Capabilities' }).click();
  await page.getByRole('textbox', { name: 'Agent instructions' }).fill('Cite every source.');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(async () => (await read()).instructions).toBe('Cite every source.');
  await page.screenshot({ path: `${artifacts}/agent-capabilities.png` });

  await page.getByRole('tab', { name: 'Settings' }).click();
  await page.getByRole('textbox', { name: 'Agent name' }).fill('Methods lead');
  await page.getByRole('button', { name: /^Harness:/ }).click();
  await page
    .getByRole('listbox', { name: 'Harness options' })
    .getByRole('option', { name: 'Codex', exact: true })
    .click();
  await page.getByRole('button', { name: /^Model:/ }).click();
  await page
    .getByRole('listbox', { name: 'Models' })
    .getByRole('option', { name: 'GPT Lab', exact: true })
    .click();
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByRole('heading', { name: 'Methods lead', exact: true })).toBeVisible();
  const saved = await read();
  expect([saved.harness, saved.model]).toEqual(['codex', 'gpt-lab']);
  await page.screenshot({ path: `${artifacts}/agent-settings.png` });

  await page.getByRole('button', { name: 'Access' }).click();
  await page.getByRole('radio', { name: /Only me/ }).check();
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect.poll(async () => (await read()).visibility).toBe('private');
  await expect(page.locator('.agent-meta')).toContainText('Personal');

  expect((await page.request.delete(`/api/cards/${studyId}`, { headers })).ok()).toBeTruthy();
  page.once('dialog', (d) => d.accept());
  await page.getByRole('button', { name: 'Delete agent' }).click();
  await expect(page).toHaveURL(/#\/agents$/);
  await expect(page.getByRole('row').filter({ hasText: 'Methods lead' })).toHaveCount(0);
});

test('supported backend selectors persist provider labels', async ({ page }) => {
  const { headers } = await inFixture(page);
  for (const [harness, label] of [
    ['claude', 'Claude Code'],
    ['codex', 'Codex'],
  ]) {
    await page.goto('/#/agents/new');
    await page.getByRole('button', { name: /Start blank/ }).click();
    const form = page.getByRole('dialog', { name: 'New agent', exact: true });
    await form.getByRole('button', { name: /^Harness:/ }).click();
    await expect(
      form.getByRole('listbox', { name: 'Harness options' }).getByRole('option'),
    ).toHaveCount(2);
    await form.getByRole('option', { name: label!, exact: true }).click();
    await form.getByRole('textbox', { name: 'Agent name', exact: true }).fill(`${label} task16`);
    await form.getByRole('button', { name: 'Create agent', exact: true }).click();
    await expect(page.locator('.agent-identity')).toContainText(label!);
    await page.getByRole('tab', { name: 'Settings', exact: true }).click();
    await expect(page.getByRole('button', { name: /^Harness:/ })).toHaveText(label!);
    const id = new URL(page.url().replace('#', '')).pathname.split('/').pop();
    const saved = await (await page.request.get(`/api/agents/${id}`, { headers })).json();
    expect(saved.harness).toBe(harness);
  }
});
