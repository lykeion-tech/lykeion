import { expect, test, type Page } from '@playwright/test';
import { labFixture } from './lab';

const artifacts = 'test-results/ui-lab/agents';

async function inFixture(page: Page) {
  const f = await labFixture(page.request);
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  return { f, headers: { 'X-Workspace-ID': f.workspace_id } };
}

test('the Agents list shows owner, access, harness and assigned studies per tab', async ({
  page,
}) => {
  const { f, headers } = await inFixture(page);
  const post = async (data: object) =>
    (await page.request.post('/api/agents', { headers, data })).json();
  const curator = await post({
    name: 'Evidence curator',
    harness: 'claude',
    description: 'Keeps sources traceable',
  });
  await post({ name: 'Private reviewer', harness: 'codex', visibility: 'private' });
  const retired = await post({ name: 'Retired analyst', harness: 'claude' });
  await page.request.post(`/api/agents/${retired.id}/archive`, { headers });
  await page.request.patch(`/api/cards/${f.card_id}/properties`, {
    headers,
    data: { assignee_type: 'agent', assignee_id: curator.id },
  });

  await page.goto('/#/agents');
  await expect(page.locator('#page-header-count')).toHaveText('2');
  const tabs = page.getByRole('tablist');
  await expect(tabs.getByRole('tab', { name: 'Mine 2' })).toHaveAttribute('aria-selected', 'true');
  await expect(tabs.getByRole('tab', { name: 'All 2' })).toBeVisible();
  await expect(tabs.getByRole('tab', { name: 'Archived 1' })).toBeVisible();
  const table = page.getByRole('table', { name: 'Agents' });
  const curatorRow = table.getByRole('row').filter({ hasText: 'Evidence curator' });
  await expect(curatorRow).toContainText('Keeps sources traceable');
  await expect(curatorRow).toContainText('Workspace');
  await expect(curatorRow).toContainText('Claude Code');
  await expect(curatorRow).toContainText('You');
  await expect(curatorRow.getByRole('cell').last()).toHaveText('1');
  const privateRow = table.getByRole('row').filter({ hasText: 'Private reviewer' });
  await expect(privateRow).toContainText('Owner only');
  await expect(privateRow).toContainText('Codex');
  await expect(privateRow.getByRole('cell').last()).toHaveText('0');
  await expect(table.getByRole('row').filter({ hasText: 'Retired analyst' })).toHaveCount(0);
  await page.screenshot({ path: `${artifacts}/agents-list.png` });

  await page.getByRole('textbox', { name: 'Search agents' }).fill('traceable');
  await expect(table.getByRole('row')).toHaveCount(2);
  await page.getByRole('textbox', { name: 'Search agents' }).fill('');
  await tabs.getByRole('tab', { name: 'Archived 1' }).click();
  await expect(table.getByRole('row').filter({ hasText: 'Retired analyst' })).toContainText(
    'Archived',
  );
  await expect(table.getByRole('row')).toHaveCount(2);
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

test.describe('new study', () => {
  test('a new study can be assigned to an agent or a colleague when it is created', async ({
    page,
  }) => {
    const { headers } = await inFixture(page);
    const agent = await (
      await page.request.post('/api/agents', {
        headers,
        data: { name: 'Gap analyst', harness: 'claude' },
      })
    ).json();
    const me = await (await page.request.get('/api/me')).json();
    const create = async (question: string, pick: string | RegExp) => {
      await page.getByRole('button', { name: 'New', exact: true }).click();
      const dialog = page.getByRole('dialog', { name: 'New study' });
      await dialog.getByRole('textbox', { name: 'Research question' }).fill(question);
      await dialog.getByRole('button', { name: 'Unassigned', exact: true }).click();
      await dialog.getByRole('menuitem', { name: pick }).click();
      return dialog;
    };
    const cardFor = async (question: string) =>
      (
        (await (await page.request.get('/api/cards', { headers })).json()) as Record<
          string,
          string
        >[]
      ).find((c) => c.question === question);

    await page.goto('/#/studies');
    const q1 = `Does the analyst own this? ${Date.now()}`;
    let dialog = await create(q1, 'Gap analyst');
    await expect(dialog.getByRole('button', { name: 'Assignee: Gap analyst' })).toBeVisible();
    await page.screenshot({ path: `${artifacts}/new-study-assignee.png` });
    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog).toHaveCount(0);
    await expect.poll(async () => (await cardFor(q1))?.assignee_id).toBe(agent.id);
    expect((await cardFor(q1))!.assignee_type).toBe('agent');

    const q2 = `Does a colleague own this? ${Date.now()}`;
    dialog = await create(q2, me.name || me.email);
    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog).toHaveCount(0);
    await expect.poll(async () => (await cardFor(q2))?.assignee_id).toBe(me.id);
    expect((await cardFor(q2))!.assignee_type).toBe('member');
  });
});

test('the agent page shows its studies and edits instructions, profile and access', async ({
  page,
}) => {
  const { f, headers } = await inFixture(page);
  const agent = await (
    await page.request.post('/api/agents', {
      headers,
      data: { name: 'Methods reviewer', harness: 'claude', description: 'Reviews methods' },
    })
  ).json();
  await page.request.patch(`/api/cards/${f.card_id}/properties`, {
    headers,
    data: { assignee_type: 'agent', assignee_id: agent.id },
  });
  const card = await (await page.request.get(`/api/cards/${f.card_id}`, { headers })).json();
  const read = async () => (await page.request.get(`/api/agents/${agent.id}`, { headers })).json();

  await page.goto('/#/agents/' + agent.id);
  await expect(page.getByRole('heading', { name: 'Methods reviewer', exact: true })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Breadcrumb' })).toContainText('Agents');
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
  await page.getByRole('combobox', { name: 'Harness' }).selectOption('codex');
  await page.getByRole('textbox', { name: 'Model' }).fill('gpt-5');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByRole('heading', { name: 'Methods lead', exact: true })).toBeVisible();
  const saved = await read();
  expect([saved.harness, saved.model]).toEqual(['codex', 'gpt-5']);
  await page.screenshot({ path: `${artifacts}/agent-settings.png` });

  await page.getByRole('button', { name: 'Access' }).click();
  await page.getByRole('radio', { name: /Only me/ }).check();
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect.poll(async () => (await read()).visibility).toBe('private');
  await expect(page.locator('.agent-meta')).toContainText('Personal');

  await page.getByRole('button', { name: 'Assign work' }).click();
  const dialog = page.getByRole('dialog', { name: 'New study' });
  await expect(dialog.getByRole('button', { name: 'Assignee: Methods lead' })).toBeVisible();
  await dialog.getByRole('button', { name: 'Close' }).click();

  page.once('dialog', (d) => d.accept());
  await page.getByRole('button', { name: 'Agent actions' }).click();
  await page.getByRole('menuitem', { name: 'Archive agent' }).click();
  await expect(page.locator('.agent-status')).toHaveText('Archived');
  await expect(page.getByRole('button', { name: 'Assign work' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Agent actions' })).toHaveCount(0);
});
