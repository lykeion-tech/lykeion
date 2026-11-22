import { selectPicker } from './select-picker';
import { expect, test, type Page } from './test';
import { labWorkspace, labSignIn, LAB, newStudy } from './lab';

async function open(page: Page, name?: string) {
  await page.goto('/#/settings/connectors');
  await expect(page.getByRole('heading', { name: 'Connectors', exact: true })).toBeVisible();
  if (name) {
    await page.getByRole('searchbox', { name: 'Search connectors' }).fill(name);
    await page.getByRole('button', { name, exact: true }).click();
  }
}
async function catalogue(page: Page, name: string) {
  const ws = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': ws };
  const response = await page.request.get('/api/connectors', { headers });
  expect(response.status()).toBe(200);
  return {
    ws,
    headers,
    saved: (await response.json()).find((c: { name: string }) => c.name === name),
  };
}

test('catalogue overview is read-only and the key stays server-side', async ({ page }) => {
  const { headers, saved } = await catalogue(page, 'biogrid');
  expect(saved.policy_enabled).toBe(true);
  expect(saved.enabled).toBe(false);
  await open(page, 'biogrid');
  const dialog = page.getByRole('dialog', { name: 'biogrid connector', exact: true });
  await expect(dialog.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible();
  await expect(dialog.getByText(saved.purpose, { exact: true })).toBeVisible();
  await expect(dialog.getByText('Lykeion scientific catalogue', { exact: true })).toBeVisible();
  await expect(dialog.getByRole('tab')).toHaveCount(0);
  await expect(dialog.getByLabel('API key', { exact: true })).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: 'Save changes', exact: true })).toHaveCount(0);
  const secret = 'browser-key-unique-13';
  const put = await page.request.put('/api/connectors/biogrid', {
    headers,
    data: { revision: saved.revision, key: secret },
  });
  expect(put.status()).toBe(200);
  const list = await page.request.get('/api/connectors', { headers });
  expect(JSON.stringify(await list.json())).not.toContain(secret);
  const events = await page.request.get(
    `${LAB}/lab/connector-events?workspace=${await labWorkspace(page.request)}`,
  );
  expect(events.status()).toBe(200);
  expect(JSON.stringify(await events.json())).not.toContain(secret);
  await expect(dialog.getByText(secret)).toHaveCount(0);
});

test('members inspect a catalogue connector without any editing controls', async ({
  page,
  browser,
}) => {
  const { ws, headers } = await catalogue(page, 'kegg');
  const context = await browser.newContext();
  try {
    const email = `connector-admin-${Date.now()}@lab.test`;
    await labSignIn(context.request, email);
    await context.request.patch('/api/me', { data: { onboarded: true } });
    const invite = await page.request.post(`/api/workspaces/${ws}/invitations`, {
      headers,
      data: { email, role: 'admin' },
    });
    expect(invite.status()).toBe(201);
    const invitation = await invite.json();
    expect(
      (await context.request.post(`/api/invitations/${invitation.token}/accept`)).status(),
    ).toBe(200);
    const other = await context.newPage();
    await other.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), ws);
    await open(other, 'kegg');
    const dialog = other.getByRole('dialog', { name: 'kegg connector', exact: true });
    await expect(dialog.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible();
    await expect(dialog.getByRole('tab')).toHaveCount(0);
    await expect(dialog.getByRole('button', { name: 'Save changes', exact: true })).toHaveCount(0);
  } finally {
    await context.close();
  }
});

