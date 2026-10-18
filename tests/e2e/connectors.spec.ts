import { expect, test, type Page } from '@playwright/test';
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

test('catalogue setup uses real readiness and key status', async ({ page }) => {
  const { headers, saved } = await catalogue(page, 'biogrid');
  await open(page, 'biogrid');
  const dialog = page.getByRole('dialog', { name: 'biogrid connector', exact: true });
  await expect(dialog.getByText(saved.reason, { exact: true })).toBeVisible();
  await dialog.getByRole('tab', { name: 'Configuration' }).click();
  await expect(dialog.getByRole('switch', { name: 'Enable connector' })).toBeChecked();
  expect(saved.policy_enabled).toBe(true);
  expect(saved.enabled).toBe(false);
  await expect(dialog.getByText('No key saved', { exact: true })).toBeVisible();
  const secret = 'browser-key-unique-13';
  const key = dialog.getByLabel('API key', { exact: true });
  await key.fill(secret);
  await expect(key).toHaveAttribute('type', 'password');
  await dialog.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(dialog.getByText('Key saved', { exact: true })).toBeVisible();
  const list = await page.request.get('/api/connectors', { headers });
  expect(JSON.stringify(await list.json())).not.toContain(secret);
  await key.fill('replacement-browser-key');
  const replaced = page.waitForResponse(
    (r) =>
      r.url().endsWith('/api/connectors/biogrid') &&
      r.request().method() === 'PUT' &&
      r.status() === 200,
  );
  await dialog.getByRole('button', { name: 'Save changes', exact: true }).click();
  await replaced;
  await expect(key).toHaveValue('');
  const events = await page.request.get(
    `${LAB}/lab/connector-events?workspace=${await labWorkspace(page.request)}`,
  );
  expect(events.status()).toBe(200);
  const payload = JSON.stringify(await events.json());
  expect(payload).not.toContain(secret);
  expect(payload).not.toContain('replacement-browser-key');
  await dialog.getByRole('button', { name: 'Clear saved key', exact: true }).click();
  await dialog.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(dialog.getByText('No key saved', { exact: true })).toBeVisible();
});

