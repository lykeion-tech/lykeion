import { expect, test, type APIRequestContext } from '@playwright/test';
import { labFixture } from './lab';

// A Backlog study in the fixture's workspace: the only kind whose owner may
// still reassign it.
async function backlogStudy(request: APIRequestContext, headers: Record<string, string>) {
  const res = await request.post('/api/cards', {
    headers,
    data: { question: `Backlog question ${Date.now()} ${Math.random()}?` },
  });
  expect(res.status()).toBe(201);
  return (await res.json()) as { id: string };
}

test('inspector resize, real Project and assignment persist; disclosures and Runs use recorded data', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  const headers = { 'X-Workspace-ID': f.workspace_id };
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  const research = await (
    await page.request.post('/api/researches', { headers, data: { name: 'Recorded Project' } })
  ).json();
  const me = await (await page.request.get('/api/me')).json();
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto(`/#/studies/${f.card_id}`);
  const inspector = page.getByRole('complementary', { name: 'Study details' });
  await expect(inspector.getByText('Stage', { exact: true })).toBeVisible();
  const resize = page.getByRole('separator', { name: 'Resize study inspector' });
  await resize.focus();
  await page.keyboard.press('End');
  await expect(inspector).toHaveCSS('width', '480px');
  await inspector.getByRole('combobox', { name: 'Project', exact: true }).selectOption(research.id);
  // A started study keeps its assignee: it is shown, not offered.
  await expect(inspector.getByRole('combobox', { name: 'Assignee', exact: true })).toHaveCount(0);
  await expect(inspector.locator('.property-row', { hasText: 'Assignee' })).toBeVisible();
  const backlog = await backlogStudy(page.request, headers);
  await page.goto(`/#/studies/${backlog.id}`);
  await inspector
    .getByRole('combobox', { name: 'Assignee', exact: true })
    .selectOption(`member:${me.id}`);
  await expect
    .poll(
      async () =>
        (await (await page.request.get(`/api/cards/${backlog.id}`, { headers })).json())
          .assignee_id,
    )
    .toBe(me.id);
  await page.reload();
  await expect(inspector.getByRole('combobox', { name: 'Assignee', exact: true })).toHaveValue(
    `member:${me.id}`,
  );
  await page.goto(`/#/studies/${f.card_id}`);
  await expect(inspector).toHaveCSS('width', '480px');
  await expect(inspector.getByRole('combobox', { name: 'Project', exact: true })).toHaveValue(
    research.id,
  );
  await expect(
    inspector.getByRole('button', { name: 'Execution log', exact: true }),
  ).toHaveAttribute('aria-expanded', 'true');
  await inspector.getByRole('button', { name: 'Execution log', exact: true }).click();
  await expect(inspector.locator('.execution-body')).toHaveCount(0);
  await inspector.getByRole('button', { name: 'Execution log', exact: true }).click();
  await page.evaluate(() => localStorage.setItem('lykeion.study-inspector-width', '364'));
  await page.reload();
  await expect(inspector).toHaveCSS('width', '364px');
  await inspector.getByRole('button', { name: 'Open timeline', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Runs', exact: true });
  await expect(dialog.getByText('Latest-attempt time', { exact: true })).toBeVisible();
  await expect(dialog.getByText('Attempt 2', { exact: false }).first()).toBeVisible();
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-study-inspector-refinements/task-2-artifacts/timeline.png',
  });
  await dialog.getByRole('button', { name: 'Close Runs' }).click();
  await inspector.getByRole('button', { name: 'Details', exact: true }).click();
  await expect(inspector.getByRole('button', { name: 'Change', exact: true })).toHaveCount(0);
  await inspector.getByRole('button', { name: 'Details', exact: true }).click();
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-study-inspector-refinements/task-2-artifacts/inspector.png',
  });
  await page.goto('/#/projects/' + research.id);
  await expect(page.getByRole('heading', { name: 'Recorded Project', exact: true })).toBeVisible();
  await expect(page.locator('.research-studies a')).toHaveCount(1);
});

