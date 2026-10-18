import { expect, test, type Page } from '@playwright/test';
import { labFixture } from './lab';

const artifacts = 'test-results/ui-lab/agents';

// Assigns the agent to a new Backlog study of the fixture's workspace, which
// starts it: a started study keeps its assignee.
async function assignNewStudy(page: Page, headers: Record<string, string>, agentId: string) {
  const res = await page.request.post('/api/cards', {
    headers,
    data: { question: `Assigned study ${Date.now()}?` },
  });
  expect(res.status()).toBe(201);
  const study = await res.json();
  const patched = await page.request.patch(`/api/cards/${study.id}/properties`, {
    headers,
    data: { assignee_type: 'agent', assignee_id: agentId },
  });
  expect(patched.status()).toBe(200);
  return study.id as string;
}

async function inFixture(page: Page) {
  const f = await labFixture(page.request);
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  return { f, headers: { 'X-Workspace-ID': f.workspace_id } };
}

test('the Agents list shows owner, access, harness and assigned studies per tab', async ({
  page,
}) => {
  const { headers } = await inFixture(page);
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
  await assignNewStudy(page, headers, curator.id);

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
      await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
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
      componentPath = '/src/components/AgentConnectors.vue';
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

test('four backend selectors persist provider labels and expose truthful focused details', async ({
  page,
}) => {
  await inFixture(page);
  for (const [harness, label] of [
    ['claude', 'Claude Code'],
    ['codex', 'Codex'],
    ['cursor', 'Cursor'],
    ['antigravity', 'Antigravity'],
  ]) {
    await page.goto('/#/agents/new');
    await page.getByRole('button', { name: /Start blank/ }).click();
    const form = page.getByRole('dialog', { name: 'New agent', exact: true });
    await expect(form.getByRole('combobox', { name: 'Harness' }).locator('option')).toHaveText([
      'Claude Code',
      'Codex',
      'Cursor',
      'Antigravity',
    ]);
    await form.getByRole('textbox', { name: 'Agent name', exact: true }).fill(`${label} task16`);
    await form.getByRole('combobox', { name: 'Harness' }).selectOption(harness!);
    await form.getByRole('button', { name: 'Create agent', exact: true }).click();
    await expect(page.locator('.agent-identity')).toContainText(label!);
    await page.getByRole('tab', { name: 'Settings', exact: true }).click();
    await expect(page.getByRole('combobox', { name: 'Harness' })).toHaveValue(harness!);
    if (harness === 'cursor' || harness === 'antigravity') {
      const trigger = page.getByRole('button', { name: 'Backend details', exact: true });
      await trigger.click();
      const detail = page.getByRole('dialog', { name: `${label} backend details` });
      await expect(detail).toContainText('Private only');
      await expect(detail).toContainText(harness === 'cursor' ? 'cursor-agent' : 'agy');
      await expect(detail).toContainText(harness === 'cursor' ? 'gpt-5' : 'gemini-3-pro');
      await expect(detail).toContainText('Supported through the tool door');
      await expect(detail).toContainText('Not reported');
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