test('members inspect but only owner changes catalogue', async ({ page, browser }) => {
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
    await other.getByRole('tab', { name: 'Configuration' }).click();
    await expect(
      other.getByText('Only the workspace owner can change catalogue settings.', { exact: true }),
    ).toBeVisible();
    await expect(other.getByRole('button', { name: 'Save changes', exact: true })).toHaveCount(0);
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

test('custom MCP is admin managed and write-only', async ({ page }) => {
  const name = `custom-${Date.now()}`;
  const { ws, headers, saved } = await addURL(page, name, 'https://example.test/mcp');
  await page.getByRole('tab', { name: 'Configuration' }).click();
  await expect(
    page.getByText(
      'Saved configuration is write-only. Replacing it requires re-entering the complete connection.',
      { exact: true },
    ),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Edit configuration', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'MCP URL', exact: true })).toHaveValue('');
  await expect(page.getByLabel('Header value 1', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
  await page
    .getByRole('textbox', { name: 'MCP URL', exact: true })
    .fill('https://replacement.example.test/mcp');
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Edit configuration', exact: true })).toBeVisible();
  const rows = await (await page.request.get('/api/mcp-servers', { headers })).json();
  expect(rows.find((c: { id: string }) => c.id === saved.id).revision).toBe(saved.revision + 1);
  expect(JSON.stringify(rows)).not.toContain('replacement.example');
  const events = await page.request.get(`${LAB}/lab/connector-events?workspace=${ws}`);
  expect(events.status()).toBe(200);
  expect(JSON.stringify(await events.json())).not.toContain('connector-browser-secret');
  await page.getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(
    page.getByText(`Delete ${name} from this workspace? The remote server is not deleted.`, {
      exact: true,
    }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Delete connector', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
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

test('stale connector save keeps typed values', async ({ page }) => {
  const { headers, saved } = await catalogue(page, 'materials-project');
  await open(page, 'materials-project');
  await page.getByRole('tab', { name: 'Configuration' }).click();
  await page.getByLabel('API key', { exact: true }).fill('unsaved-browser-key');
  expect(
    (
      await page.request.put('/api/connectors/materials-project', {
        headers,
        data: { revision: saved.revision, key: 'other-browser-key' },
      })
    ).status(),
  ).toBe(200);
  await expect(
    page.getByText('Saved configuration changed. Your draft is preserved.', { exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel('API key', { exact: true })).toHaveValue('unsaved-browser-key');
  await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
});

test('restricted licence and optional-key states use saved server metadata', async ({ page }) => {
  await open(page, 'kegg');
  await page.getByRole('tab', { name: 'Configuration' }).click();
  await page.getByRole('checkbox', { name: /I confirm this workspace/ }).check();
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(page.getByText('Available to this workspace', { exact: true })).toBeVisible();
  await page.getByRole('switch', { name: 'Enable connector' }).uncheck();
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(
    page.getByText('the workspace owner enables `kegg` in Connectors', { exact: true }),
  ).toBeVisible();
  await page.keyboard.press('Escape');
  await open(page, 'geo');
  await page.getByRole('tab', { name: 'Configuration' }).click();
  await expect(page.getByText('No key saved', { exact: true })).toBeVisible();
  await expect(page.getByText('Available without a key.', { exact: true })).toBeVisible();
});

test('a real 409 keeps the connector key draft until explicit reload', async ({ page }) => {
  const { headers, saved } = await catalogue(page, 'core');
  await open(page, 'core');
  await page.getByRole('tab', { name: 'Configuration' }).click();
  await page.getByLabel('API key', { exact: true }).fill('keep-this-unsaved-key');
  await page.route('**/api/connectors/core', async (route) => {
    if (route.request().method() === 'PUT') {
      expect(
        (
          await page.request.put('/api/connectors/core', {
            headers,
            data: { revision: saved.revision, key: 'concurrent-core-key' },
          })
        ).status(),
      ).toBe(200);
    }
    await route.continue();
  });
  const refusal = page.waitForResponse(
    (r) =>
      r.url().endsWith('/api/connectors/core') &&
      r.request().method() === 'PUT' &&
      r.status() === 409,
  );
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await refusal;
  await expect(page.getByLabel('API key', { exact: true })).toHaveValue('keep-this-unsaved-key');
  await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
  await page.getByRole('button', { name: 'Reload saved version', exact: true }).click();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(page.getByLabel('API key', { exact: true })).toHaveValue('keep-this-unsaved-key');
  await page.getByRole('button', { name: 'Reload saved version', exact: true }).click();
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await expect(page.getByLabel('API key', { exact: true })).toHaveValue('');
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

test('reconnect refreshes metadata and preserves dirty configuration', async ({ page }) => {
  const { headers, saved } = await catalogue(page, 'lincs-l1000');
  await open(page, 'lincs-l1000');
  await page.getByRole('tab', { name: 'Configuration' }).click();
  await page.getByLabel('API key', { exact: true }).fill('offline-unsaved-key');
  await page.context().setOffline(true);
  expect(
    (
      await page.request.put('/api/connectors/lincs-l1000', {
        headers,
        data: { revision: saved.revision, key: 'new-saved-lincs-key' },
      })
    ).status(),
  ).toBe(200);
  await page.context().setOffline(false);
  await expect(page.getByText('Key saved', { exact: true })).toBeVisible();
  await expect(page.getByLabel('API key', { exact: true })).toHaveValue('offline-unsaved-key');
  await expect(
    page.getByText('Saved configuration changed. Your draft is preserved.', { exact: true }),
  ).toBeVisible();
});

test('connector C layout follows all supported themes and keyboard focus', async ({ page }) => {
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
    await page.getByRole('tab', { name: 'Configuration' }).click();
    await page.getByRole('button', { name: 'Close connector', exact: true }).focus();
    await page.keyboard.press('Tab');
    await expect(page.getByRole('tab', { name: 'Overview', exact: true })).toBeFocused();
    await page.screenshot({ path: `test-results/connectors/${theme}-detail.png` });
    await page.keyboard.press('Escape');
    await expect(page.getByRole('button', { name: 'biogrid', exact: true })).toBeFocused();
  }
  await page.getByRole('button', { name: 'Add connector', exact: true }).click();
  await page.screenshot({ path: 'test-results/connectors/url-form.png' });
});

test('admin creates custom connectors while members inspect and permission loss preserves replacement', async ({
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
    const saved = (await (await page.request.get('/api/mcp-servers', { headers })).json()).find(
      (c: { name: string }) => c.name === name,
    );
    await editor.getByRole('tab', { name: 'Configuration' }).click();
    await editor.getByRole('button', { name: 'Edit configuration', exact: true }).click();
    const draft = 'https://keep-admin-draft.example.test/mcp';
    await editor.getByRole('textbox', { name: 'MCP URL', exact: true }).fill(draft);
    await editor.route(`**/api/mcp-servers/${saved.id}`, async (route) => {
      if (route.request().method() === 'PUT')
        expect(
          (
            await page.request.patch(`/api/workspaces/${ws}/members/${me.id}`, {
              headers,
              data: { role: 'member' },
            })
          ).status(),
        ).toBe(204);
      await route.continue();
    });
    const refusal = editor.waitForResponse(
      (r) =>
        r.url().endsWith(`/api/mcp-servers/${saved.id}`) &&
        r.request().method() === 'PUT' &&
        r.status() === 403,
    );
    await editor.getByRole('button', { name: 'Save changes', exact: true }).click();
    await refusal;
    await expect(editor.getByRole('textbox', { name: 'MCP URL', exact: true })).toHaveValue(draft);
    await expect(editor.getByRole('textbox', { name: 'MCP URL', exact: true })).toBeDisabled();
    expect(
      (
        await page.request.patch(`/api/workspaces/${ws}/members/${me.id}`, {
          headers,
          data: { role: 'admin' },
        })
      ).status(),
    ).toBe(204);
    await expect(editor.getByRole('button', { name: 'Save changes', exact: true })).toBeEnabled();
    await expect(editor.getByRole('textbox', { name: 'MCP URL', exact: true })).toHaveValue(draft);
    await editor.unroute(`**/api/mcp-servers/${saved.id}`);
    await editor.getByRole('button', { name: 'Save changes', exact: true }).click();
    await expect(
      editor.getByRole('button', { name: 'Edit configuration', exact: true }),
    ).toBeVisible();
    expect(
      (
        await page.request.patch(`/api/workspaces/${ws}/members/${me.id}`, {
          headers,
          data: { role: 'member' },
        })
      ).status(),
    ).toBe(204);
    await expect(
      editor.getByRole('button', { name: 'Edit configuration', exact: true }),
    ).toHaveCount(0);
    await expect(editor.getByRole('button', { name: 'Delete', exact: true })).toHaveCount(0);
    await editor.getByRole('button', { name: 'Close', exact: true }).click();
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
      data: { name: name + Date.now(), harness: 'claude', model, instructions: 'Works' },
    });
    expect(r.status()).toBe(201);
    return r.json();
  };
  const lead = await make('Connector leader', 'sonnet'),
    audit = await make('Connector auditor', 'opus');
  await page.goto('/#/teams/new');
  const teamName = `Connector Team ${Date.now()}`;
  await page.getByLabel('Team name', { exact: true }).fill(teamName);
  await page.getByLabel('Leader agent', { exact: true }).selectOption(lead.id);
  await page.getByLabel('Leader role', { exact: true }).fill('Leads');
  await page.getByLabel('Member name', { exact: true }).fill('worker');
  await page.getByLabel('Member agent', { exact: true }).selectOption(lead.id);
  await page.getByLabel('Member role', { exact: true }).fill('Works');
  await page.getByLabel('Auditor agent', { exact: true }).selectOption(audit.id);
  await page.getByLabel('Auditor role', { exact: true }).fill('Audits');
  await page.getByLabel('Add connector', { exact: true }).selectOption('workspace:' + saved.id);
  const request = page.waitForRequest(
    (r) => r.url().endsWith('/api/teams') && r.method() === 'POST',
  );
  await page.getByRole('button', { name: 'Create Team', exact: true }).click();
  const body = (await request).postDataJSON();
  expect(body.members[0].connectors).toEqual([{ name: saved.name, workspace: saved.id }]);
  expect(JSON.stringify(body)).not.toContain('team-private');
  await expect(page.getByRole('heading', { name: teamName, exact: true })).toBeVisible();
  await page.goto('/#/teams/new');
  await page.getByLabel('Add connector', { exact: true }).selectOption('workspace:' + saved.id);
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
  await expect(page.locator('.workspace-switch')).toContainText('Connector destination');
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
