import { selectPicker } from './select-picker';
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import {
  labAutoresearchRun,
  labFixture,
  labMember,
  labRealtime,
  labReportAutoresearch,
  labSkillArchive,
} from './lab';

const headers = (workspace: string) => ({ 'X-Workspace-ID': workspace });
const content = (name: string, tail: string) =>
  `---\nname: ${name}\ndescription: Realtime evidence.\n---\n\n# Evidence\n${tail}\n`;
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
async function fresh(page: Page) {
  const f = await labFixture(page.request);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
  return f.workspace_id;
}
async function createSkill(request: APIRequestContext, workspace: string, name: string) {
  const response = await request.post('/api/skills', {
    headers: headers(workspace),
    data: { name, description: 'Realtime evidence.' },
  });
  expect(response.status()).toBe(201);
  return response.json();
}
async function editSkill(page: Page, name: string) {
  await page.goto('/#/settings/skills');
  await page.getByRole('searchbox', { name: 'Search skills' }).fill(name);
  await page.getByRole('button', { name, exact: true }).click();
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
}

test('colleague import refreshes open Settings and Team picker and deletion retains selected identity', async ({
  page,
  browser,
}) => {
  const ws = await fresh(page);
  const colleague = await labMember(browser, page.request, ws);
  const picker = await page.context().newPage();
  try {
    const settingsSocket = await labRealtime(page);
    const pickerSocket = await labRealtime(picker);
    await page.goto('/#/settings/skills');
    await picker.goto('/#/teams/new');
    await colleague.page.goto('/#/settings/skills');
    await settingsSocket.subscribed();
    await pickerSocket.subscribed();
    await expect(picker.getByRole('combobox', { name: 'Add skill', exact: true })).toBeVisible();
    const imported = await colleague.context.request.post('/api/skills/import', {
      headers: headers(ws),
      multipart: {
        file: {
          name: 'review-helper.zip',
          mimeType: 'application/zip',
          buffer: labSkillArchive('review-helper'),
        },
      },
    });
    expect(imported.status()).toBe(201);
    const skill = await imported.json();
    await expect(page.getByText('review-helper', { exact: true })).toBeVisible();
    await expect(
      picker
        .getByRole('combobox', { name: 'Add skill', exact: true })
        .locator('..')
        .locator('select option[value="review-helper"]'),
    ).toHaveCount(1);
    const makeAgent = async (name: string, model: string) => {
      const response = await page.request.post('/api/agents', {
        headers: headers(ws),
        data: { name, stage: 'experimentation', harness: 'claude', model, instructions: 'Works' },
      });
      expect(response.status()).toBe(201);
      return response.json();
    };
    const lead = await makeAgent('Realtime leader', 'sonnet');
    const audit = await makeAgent('Realtime auditor', 'opus');
    await picker.getByLabel('Team name', { exact: true }).fill('Realtime Team');
    await selectPicker(picker.getByLabel('Leader agent', { exact: true }), lead.id);
    await picker.getByLabel('Leader role', { exact: true }).fill('Leads');
    await picker.getByLabel('Member name', { exact: true }).fill('worker');
    await selectPicker(picker.getByLabel('Member agent', { exact: true }), lead.id);
    await picker.getByLabel('Member role', { exact: true }).fill('Works');
    await selectPicker(picker.getByLabel('Auditor agent', { exact: true }), audit.id);
    await picker.getByLabel('Auditor role', { exact: true }).fill('Audits');
    await selectPicker(
      picker.getByRole('combobox', { name: 'Add skill', exact: true }),
      'review-helper',
    );
    await expect(picker.getByRole('button', { name: 'Create Team', exact: true })).toBeEnabled();
    expect(
      (
        await colleague.context.request.delete(`/api/skills/${skill.id}`, {
          headers: headers(ws),
          data: { revision: skill.revision },
        })
      ).status(),
    ).toBe(204);
    await expect(page.getByRole('button', { name: 'review-helper', exact: true })).toHaveCount(0);
    await expect(
      picker
        .getByRole('combobox', { name: 'Add skill', exact: true })
        .locator('..')
        .locator('select option[value="review-helper"]'),
    ).toHaveCount(0);
    await expect(
      picker.getByRole('button', { name: 'Remove skill review-helper', exact: true }),
    ).toBeVisible();
    await expect(picker.getByRole('button', { name: 'Create Team', exact: true })).toBeDisabled();
    await expect(picker.getByRole('button', { name: 'Create Team', exact: true })).toHaveAttribute(
      'title',
      "Check worker's unavailable skills.",
    );
  } finally {
    await picker.close();
    await colleague.context.close();
  }
});