async function addURL(
  page: Page,
  name: string,
  url: string,
  secret = 'Bearer connector-browser-secret',
) {
  await open(page);
  await page.getByRole('button', { name: 'Add connector', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Add connector', exact: true });
  await dialog.getByRole('textbox', { name: 'Name', exact: true }).fill(name);
  await dialog.getByRole('textbox', { name: 'MCP URL', exact: true }).fill(url);
  await dialog.getByRole('button', { name: 'Add HTTP header', exact: true }).click();
  await dialog.getByRole('textbox', { name: 'Header name 1', exact: true }).fill('Authorization');
  await dialog.getByLabel('Header value 1', { exact: true }).fill(secret);
  await expect(dialog.getByLabel('Header value 1', { exact: true })).toHaveAttribute(
    'type',
    'password',
  );
  await dialog.getByRole('button', { name: 'Add connector', exact: true }).click();
  await expect(page.getByRole('dialog', { name: `${name} connector`, exact: true })).toBeVisible();
  const ws = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': ws };
  const list = await page.request.get('/api/mcp-servers', { headers });
  expect(list.status()).toBe(200);
  const rows = await list.json();
  expect(JSON.stringify(rows)).not.toContain(secret);
  expect(JSON.stringify(rows)).not.toContain(url);
  return { ws, headers, saved: rows.find((c: { name: string }) => c.name === name) };
}

test('custom MCP overview is read-only and its configuration stays write-only', async ({
  page,
}) => {
  const name = `custom-${Date.now()}`;
  const { ws, headers, saved } = await addURL(page, name, 'https://example.test/mcp');
  const dialog = page.getByRole('dialog', { name: `${name} connector`, exact: true });
  await expect(dialog.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible();
  await expect(dialog.locator('.chips').getByText('Remote URL', { exact: true })).toBeVisible();
  await expect(dialog.locator('dd').getByText('Remote URL', { exact: true })).toBeVisible();
  await expect(dialog.getByText('You', { exact: true })).toBeVisible();
  await expect(dialog.getByText('example.test')).toHaveCount(0);
  await expect(dialog.getByRole('tab')).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: 'Edit configuration', exact: true })).toHaveCount(
    0,
  );
  const replaced = await page.request.put(`/api/mcp-servers/${saved.id}`, {
    headers,
    data: {
      name,
      revision: saved.revision,
      config: { url: 'https://replacement.example.test/mcp' },
    },
  });
  expect(replaced.status()).toBe(200);
  const rows = await (await page.request.get('/api/mcp-servers', { headers })).json();
  expect(rows.find((c: { id: string }) => c.id === saved.id).revision).toBe(saved.revision + 1);
  expect(JSON.stringify(rows)).not.toContain('replacement.example');
  const events = await page.request.get(`${LAB}/lab/connector-events?workspace=${ws}`);
  expect(events.status()).toBe(200);
  expect(JSON.stringify(await events.json())).not.toContain('connector-browser-secret');
  const current = rows.find((c: { id: string }) => c.id === saved.id);
  expect(
    (
      await page.request.delete(`/api/mcp-servers/${saved.id}`, {
        headers,
        data: { revision: current.revision },
      })
    ).status(),
  ).toBe(204);
  const after = await (await page.request.get('/api/mcp-servers', { headers })).json();
  expect(after.some((c: { id: string }) => c.id === saved.id)).toBe(false);
});