test('both separators persist pointer and keyboard sizes without collapsing after a drag', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  await page.goto(`/#/studies/${f.card_id}`);
  const rail = page.getByRole('separator', { name: 'Resize left sidebar' });
  await expect(rail).toBeVisible();
  await rail.focus();
  await page.keyboard.press('Home');
  await expect(page.locator('.sidebar')).toHaveCSS('width', '200px');
  const box = (await rail.boundingBox())!;
  await page.mouse.move(box.x + 2, box.y + 60);
  await page.mouse.down();
  await page.mouse.move(box.x + 62, box.y + 60);
  await page.mouse.up();
  await expect(page.locator('.sidebar')).toHaveCSS('width', '260px');
  const inspector = page.getByRole('separator', { name: 'Resize study inspector' });
  await inspector.focus();
  await page.keyboard.press('Home');
  await expect(page.locator('.study-meta')).toHaveCSS('width', '240px');
  const right = (await inspector.boundingBox())!;
  await page.mouse.move(right.x, right.y + 60);
  await page.mouse.down();
  await page.mouse.move(right.x + 70, right.y + 60);
  await page.mouse.up();
  await expect(page.locator('.study-meta')).toHaveCSS('width', '310px');
  await page.goto('/#/projects');
  await page.goto(`/#/studies/${f.card_id}`);
  await page.reload();
  await expect(page.locator('.sidebar')).toHaveCSS('width', '260px');
  await expect(page.locator('.study-meta')).toHaveCSS('width', '310px');
});