test('a custom connector appears for an open member page and its deletion shows it is gone', async ({
  page,
  browser,
}) => {
  const ws = await fresh(page);
  const member = await labMember(browser, page.request, ws);
  try {
    const socket = await labRealtime(member.page);
    await member.page.goto('/#/settings/connectors');
    await member.page.getByRole('button', { name: 'biogrid', exact: true }).click();
    const dialog = member.page.getByRole('dialog', { name: 'biogrid connector', exact: true });
    await expect(dialog.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible();
    await socket.subscribed();
    await expect(dialog.getByRole('button', { name: 'Save changes', exact: true })).toHaveCount(0);
    await member.page.keyboard.press('Escape');
    const created = await page.request.post('/api/mcp-servers', {
      headers: headers(ws),
      data: { name: 'realtime-connection', config: { command: 'fixture-command', args: [] } },
    });
    expect(created.status()).toBe(201);
    const custom = await created.json();
    await member.page.getByRole('button', { name: custom.name, exact: true }).click();
    const customDialog = member.page.getByRole('dialog', {
      name: `${custom.name} connector`,
      exact: true,
    });
    await expect(customDialog).toBeVisible();
    expect(
      (
        await page.request.delete(`/api/mcp-servers/${custom.id}`, {
          headers: headers(ws),
          data: { revision: custom.revision },
        })
      ).status(),
    ).toBe(204);
    await expect(
      member.page.getByText('This connector is no longer available.', {
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      member.page.getByRole('button', { name: 'Edit configuration', exact: true }),
    ).toHaveCount(0);
  } finally {
    await member.context.close();
  }
});

test('report event refreshes member history and selected run independently of execution status', async ({
  page,
  browser,
}) => {
  const f = await labReportAutoresearch(page.request);
  const run = await labAutoresearchRun(page.request, f.workspace_id, f.autoresearch_id, {
    status: 'running',
  });
  const member = await labMember(browser, page.request, f.workspace_id);
  try {
    const socket = await labRealtime(member.page);
    await member.page.goto(`/#/autoresearch/${f.autoresearch_id}`);
    await member.page.getByRole('button', { name: /Running.*View run/ }).click();
    const dialog = member.page.getByRole('dialog', { name: 'Autoresearch run', exact: true });
    await expect(dialog.getByText('Waiting for a report', { exact: true })).toBeVisible();
    await socket.subscribed();
    const beforeReport = socket.events().length;
    await labAutoresearchRun(page.request, f.workspace_id, f.autoresearch_id, {
      run_id: run,
      status: 'running',
      report: '# Colleague report\nrecorded-tail',
    });
    await expect.poll(() => socket.events()).toContain('autoresearch_run.reported');
    await expect(dialog.getByText('recorded-tail', { exact: true })).toBeVisible();
    expect(
      socket
        .events()
        .slice(beforeReport)
        .filter((type) => type.startsWith('autoresearch_run.')),
    ).toEqual(['autoresearch_run.reported']);
    await expect(member.page.getByRole('button', { name: /Running.*Open report/ })).toBeVisible();
    await expect(dialog.getByText('Report available · Run still in progress')).toBeVisible();
    await labAutoresearchRun(page.request, f.workspace_id, f.autoresearch_id, {
      run_id: run,
      status: 'failed',
      reason: 'fixture_failure_after_report',
      report: '# Colleague report\nretained-after-failure',
    });
    await expect(dialog.getByText('retained-after-failure', { exact: true })).toBeVisible();
    await expect.poll(() => socket.events()).toContain('autoresearch_run.failed');
    await expect(dialog.getByText('fixture_failure_after_report', { exact: true })).toBeVisible();
    await expect(dialog.getByText('Full report · As submitted by the agent')).toBeVisible();
    await expect(member.page.getByRole('button', { name: /Failed.*Open report/ })).toBeVisible();
  } finally {
    await member.context.close();
  }
});

test('dirty draft survives reconnect, rejects a real stale save and becomes unavailable on deletion', async ({
  page,
  browser,
}) => {
  const ws = await fresh(page);
  const colleague = await labMember(browser, page.request, ws);
  try {
    const skill = await createSkill(colleague.context.request, ws, 'concurrent-evidence');
    const socket = await labRealtime(page);
    await editSkill(page, skill.name);
    await socket.subscribed();
    const editor = page.getByRole('textbox', { name: 'SKILL.md', exact: true });
    const draft = content(skill.name, 'unsaved-local-evidence');
    await editor.fill(draft);
    const resume = await socket.pause();
    await expect(page.getByText('Reconnecting…', { exact: true })).toBeVisible();
    const remote = content(skill.name, 'colleague-saved-evidence');
    expect(
      (
        await colleague.context.request.patch(`/api/skills/${skill.id}`, {
          headers: headers(ws),
          data: { revision: skill.revision, content: remote },
        })
      ).status(),
    ).toBe(200);
    const refusal = page.waitForResponse(
      (r) =>
        r.url().endsWith(`/api/skills/${skill.id}`) &&
        r.request().method() === 'PATCH' &&
        r.status() === 409,
    );
    await page.getByRole('button', { name: 'Save changes', exact: true }).click();
    await refusal;
    await expect(editor).toHaveValue(draft);
    resume();
    await socket.subscribed(2);
    await expect(page.getByText('Reconnecting…', { exact: true })).toHaveCount(0);
    await expect(editor).toHaveValue(draft);
    await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
    await page.getByRole('button', { name: 'Reload saved version', exact: true }).click();
    await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
    await expect(editor).toHaveValue(draft);
    await page.getByRole('button', { name: 'Reload saved version', exact: true }).click();
    await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
    await expect(editor).toHaveValue(remote);
    const lastDraft = content(skill.name, 'draft-after-explicit-discard');
    await editor.fill(lastDraft);
    expect(
      (
        await colleague.context.request.delete(`/api/skills/${skill.id}`, {
          headers: headers(ws),
          data: { revision: 2 },
        })
      ).status(),
    ).toBe(204);
    await expect(
      page.getByText('This skill is no longer editable. Your draft is preserved.', { exact: true }),
    ).toBeVisible();
    await expect(editor).toHaveValue(lastDraft);
    await expect(editor).toHaveAttribute('readonly', '');
    await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Delete skill', exact: true })).toBeDisabled();
    await page.getByRole('tab', { name: 'Overview', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeDisabled();
  } finally {
    await colleague.context.close();
  }
});

test('permission loss revokes edit controls and restoration refetches without adopting a changed draft', async ({
  page,
  browser,
}) => {
  const ws = await fresh(page);
  const skill = await createSkill(page.request, ws, 'permission-evidence');
  const admin = await labMember(browser, page.request, ws, 'admin');
  try {
    const socket = await labRealtime(admin.page);
    await editSkill(admin.page, skill.name);
    await socket.subscribed();
    const editor = admin.page.getByRole('textbox', { name: 'SKILL.md', exact: true });
    const draft = content(skill.name, 'admin-local-draft');
    await editor.fill(draft);
    const role = async (value: string) =>
      expect(
        (
          await page.request.patch(`/api/workspaces/${ws}/members/${admin.userId}`, {
            headers: headers(ws),
            data: { role: value },
          })
        ).status(),
      ).toBe(204);
    await role('member');
    await expect(editor).toHaveAttribute('readonly', '');
    await expect(editor).toHaveValue(draft);
    await expect(
      admin.page.getByRole('button', { name: 'Save changes', exact: true }),
    ).toBeDisabled();
    expect(
      (
        await admin.context.request.patch(`/api/skills/${skill.id}`, {
          headers: headers(ws),
          data: { revision: 1, content: draft },
        })
      ).status(),
    ).toBe(403);
    await role('admin');
    await expect(
      admin.page.getByRole('button', { name: 'Save changes', exact: true }),
    ).toBeEnabled();
    await expect(editor).toHaveValue(draft);
    await role('member');
    await expect(editor).toHaveAttribute('readonly', '');
    expect(
      (
        await page.request.patch(`/api/skills/${skill.id}`, {
          headers: headers(ws),
          data: { revision: 1, content: content(skill.name, 'new-authoritative-version') },
        })
      ).status(),
    ).toBe(200);
    await role('admin');
    await expect(editor).not.toHaveAttribute('readonly', '');
    await expect(
      admin.page.getByText('The saved version has changed. Your draft is preserved.', {
        exact: true,
      }),
    ).toBeVisible();
    await expect(editor).toHaveValue(draft);
    await expect(
      admin.page.getByRole('button', { name: 'Save changes', exact: true }),
    ).toBeDisabled();
  } finally {
    await admin.context.close();
  }
});

test('reconnect during initial fetch cancels the old real snapshot before refreshing', async ({
  page,
  browser,
}) => {
  const ws = await fresh(page);
  const colleague = await labMember(browser, page.request, ws);
  const captured = deferred(),
    release = deferred();
  let requests = 0,
    deliveries = 0;
  let holdInitial = true;
  try {
    const socket = await labRealtime(page);
    await page.route('**/api/skills', async (route) => {
      if (route.request().method() !== 'GET' || !holdInitial) return route.continue();
      requests++;
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      expect(
        (await response.json()).some(
          (skill: { name: string }) => skill.name === 'arrived-during-first-fetch',
        ),
      ).toBe(false);
      captured.resolve();
      await release.promise;
      await route.fulfill({ response }).catch(() => {});
      deliveries++;
    });
    await page.goto('/#/studies');
    await socket.subscribed();
    await page.locator('.workspace-switch').click();
    await page.getByRole('menuitem', { name: 'Settings', exact: true }).click();
    await page.getByRole('button', { name: 'Skills', exact: true }).click();
    await captured.promise;
    const resume = await socket.pause();
    await createSkill(colleague.context.request, ws, 'arrived-during-first-fetch');
    holdInitial = false;
    resume();
    await socket.subscribed(2);
    await expect(
      page.getByRole('button', { name: 'arrived-during-first-fetch', exact: true }),
    ).toBeVisible();
    release.resolve();
    await expect.poll(() => deliveries).toBe(requests);
    await expect(
      page.getByRole('button', { name: 'arrived-during-first-fetch', exact: true }),
    ).toBeVisible();
  } finally {
    release.resolve();
    await colleague.context.close();
  }
});

test('workspace switch confirms dirty discard, closes modal and ignores a late real skill response', async ({
  page,
  browser,
}) => {
  const ws = await fresh(page);
  const colleague = await labMember(browser, page.request, ws);
  const destination = await (
    await page.request.post('/api/workspaces', {
      data: { name: 'Realtime destination', slug: `realtime-destination-${Date.now()}` },
    })
  ).json();
  const captured = deferred(),
    release = deferred(),
    delivered = deferred();
  let hold = false;
  try {
    const skill = await createSkill(colleague.context.request, ws, 'workspace-a-only-skill');
    await editSkill(page, skill.name);
    const editor = page.getByRole('textbox', { name: 'SKILL.md', exact: true });
    const draft = content(skill.name, 'workspace-A-only-draft');
    await editor.fill(draft);
    await page.route(`**/api/skills/${skill.id}`, async (route) => {
      if (!hold || route.request().method() !== 'GET') return route.continue();
      hold = false;
      const response = await route.fetch();
      captured.resolve();
      await release.promise;
      await route.fulfill({ response }).catch(() => {});
      delivered.resolve();
    });
    hold = true;
    expect(
      (
        await colleague.context.request.patch(`/api/skills/${skill.id}`, {
          headers: headers(ws),
          data: { revision: 1, content: content(skill.name, 'remote-before-switch') },
        })
      ).status(),
    ).toBe(200);
    await captured.promise;
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
    await expect(editor).toHaveValue(draft);
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await page.locator('.workspace-switch').click();
    await page.getByRole('menuitem', { name: 'Realtime destination', exact: false }).click();
    await expect(page.locator('.workspace-switch')).toContainText('Realtime destination');
    await expect(page.getByRole('dialog')).toHaveCount(0);
    release.resolve();
    await delivered.promise;
    await expect(page.getByText('workspace-a-only-skill', { exact: true })).toHaveCount(0);
    await expect(page.getByRole('dialog')).toHaveCount(0);
    expect(
      (
        await (await page.request.get('/api/skills', { headers: headers(destination.id) })).json()
      ).some((s: { name: string }) => s.name === skill.name),
    ).toBe(false);
  } finally {
    release.resolve();
    await colleague.context.close();
  }
});

test('late real run response cannot reopen a report modal after a workspace switch', async ({
  page,
  browser,
}) => {
  const f = await labReportAutoresearch(page.request);
  const run = await labAutoresearchRun(page.request, f.workspace_id, f.autoresearch_id, {
    status: 'running',
    report: '# Workspace A only report',
  });
  const member = await labMember(browser, page.request, f.workspace_id);
  const destination = await (
    await member.context.request.post('/api/workspaces', {
      data: { name: 'Run destination', slug: `run-destination-${Date.now()}` },
    })
  ).json();
  const captured = deferred(),
    release = deferred(),
    delivered = deferred();
  try {
    await member.page.route(`**/api/autoresearch-runs/${run}`, async (route) => {
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      expect(await response.json()).toMatchObject({
        autoresearch_id: f.autoresearch_id,
        execution_mode: 'run_only',
        run: {
          id: run,
          workspace_id: f.workspace_id,
          autoresearch_id: f.autoresearch_id,
          source: 'manual',
          status: 'running',
          report: '# Workspace A only report',
        },
      });
      captured.resolve();
      await release.promise;
      await route.fulfill({ response }).catch(() => {});
      delivered.resolve();
    });
    await member.page.goto(`/#/autoresearch/${f.autoresearch_id}`);
    await member.page.getByRole('button', { name: /Running.*Open report/ }).click();
    await captured.promise;
    await expect(
      member.page.getByRole('dialog', { name: 'Autoresearch run', exact: true }),
    ).toBeVisible();
    await member.page.getByRole('button', { name: 'Close run', exact: true }).click();
    await member.page.locator('.workspace-switch').click();
    await member.page.getByRole('menuitem', { name: 'Run destination', exact: false }).click();
    await expect(member.page.locator('.workspace-switch')).toContainText('Run destination');
    await expect(member.page.getByRole('dialog')).toHaveCount(0);
    release.resolve();
    await delivered.promise;
    await expect(member.page.getByText('Workspace A only report', { exact: true })).toHaveCount(0);
    await expect(member.page.getByRole('dialog')).toHaveCount(0);
    expect(
      (
        await member.context.request.get(`/api/autoresearch-runs/${run}`, {
          headers: headers(destination.id),
        })
      ).status(),
    ).toBe(404);
  } finally {
    release.resolve();
    await member.context.close();
  }
});