test('URL and command forms execute through the real door', async ({ page }) => {
  const fixture = await (await page.request.get(`${LAB}/lab/connector-source`)).json();
  const name = `remote-${Date.now()}`;
  const { headers, ws } = await addURL(page, name, fixture.url);
  await page.keyboard.press('Escape');
  const card = await newStudy(page.request, 'Connector calls ' + Date.now(), { assignee: null });
  const remote = await page.request.post(`${LAB}/lab/connector-door`, {
    data: { workspace: ws, card: card.id, name },
  });
  expect(remote.status(), await remote.text()).toBe(200);
  const answer = await remote.json();
  expect(answer.answer).toContain('remote answer browser call');
  expect(answer.claim_configuration_private).toBe(true);
  await page.getByRole('button', { name: 'Add connector', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Add connector', exact: true });
  const commandName = `command-${Date.now()}`;
  await dialog.getByRole('textbox', { name: 'Name', exact: true }).fill(commandName);
  await dialog.getByRole('button', { name: 'Command', exact: true }).click();
  await dialog.getByRole('textbox', { name: 'Command', exact: true }).fill(fixture.command);
  for (const arg of ['remove me', 'one argument with spaces', '--literal=$HOME']) {
    await dialog.getByRole('button', { name: 'Add argument', exact: true }).click();
    const all = dialog.getByRole('textbox', { name: /^Argument / });
    await all.last().fill(arg);
  }
  await dialog.getByRole('button', { name: 'Remove argument 1', exact: true }).click();
  await expect(dialog.getByRole('textbox', { name: 'Argument 1', exact: true })).toHaveValue(
    'one argument with spaces',
  );
  for (const [name, value] of [
    ['LYK_UI_CONNECTOR_STDIO', '1'],
    ['LAB_MODE', 'non secret mode'],
  ]) {
    await dialog.getByRole('button', { name: 'Add environment variable', exact: true }).click();
    await dialog
      .getByRole('textbox', { name: /^Variable name / })
      .last()
      .fill(name);
    await dialog
      .getByLabel(/^Variable value /)
      .last()
      .fill(value);
  }
  await page.screenshot({ path: 'test-results/connectors/command-form.png' });
  await dialog.getByRole('button', { name: 'Add connector', exact: true }).click();
  await expect(
    page.getByRole('dialog', { name: `${commandName} connector`, exact: true }),
  ).toBeVisible();
  await page.keyboard.press('Escape');
  const command = await page.request.post(`${LAB}/lab/connector-door`, {
    data: { workspace: ws, card: card.id, name: commandName },
  });
  expect(command.status(), await command.text()).toBe(200);
  const called = await command.json();
  expect(called.answer).toContain('one argument with spaces');
  expect(called.answer).toContain('--literal=$HOME');
  expect(called.answer).toContain('non secret mode');
  const records = await page.request.get(`/api/cards/${card.id}/records?kind=call`, { headers });
  expect(records.status()).toBe(200);
  const calls = (await records.json()).filter((r: { kind: string }) => r.kind === 'call');
  expect(calls).toHaveLength(2);
  expect(JSON.stringify(calls)).not.toContain('connector-browser-secret');
  expect(JSON.stringify(calls)).toContain('remote answer browser call');
  const events = await page.request.get(`${LAB}/lab/connector-events?workspace=${ws}`);
  expect(events.status()).toBe(200);
  expect(JSON.stringify(await events.json())).not.toContain('connector-browser-secret');
});

test('licence-restricted and keyless connectors show their saved metadata in the overview', async ({
  page,
}) => {
  const { saved: kegg } = await catalogue(page, 'kegg');
  await open(page, 'kegg');
  const dialog = page.getByRole('dialog', { name: 'kegg connector', exact: true });
  await expect(dialog.getByText(kegg.domain, { exact: true })).toBeVisible();
  await expect(dialog.getByText(kegg.tier, { exact: true })).toBeVisible();
  await expect(dialog.getByRole('link', { name: /View source terms/ })).toHaveAttribute(
    'href',
    kegg.terms,
  );
  await expect(dialog.getByRole('checkbox')).toHaveCount(0);
  await page.keyboard.press('Escape');
  const { saved: geo } = await catalogue(page, 'geo');
  await open(page, 'geo');
  const geoDialog = page.getByRole('dialog', { name: 'geo connector', exact: true });
  await expect(geoDialog.getByText(geo.purpose, { exact: true })).toBeVisible();
  await expect(geoDialog.getByLabel('API key', { exact: true })).toHaveCount(0);
});

test('new connector forms guard cancel and route exits', async ({ page }) => {
  await open(page);
  await page.getByRole('button', { name: 'Add connector', exact: true }).click();
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill('unsaved-connection');
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue(
    'unsaved-connection',
  );
  await page.evaluate(() => (location.hash = '#/settings/skills'));
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue(
    'unsaved-connection',
  );
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('connector overview layout follows all supported themes and keyboard focus', async ({
  page,
}) => {
  for (const theme of ['ash', 'midnight', 'dawn', 'pale']) {
    await page.goto('/#/settings/preferences');
    await page
      .getByRole('button', { name: theme[0]!.toUpperCase() + theme.slice(1), exact: true })
      .click();
    await open(page);
    await page.getByRole('searchbox', { name: 'Search connectors' }).fill('biogrid');
    await expect(page.getByRole('button', { name: 'biogrid', exact: true })).toBeVisible();
    if (theme === 'ash') await page.screenshot({ path: 'test-results/connectors/list.png' });
    await page.getByRole('button', { name: 'biogrid', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Close connector', exact: true }).focus();
    await page.keyboard.press('Tab');
    await expect(page.getByRole('link', { name: /View source terms/ })).toBeFocused();
    await page.screenshot({ path: `test-results/connectors/${theme}-detail.png` });
    await page.keyboard.press('Escape');
    await expect(page.getByRole('button', { name: 'biogrid', exact: true })).toBeFocused();
  }
  await page.getByRole('button', { name: 'Add connector', exact: true }).click();
  await page.screenshot({ path: 'test-results/connectors/url-form.png' });
});

test('admin creates custom connectors and a demoted member can only inspect them', async ({
  page,
  browser,
}) => {
  const ws = await labWorkspace(page.request),
    headers = { 'X-Workspace-ID': ws };
  const email = `connectors-admin-${Date.now()}@lab.test`;
  const invitation = await (
    await page.request.post(`/api/workspaces/${ws}/invitations`, {
      headers,
      data: { email, role: 'admin' },
    })
  ).json();
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  try {
    await labSignIn(context.request, email);
    await context.request.patch('/api/me', { data: { onboarded: true } });
    expect(
      (await context.request.post(`/api/invitations/${invitation.token}/accept`)).status(),
    ).toBe(200);
    const me = await (await context.request.get('/api/me')).json();
    const editor = await context.newPage();
    await editor.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), ws);
    const name = `admin-mcp-${Date.now()}`;
    await open(editor);
    await editor.getByRole('button', { name: 'Add connector', exact: true }).click();
    await editor.getByRole('textbox', { name: 'Name', exact: true }).fill(name);
    await editor
      .getByRole('textbox', { name: 'MCP URL', exact: true })
      .fill('https://admin.example.test/mcp');
    await editor
      .getByRole('dialog', { name: 'Add connector', exact: true })
      .getByRole('button', { name: 'Add connector', exact: true })
      .click();
    await expect(
      editor.getByRole('dialog', { name: `${name} connector`, exact: true }),
    ).toBeVisible();
    expect(
      (
        await page.request.patch(`/api/workspaces/${ws}/members/${me.id}`, {
          headers,
          data: { role: 'member' },
        })
      ).status(),
    ).toBe(204);
    await editor.getByRole('button', { name: 'Close connector', exact: true }).click();
    await expect(editor.getByRole('button', { name: 'Add connector', exact: true })).toHaveCount(0);
    await editor.getByRole('button', { name: name, exact: true }).click();
    await expect(
      editor.getByRole('dialog', { name: `${name} connector`, exact: true }),
    ).toBeVisible();
  } finally {
    await context.close();
  }
});

test('Team picker submits only workspace identity and preserves removed references', async ({
  page,
}) => {
  const { headers, saved } = await addURL(
    page,
    `team-connection-${Date.now()}`,
    'https://team-private.example.test/mcp',
  );
  await page.keyboard.press('Escape');
  const make = async (name: string, model: string) => {
    const r = await page.request.post('/api/agents', {
      headers,
      data: {
        name: name + Date.now(),
        stage: 'experimentation',
        harness: 'claude',
        model,
        instructions: 'Works',
      },
    });
    expect(r.status()).toBe(201);
    return r.json();
  };
  const lead = await make('Connector leader', 'sonnet'),
    audit = await make('Connector auditor', 'opus');
  await page.goto('/#/teams/new');
  const teamName = `Connector Team ${Date.now()}`;
  await page.getByLabel('Team name', { exact: true }).fill(teamName);
  await selectPicker(page.getByLabel('Leader agent', { exact: true }), lead.id);
  await page.getByLabel('Leader role', { exact: true }).fill('Leads');
  await page.getByLabel('Member name', { exact: true }).fill('worker');
  await selectPicker(page.getByLabel('Member agent', { exact: true }), lead.id);
  await page.getByLabel('Member role', { exact: true }).fill('Works');
  await selectPicker(page.getByLabel('Auditor agent', { exact: true }), audit.id);
  await page.getByLabel('Auditor role', { exact: true }).fill('Audits');
  await selectPicker(page.getByLabel('Add connector', { exact: true }), 'workspace:' + saved.id);
  const request = page.waitForRequest(
    (r) => r.url().endsWith('/api/teams') && r.method() === 'POST',
  );
  await page.getByRole('button', { name: 'Create Team', exact: true }).click();
  const body = (await request).postDataJSON();
  expect(body.members[0].connectors).toEqual([{ name: saved.name, workspace: saved.id }]);
  expect(JSON.stringify(body)).not.toContain('team-private');
  await expect(page.getByRole('heading', { name: teamName, exact: true })).toBeVisible();
  await page.goto('/#/teams/new');
  await selectPicker(page.getByLabel('Add connector', { exact: true }), 'workspace:' + saved.id);
  expect(
    (
      await page.request.delete(`/api/mcp-servers/${saved.id}`, {
        headers,
        data: { revision: saved.revision },
      })
    ).status(),
  ).toBe(204);
  await expect(
    page.getByText(saved.name, { exact: false }).filter({ hasText: 'Unavailable' }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: `Remove connector ${saved.name}`, exact: true }),
  ).toBeVisible();
});

test('late connector creation stays in its captured workspace after discard', async ({ page }) => {
  const ws = await labWorkspace(page.request);
  const destination = await (
    await page.request.post('/api/workspaces', {
      data: { name: 'Connector destination', slug: `connector-destination-${Date.now()}` },
    })
  ).json();
  await open(page);
  await page.getByRole('button', { name: 'Add connector', exact: true }).click();
  const name = `late-connection-${Date.now()}`;
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill(name);
  await page
    .getByRole('textbox', { name: 'MCP URL', exact: true })
    .fill('https://late.example.test/mcp');
  let release!: () => void, started!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve)),
    observed = new Promise<void>((resolve) => (started = resolve));
  await page.route('**/api/mcp-servers', async (route) => {
    if (route.request().method() === 'POST') {
      started();
      await gate;
    }
    await route.continue();
  });
  await page
    .getByRole('dialog', { name: 'Add connector', exact: true })
    .getByRole('button', { name: 'Add connector', exact: true })
    .click();
  await observed;
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await page.locator('.workspace-switch').click();
  await page.getByRole('menuitem', { name: 'Connector destination', exact: false }).click();
  await expect(page.locator('.workspace-switch')).toHaveAccessibleDescription(
    'Connector destination',
  );
  release();
  await expect
    .poll(async () =>
      (
        await (
          await page.request.get('/api/mcp-servers', { headers: { 'X-Workspace-ID': ws } })
        ).json()
      ).some((c: { name: string }) => c.name === name),
    )
    .toBe(true);
  await open(page);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  const list = await (
    await page.request.get('/api/mcp-servers', { headers: { 'X-Workspace-ID': destination.id } })
  ).json();
  expect(list.some((c: { name: string }) => c.name === name)).toBe(false);
});

test('HTTP header duplicates ignore case while environment names retain case', async ({ page }) => {
  await open(page);
  await page.getByRole('button', { name: 'Add connector', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Add connector', exact: true });
  await dialog
    .getByRole('textbox', { name: 'Name', exact: true })
    .fill(`header-identity-${Date.now()}`);
  await dialog
    .getByRole('textbox', { name: 'MCP URL', exact: true })
    .fill('https://headers.example.test/mcp');
  for (const [index, name, value] of [
    [1, 'Authorization', 'first-private-value'],
    [2, 'authorization', 'second-private-value'],
  ] as const) {
    await dialog.getByRole('button', { name: 'Add HTTP header', exact: true }).click();
    await dialog.getByRole('textbox', { name: `Header name ${index}`, exact: true }).fill(name);
    await dialog.getByLabel(`Header value ${index}`, { exact: true }).fill(value);
  }
  const submit = dialog.getByRole('button', { name: 'Add connector', exact: true });
  await expect(submit).toBeDisabled();
  await expect(submit).toHaveAttribute('title', 'Each row needs a unique name.');
  await expect(dialog.getByRole('textbox', { name: 'Header name 1', exact: true })).toHaveValue(
    'Authorization',
  );
  await expect(dialog.getByRole('textbox', { name: 'Header name 2', exact: true })).toHaveValue(
    'authorization',
  );
  await expect(dialog.getByLabel('Header value 2', { exact: true })).toHaveValue(
    'second-private-value',
  );
  await dialog.getByRole('textbox', { name: 'Header name 2', exact: true }).fill('X-Lab-Other');
  const request = page.waitForRequest(
    (r) => r.url().endsWith('/api/mcp-servers') && r.method() === 'POST',
  );
  await submit.click();
  expect((await request).postDataJSON().config.headers).toEqual({
    Authorization: 'first-private-value',
    'X-Lab-Other': 'second-private-value',
  });
  await expect(dialog).toHaveCount(0);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Add connector', exact: true }).click();
  await dialog
    .getByRole('textbox', { name: 'Name', exact: true })
    .fill(`env-identity-${Date.now()}`);
  await dialog.getByRole('button', { name: 'Command', exact: true }).click();
  await dialog.getByRole('textbox', { name: 'Command', exact: true }).fill('printf');
  for (const [index, name, value] of [
    [1, 'Lab_Mode', 'first'],
    [2, 'lab_mode', 'second'],
  ] as const) {
    await dialog.getByRole('button', { name: 'Add environment variable', exact: true }).click();
    await dialog.getByRole('textbox', { name: `Variable name ${index}`, exact: true }).fill(name);
    await dialog.getByLabel(`Variable value ${index}`, { exact: true }).fill(value);
  }
  await expect(submit).toBeEnabled();
  const commandRequest = page.waitForRequest(
    (r) => r.url().endsWith('/api/mcp-servers') && r.method() === 'POST',
  );
  await submit.click();
  expect((await commandRequest).postDataJSON().config.env).toEqual({
    Lab_Mode: 'first',
    lab_mode: 'second',
  });
  await expect(dialog).toHaveCount(0);
});