test('Execution log and Details independently disclose; Runs keeps recorded costs and attempt timing honest', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  await page.goto(`/#/studies/${f.card_id}`);
  const meta = page.locator('.study-meta');
  await expect(meta.getByRole('button', { name: 'Execution log', exact: true })).toBeVisible();
  await meta.getByRole('button', { name: 'Execution log', exact: true }).click();
  await expect(meta.locator('.execution-body')).toHaveCount(0);
  await expect(meta.locator('.details-body')).toBeVisible();
  await meta.getByRole('button', { name: 'Details', exact: true }).click();
  await expect(meta.locator('.details-body')).toHaveCount(0);
  await meta.getByRole('button', { name: 'Open Runs', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Runs', exact: true });
  await expect(dialog.getByText('Latest-attempt time', { exact: true })).toBeVisible();
  await expect(dialog.locator('.timeline-row')).toHaveCount(1);
  const runs = await (
    await page.request.get(`/api/cards/${f.card_id}/runs`, {
      headers: { 'X-Workspace-ID': f.workspace_id },
    })
  ).json();
  await expect(dialog.locator('.run-metrics')).toContainText(`$${runs[0].cost_usd.toFixed(2)}`);
  await expect(dialog.locator('.timeline-row')).toContainText('Attempt 2');
  await expect(dialog).toContainText('Full attempt timing is unavailable.');
});

test('Project creation/edit and member/agent responsibility use canonical writes and old/current Project events', async ({
  page,
}) => {
  const f = await labFixture(page.request),
    headers = { 'X-Workspace-ID': f.workspace_id };
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  await page.goto('/#/projects');
  await page.getByRole('button', { name: 'New Project', exact: true }).click();
  const create = page.getByRole('dialog', { name: 'New Project', exact: true });
  await create.getByRole('textbox', { name: 'Project name' }).fill('Question grouping');
  await create.getByRole('textbox', { name: 'Project description' }).fill('Recorded scope');
  await create.getByRole('button', { name: 'Create Project', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Question grouping', exact: true })).toBeVisible();
  const first = page.url().split('/').pop()!;
  await page.getByRole('button', { name: 'Edit Project', exact: true }).click();
  await page.getByRole('textbox', { name: 'Project description' }).fill('Revised scope');
  await page.getByRole('button', { name: 'Save Project', exact: true }).click();
  await expect(page.locator('.description')).toHaveText('Revised scope');
  const second = await (
    await page.request.post('/api/researches', { headers, data: { name: 'Second research' } })
  ).json();
  await page.goto('/#/agents');
  await page.getByRole('button', { name: 'New agent', exact: true }).click();
  await page.getByRole('button', { name: /Start blank/ }).click();
  const agentDialog = page.getByRole('dialog', { name: 'New agent', exact: true });
  await agentDialog.getByRole('textbox', { name: 'Agent name' }).fill('Evidence curator');
  await agentDialog
    .getByRole('textbox', { name: 'Agent instructions' })
    .fill('Keep recorded sources traceable.');
  await agentDialog.getByRole('button', { name: 'Create agent', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Evidence curator', exact: true })).toBeVisible();
  const agentId = page.url().split('/').pop()!;
  await page.getByRole('tab', { name: 'Settings' }).click();
  await page.getByRole('textbox', { name: 'Agent description' }).fill('Curates recorded evidence');
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(page.locator('.agent-description')).toHaveText('Curates recorded evidence');
  const before = await (await page.request.get(`/api/cards/${f.card_id}`, { headers })).json();
  const runsBefore = await (
    await page.request.get(`/api/cards/${f.card_id}/runs`, { headers })
  ).json();
  await page.goto(`/#/studies/${f.card_id}`);
  const meta = page.locator('.study-meta');
  for (const [old, current] of [
    [null, first],
    [first, second.id],
    [second.id, null],
  ]) {
    await meta.getByRole('combobox', { name: 'Project', exact: true }).selectOption(current ?? '');
    await expect
      .poll(
        async () =>
          (await (await page.request.get(`/api/cards/${f.card_id}`, { headers })).json())
            .research_id,
      )
      .toBe(current);
    const events = await (
      await page.request.get(`/api/cards/${f.card_id}/events`, { headers })
    ).json();
    const event = events
      .filter(
        (e: {
          type: string;
          payload: {
            fields: string[];
            previous_research_id: string | null;
            research_id: string | null;
          };
        }) => e.type === 'card.properties_updated' && e.payload.fields.includes('research_id'),
      )
      .at(-1);
    expect(event.payload.previous_research_id).toBe(old);
    expect(event.payload.research_id).toBe(current);
    expect(
      (await (await page.request.get('/api/researches/' + first, { headers })).json()).studies,
    ).toHaveLength(current === first ? 1 : 0);
    expect(
      (await (await page.request.get('/api/researches/' + second.id, { headers })).json()).studies,
    ).toHaveLength(current === second.id ? 1 : 0);
  }
  const after = await (await page.request.get(`/api/cards/${f.card_id}`, { headers })).json();
  for (const key of ['owner_id', 'board_column', 'state', 'round', 'spent_usd', 'ending'])
    expect(after[key]).toEqual(before[key]);
  expect(
    await (await page.request.get(`/api/cards/${f.card_id}/runs`, { headers })).json(),
  ).toEqual(runsBefore);
  // The owner's agent assigned in Backlog starts the study, which then
  // shows its assignee instead of offering it.
  const backlog = await backlogStudy(page.request, headers);
  await page.goto(`/#/studies/${backlog.id}`);
  await meta
    .getByRole('combobox', { name: 'Assignee', exact: true })
    .selectOption('agent:' + agentId);
  await expect
    .poll(async () => {
      const c = await (await page.request.get(`/api/cards/${backlog.id}`, { headers })).json();
      return [c.assignee_id, c.board_column];
    })
    .toEqual([agentId, 'grounding']);
  await page.reload();
  await expect(meta.getByRole('combobox', { name: 'Assignee', exact: true })).toHaveCount(0);
  await expect(meta.locator('.property-row', { hasText: 'Assignee' })).toContainText(
    'Evidence curator',
  );
  await page.goto('/#/agents/' + agentId);
  page.once('dialog', (d) => d.accept());
  await page.getByRole('button', { name: 'Agent actions' }).click();
  await page.getByRole('menuitem', { name: 'Archive agent' }).click();
  await expect(page.getByText('Archived', { exact: true })).toBeVisible();
  const privateAgent = await (
    await page.request.post('/api/agents', {
      headers,
      data: { name: 'Owner private agent', harness: 'codex', visibility: 'private' },
    })
  ).json();
  const another = await backlogStudy(page.request, headers);
  await page.goto(`/#/studies/${another.id}`);
  await expect(meta.getByRole('combobox', { name: 'Assignee', exact: true })).not.toContainText(
    'Evidence curator',
  );
  await meta
    .getByRole('combobox', { name: 'Assignee', exact: true })
    .selectOption('agent:' + privateAgent.id);
  await expect
    .poll(
      async () =>
        (await (await page.request.get(`/api/cards/${another.id}`, { headers })).json())
          .assignee_id,
    )
    .toBe(privateAgent.id);
});

test('held actual assignment refusal cannot cross study navigation', async ({ page }) => {
  const f = await labFixture(page.request),
    headers = { 'X-Workspace-ID': f.workspace_id };
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  const a = await (
    await page.request.post('/api/agents', {
      headers,
      data: { name: 'Refused agent', harness: 'codex' },
    })
  ).json();
  const r = await (
    await page.request.post('/api/researches', {
      headers,
      data: { name: 'Navigation destination' },
    })
  ).json();
  const study = await backlogStudy(page.request, headers);
  await page.goto(`/#/studies/${study.id}`);
  await expect(page.getByRole('combobox', { name: 'Assignee', exact: true })).toContainText(a.name);
  let release!: () => void, held!: () => void;
  const gate = new Promise<void>((r) => (release = r)),
    reached = new Promise<void>((r) => (held = r));
  await page.route(`**/api/cards/${study.id}/properties`, async (route) => {
    await page.request.post(`/api/agents/${a.id}/archive`, { headers });
    const response = await route.fetch();
    expect(response.status()).toBe(400);
    held();
    await gate;
    await route.fulfill({ response });
  });
  await page.getByRole('combobox', { name: 'Assignee', exact: true }).selectOption('agent:' + a.id);
  await reached;
  await page.goto('/#/projects/' + r.id);
  await expect(page.getByRole('heading', { name: r.name, exact: true })).toBeVisible();
  release();
  await page.waitForTimeout(150);
  await expect(page.getByRole('alert')).toHaveCount(0);
  await page.goto(`/#/studies/${study.id}`);
  await expect(page.getByRole('combobox', { name: 'Assignee', exact: true })).toHaveValue('');
  expect(
    (await (await page.request.get(`/api/cards/${study.id}`, { headers })).json()).assignee_id,
  ).toBeNull();
});

for (const kind of ['researches', 'agents'] as const) {
  test(`delayed ${kind} create refetch cannot navigate after leaving its visit`, async ({
    page,
  }) => {
    const f = await labFixture(page.request),
      headers = { 'X-Workspace-ID': f.workspace_id };
    await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
    const other =
      kind === 'agents'
        ? await (
            await page.request.post('/api/workspaces', {
              data: { name: 'Refetch destination', slug: 'refetch-' + Date.now() },
            })
          ).json()
        : null;
    const path = kind === 'researches' ? '/projects' : '/agents';
    const collection = page.waitForResponse(
      (r) => r.request().method() === 'GET' && r.url().endsWith('/api/' + kind),
    );
    await page.goto('/#' + path);
    await collection;
    let release!: () => void, reached!: () => void;
    const held = new Promise<void>((r) => (reached = r)),
      gate = new Promise<void>((r) => (release = r));
    await page.route('**/api/' + kind, async (route) => {
      if (route.request().method() !== 'GET') {
        await route.continue();
        return;
      }
      const response = await route.fetch();
      reached();
      await gate;
      await route.fulfill({ response });
    });
    const label = kind === 'researches' ? 'Project' : 'agent';
    await page.getByRole('button', { name: 'New ' + label, exact: true }).click();
    if (kind === 'agents') await page.getByRole('button', { name: /Start blank/ }).click();
    const dialog = page.getByRole('dialog', { name: 'New ' + label, exact: true });
    const name = 'Held ' + kind + ' creation';
    await dialog
      .getByRole('textbox', { name: kind === 'researches' ? 'Project name' : 'Agent name' })
      .fill(name);
    await dialog.getByRole('button', { name: 'Create ' + label, exact: true }).click();
    await held;
    const canonical = await (await page.request.get('/api/' + kind, { headers })).json();
    expect(canonical.some((item: { name: string }) => item.name === name)).toBe(true);
    if (kind === 'agents') {
      await page.locator('.workspace-switch').click();
      await page.getByRole('menuitem', { name: 'Refetch destination', exact: false }).click();
      await expect(page.locator('.workspace-switch')).toContainText('Refetch destination');
      expect(other.id).not.toBe(f.workspace_id);
    } else await page.goto('/#/studies/' + f.card_id);
    release();
    await page.waitForTimeout(250);
    await expect(page).toHaveURL(
      kind === 'agents' ? /#\/studies$/ : new RegExp('#/studies/' + f.card_id + '$'),
    );
  });
}

test('open Project detail updates embedded linked studies after another client edits and deletes', async ({
  page,
}) => {
  const ws = await (
      await page.request.post('/api/workspaces', {
        data: { name: 'Linked refresh lab', slug: 'linked-refresh-' + Date.now() },
      })
    ).json(),
    headers = { 'X-Workspace-ID': ws.id };
  expect(
    (
      await page.request.patch(`/api/workspaces/${ws.id}/settings`, {
        data: { settings: { wip_limit: 0 } },
      })
    ).status(),
  ).toBe(204);
  const study = await (
    await page.request.post('/api/cards', {
      headers,
      data: { title: 'Before linked edit', question: 'Linked cache test ' + Date.now() + '?' },
    })
  ).json();
  const research = await (
    await page.request.post('/api/researches', { headers, data: { name: 'Live linked studies' } })
  ).json();
  expect(
    (
      await page.request.patch(`/api/cards/${study.id}/properties`, {
        headers,
        data: { research_id: research.id },
      })
    ).status(),
  ).toBe(200);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), ws.id);
  await page.goto('/#/projects/' + research.id);
  await expect(page.locator('.research-studies a')).toContainText('Before linked edit');
  expect(
    (
      await page.request.patch(`/api/cards/${study.id}`, {
        headers,
        data: { title: 'After linked edit' },
      })
    ).status(),
  ).toBe(200);
  await expect(page.locator('.research-studies a')).toContainText('After linked edit');
  expect((await page.request.delete(`/api/cards/${study.id}`, { headers })).status()).toBe(204);
  await expect(page.locator('.research-studies a')).toHaveCount(0);
  await expect(page.locator('.research-studies')).toContainText('No studies linked yet.');
});
