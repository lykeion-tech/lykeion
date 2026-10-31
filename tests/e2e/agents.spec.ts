import { selectPicker } from './select-picker';
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
        data: { name: 'Custom source agent', harness: 'claude' },
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
    (await page.request.post('/api/agents', { headers, data })).json();
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
  await expect(menu.getByRole('menuitemradio', { name: 'Mine 2' })).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await expect(menu.getByRole('menuitemradio', { name: 'All 2' })).toBeVisible();
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
  await expect(menu.getByRole('menuitemradio', { name: 'Mine 2' })).toBeFocused();
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
    data: { name: 'Agent to delete', harness: 'claude' },
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
    data: { name: 'Study agent', harness: 'claude' },
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
  const agent = await (
    await page.request.post('/api/agents', {
      headers,
      data: { name: 'Methods reviewer', harness: 'claude', description: 'Reviews methods' },
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
  await selectPicker(page.getByRole('combobox', { name: 'Harness' }), 'codex');
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

  expect((await page.request.delete(`/api/cards/${studyId}`, { headers })).ok()).toBeTruthy();
  page.once('dialog', (d) => d.accept());
  await page.getByRole('button', { name: 'Delete agent' }).click();
  await expect(page).toHaveURL(/#\/agents$/);
  await expect(page.getByRole('row').filter({ hasText: 'Methods lead' })).toHaveCount(0);
});

// Mount the real hidden components directly. No agent connector HTTP route or
// persistence is simulated; the harness observes their public event contract.
async function mountAgentConnectors(
  page: Page,
  servers: Array<{ name: string; url: string }> = [],
) {
  await inFixture(page);
  await page.goto('/#/agents');
  await page.evaluate(async (servers) => {
    const vuePath = (await (await fetch('/src/main.ts')).text()).match(
        /from ["']([^"']+)["']/,
      )![1]!,
      componentPath = '/src/features/agents/AgentConnectors.vue';
    const { createApp, h, reactive } = await import(vuePath);
    const { default: Panel } = await import(componentPath);
    const props = reactive({ servers, editable: true, archived: false });
    const events: Array<{ kind: string; value: unknown }> = [];
    const root = document.createElement('section');
    root.id = 'agent-connector-test';
    root.style.background = 'var(--surface)';
    document.body.append(root);
    const app = createApp({
      render: () =>
        h('div', [
          h(Panel, {
            ...props,
            onSave: (value: unknown) => events.push({ kind: 'save', value }),
            onRemove: (value: unknown) => events.push({ kind: 'remove', value }),
          }),
        ]),
    });
    const routerPath = '/src/router.ts';
    const { router } = await import(routerPath);
    app.use(router);
    app.mount(root);
    (window as unknown as { agentConnectors: unknown }).agentConnectors = { props, events, app };
  }, servers);
  return page.locator('#agent-connector-test');
}
async function connectorAccess(
  page: Page,
  change: {
    editable?: boolean;
    archived?: boolean;
    servers?: Array<{ name: string; url: string }>;
  },
) {
  await page.evaluate(
    (change) =>
      Object.assign(
        (window as unknown as { agentConnectors: { props: object } }).agentConnectors.props,
        change,
      ),
    change,
  );
}
async function connectorEvents(page: Page) {
  return page.evaluate(
    () =>
      (
        window as unknown as {
          agentConnectors: { events: Array<{ kind: string; value: unknown }> };
        }
      ).agentConnectors.events,
  );
}

test('agent connector controls obey ownership and archival', async ({ page }) => {
  const servers = [{ name: 'lab-search', url: 'https://tools.lab.org/mcp' }];
  const panel = await mountAgentConnectors(page, servers);
  await expect(panel.getByRole('button', { name: 'Add connector', exact: true })).toBeVisible();
  await panel.getByRole('button', { name: 'Edit lab-search' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit connector', exact: true });
  await dialog.getByRole('textbox', { name: 'Connector URL' }).fill('http://tools.lab.org/changed');
  await connectorAccess(page, { editable: false });
  await expect(dialog.getByRole('textbox', { name: 'Connector URL' })).toHaveAttribute(
    'readonly',
    '',
  );
  await expect(dialog.getByRole('button', { name: 'Save changes' })).toHaveCount(0);
  await expect(dialog.getByRole('textbox', { name: 'Connector URL' })).toHaveValue(
    'http://tools.lab.org/changed',
  );
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(dialog).toBeVisible();
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(panel.getByRole('button', { name: 'Add connector', exact: true })).toHaveCount(0);
  await panel.getByRole('button', { name: 'View lab-search' }).click();
  const read = page.getByRole('dialog', { name: 'Connector details', exact: true });
  await expect(read.getByRole('textbox', { name: 'Connector URL' })).toHaveValue(servers[0]!.url);
  await read.getByRole('button', { name: 'Close connector', exact: true }).click();
  await connectorAccess(page, { editable: true, archived: true });
  await expect(panel.getByRole('button', { name: 'Add connector', exact: true })).toHaveCount(0);
  await expect(panel.getByRole('button', { name: 'Remove lab-search' })).toHaveCount(0);
  await expect(panel).toContainText('archived');
  expect(await connectorEvents(page)).toEqual([]);
  await panel.getByRole('button', { name: 'View lab-search' }).click();
  await expect(
    page
      .getByRole('dialog', { name: 'Connector details', exact: true })
      .getByRole('textbox', { name: 'Connector name' }),
  ).toHaveAttribute('readonly', '');
});

test('agent connector form has no command or header fields and emits structured changes', async ({
  page,
}) => {
  const servers = [{ name: 'lab-search', url: 'https://tools.lab.org/mcp' }];
  const panel = await mountAgentConnectors(page, servers);
  await panel.getByRole('button', { name: 'Add connector', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Add connector', exact: true });
  await expect(dialog.getByRole('textbox')).toHaveCount(2);
  await expect(dialog.getByRole('textbox', { name: /command|header|environment/i })).toHaveCount(0);
  await dialog.getByRole('textbox', { name: 'Connector name' }).fill('sources');
  await dialog.getByRole('textbox', { name: 'Connector URL' }).fill('http://index.lab.org/mcp');
  await expect(dialog.getByRole('alert')).toContainText('reserved');
  await expect(dialog.getByRole('button', { name: 'Add connector', exact: true })).toBeDisabled();
  await dialog.getByRole('textbox', { name: 'Connector name' }).fill('lab-search');
  await expect(dialog.getByRole('alert')).toContainText('twice');
  await dialog.getByRole('textbox', { name: 'Connector name' }).fill('index');
  await dialog.getByRole('button', { name: 'Add connector', exact: true }).click();
  expect(await connectorEvents(page)).toEqual([
    { kind: 'save', value: [...servers, { name: 'index', url: 'http://index.lab.org/mcp' }] },
  ]);
  // Emission alone cannot acknowledge a persisted save.
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await panel.getByRole('button', { name: 'Remove lab-search' }).click();
  const remove = page.getByRole('dialog', { name: 'Remove connector', exact: true });
  await expect(remove).toContainText('lab-search');
  await expect(remove).toContainText('remote server');
  await expect(remove).toContainText('workspace connectors');
  await connectorAccess(page, { archived: true });
  await expect(
    remove.getByRole('button', { name: 'Remove connector', exact: true }),
  ).toBeDisabled();
  await connectorAccess(page, { archived: false });
  await remove.getByRole('button', { name: 'Remove connector', exact: true }).click();
  expect(await connectorEvents(page)).toEqual([
    { kind: 'save', value: [...servers, { name: 'index', url: 'http://index.lab.org/mcp' }] },
    { kind: 'remove', value: 'lab-search' },
  ]);
  await expect(panel.getByRole('link', { name: /Manage workspace connectors/ })).toHaveAttribute(
    'href',
    '#/settings/connectors',
  );
});

test('agent connector entry is hidden before T1', async ({ page }) => {
  const { headers } = await inFixture(page);
  const agent = await (
    await page.request.post('/api/agents', {
      headers,
      data: { name: 'Connector boundary', harness: 'claude' },
    })
  ).json();
  await page.goto('/#/agents/' + agent.id);
  await page.getByRole('tab', { name: 'Capabilities' }).click();
  await expect(page.getByRole('button', { name: 'Connectors', exact: true })).toHaveCount(0);
  await expect(page.getByRole('textbox', { name: 'Agent instructions' })).toBeVisible();
});

test('agent connector edit preserves peer servers and dirty snapshots require explicit discard', async ({
  page,
}) => {
  const servers = [
    { name: 'lab-search', url: 'https://tools.lab.org/mcp' },
    { name: 'index', url: 'http://index.lab.org/mcp' },
  ];
  const panel = await mountAgentConnectors(page, servers);
  await panel.getByRole('button', { name: 'Edit lab-search' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit connector', exact: true });
  await dialog.getByRole('textbox', { name: 'Connector URL' }).fill('https://tools.lab.org/new');
  await dialog.screenshot({ path: `${artifacts}/agent-connector-prepared-edit.png` });
  await expect(dialog).toHaveCSS('width', '450px');
  await dialog.getByRole('button', { name: 'Save changes' }).click();
  expect(await connectorEvents(page)).toEqual([
    { kind: 'save', value: [{ name: 'lab-search', url: 'https://tools.lab.org/new' }, servers[1]] },
  ]);
  await connectorAccess(page, {
    servers: [{ name: 'lab-search', url: 'https://tools.lab.org/remote' }, servers[1]!],
  });
  await expect(dialog.getByRole('textbox', { name: 'Connector URL' })).toHaveValue(
    'https://tools.lab.org/new',
  );
  await expect(dialog.getByRole('button', { name: 'Save changes' })).toBeDisabled();
  await dialog.getByRole('button', { name: 'Reload saved version' }).click();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(dialog.getByRole('textbox', { name: 'Connector URL' })).toHaveValue(
    'https://tools.lab.org/new',
  );
  await dialog.getByRole('button', { name: 'Reload saved version' }).click();
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await expect(dialog.getByRole('textbox', { name: 'Connector URL' })).toHaveValue(
    'https://tools.lab.org/remote',
  );
  await dialog
    .getByRole('textbox', { name: 'Connector URL' })
    .fill('https://tools.lab.org/unsaved');
  await page.mouse.click(5, 5);
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: 'Close connector', exact: true }).click();
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(panel).toContainText('https://tools.lab.org/remote');
  await expect(panel).toContainText(servers[1]!.url);
  await connectorAccess(page, {
    servers: Array.from({ length: 8 }, (_, n) => ({
      name: 'server-' + n,
      url: 'https://tools.lab.org/mcp',
    })),
  });
  await expect(panel.getByRole('button', { name: 'Add connector', exact: true })).toBeDisabled();
  await connectorAccess(page, { servers: [] });
  await expect(panel).toContainText('No connectors have been added to this agent.');
  await panel.screenshot({ path: `${artifacts}/agent-connectors-prepared-empty.png` });
});

test('supported backend selectors persist provider labels and expose truthful focused details', async ({
  page,
}) => {
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
    await expect(page.getByRole('combobox', { name: 'Harness' })).toHaveValue(harness!);
    {
      const trigger = page.getByRole('button', { name: 'Backend details', exact: true });
      await trigger.click();
      const detail = page.getByRole('dialog', { name: `${label} backend details` });
      await expect(detail).not.toContainText('Private only');
      await expect(detail).toContainText(harness!);
      await expect(detail).toContainText('Harness default');
      const response = await page.request.get('/api/runtimes', { headers });
      expect(response.status()).toBe(200);
      const reported = (
        (await response.json()) as {
          harness: string;
          custom_name: string | null;
          device_name: string;
          cli_version: string;
        }[]
      ).filter((runtime) => runtime.harness === harness);
      for (const runtime of reported) {
        const report = detail.locator('.runtime-report').filter({
          has: page.getByText(runtime.custom_name || runtime.device_name, { exact: true }),
        });
        await expect(report).toHaveCount(1);
        await expect(report.locator('dd').nth(2)).toHaveText(runtime.cli_version || 'Not reported');
      }
      if (!reported.length) await expect(detail).toContainText('Not reported');
      const agentURL = page.url();
      const canvases: string[] = [];
      for (const palette of ['ash', 'midnight', 'dawn', 'pale']) {
        await page.goto('/#/settings/preferences');
        await page
          .getByRole('button', { name: palette[0]!.toUpperCase() + palette.slice(1), exact: true })
          .click();
        await expect(page.locator('html')).toHaveAttribute('data-palette', palette);
        await page.goto(agentURL);
        await page.getByRole('tab', { name: 'Settings', exact: true }).click();
        await trigger.click();
        await expect(detail).toBeVisible();
        canvases.push(
          await page
            .locator('.backend-dialog')
            .evaluate((el) => getComputedStyle(el).backgroundColor),
        );
        await page.screenshot({
          path: `test-results/ui2-task16/${palette}-${harness}-backend.png`,
        });
        await page.keyboard.press('Escape');
        await expect(trigger).toBeFocused();
      }
      expect(new Set(canvases).size).toBe(4);
    }
  }
});

for (const width of [1512, 390]) {
  test(`prepared agent connectors use actual palettes and keyboard focus at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 982 });
    const colors: string[] = [];
    for (const theme of ['ash', 'midnight', 'dawn', 'pale']) {
      // The actual panel stays absent from production routes until T1.
      const panel = await mountAgentConnectors(page, [
        { name: 'lab-search', url: 'https://tools.lab.org/mcp' },
      ]);
      await page.setViewportSize({ width: 1512, height: 982 });
      await page.goto('/#/settings/preferences');
      await page
        .getByRole('button', { name: theme[0]!.toUpperCase() + theme.slice(1), exact: true })
        .click();
      await expect(page.locator('html')).toHaveAttribute('data-palette', theme);
      await page.setViewportSize({ width, height: width === 390 ? 844 : 982 });
      const trigger = panel.getByRole('button', { name: 'Add connector', exact: true });
      await trigger.scrollIntoViewIfNeeded();
      await trigger.click();
      const dialog = page.getByRole('dialog', { name: 'Add connector', exact: true });
      await expect(dialog).toBeVisible();
      colors.push(
        await dialog.evaluate(
          (el) => getComputedStyle(el).backgroundColor + ' / ' + getComputedStyle(el).color,
        ),
      );
      await dialog.getByRole('textbox', { name: 'Connector name' }).focus();
      await page.keyboard.press('Tab');
      await expect(dialog.getByRole('textbox', { name: 'Connector URL' })).toBeFocused();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await page.screenshot({
        path: `test-results/ui2/task18/screenshots/${width}-${theme}-prepared-connectors.png`,
      });
      await page.keyboard.press('Escape');
      await expect(trigger).toBeFocused();
      expect(await connectorEvents(page)).toEqual([]);
      await page.evaluate(() => {
        const host = window as unknown as { agentConnectors: { app: { unmount(): void } } };
        host.agentConnectors.app.unmount();
        document.getElementById('agent-connector-test')?.remove();
      });
    }
    expect(new Set(colors).size).toBe(4);
  });
}
