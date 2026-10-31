import { selectPicker } from './select-picker';
import { expect, test, type Page } from '@playwright/test';
import { LAB, labFixture } from './lab';

// Opens one of the study's right-pane surfaces from the chooser.
async function openSurface(page: Page, surface: string) {
  const pane = page.getByRole('complementary', { name: 'Right pane', exact: true });
  const back = pane.getByRole('button', { name: 'Back to surfaces', exact: true });
  if (await back.isVisible()) await back.click();
  await pane.getByRole('button', { name: surface, exact: true }).click();
  return pane;
}

test('inspector resize and real Project persist; disclosures and Runs use recorded data', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  const headers = { 'X-Workspace-ID': f.workspace_id };
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  const research = await (
    await page.request.post('/api/researches', { headers, data: { name: 'Recorded Project' } })
  ).json();
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto(`/#/studies/${f.card_id}`);
  const inspector = page.getByRole('complementary', { name: 'Right pane', exact: true });
  await openSurface(page, 'Properties');
  await expect(inspector.getByText('Stage', { exact: true })).toBeVisible();
  const resize = page.getByRole('separator', { name: 'Resize right pane' });
  await resize.focus();
  await page.keyboard.press('End');
  await expect(inspector).toHaveCSS('width', '640px');
  await selectPicker(
    inspector.getByRole('combobox', { name: 'Project', exact: true }),
    research.id,
  );
  // A study's assignee is shown, not offered.
  await expect(inspector.getByRole('combobox', { name: 'Assignee', exact: true })).toHaveCount(0);
  await expect(inspector.locator('.property-row', { hasText: 'Assignee' })).toBeVisible();
  await page.goto(`/#/studies/${f.card_id}`);
  await expect(inspector).toHaveCSS('width', '640px');
  await openSurface(page, 'Properties');
  await expect(inspector.getByRole('combobox', { name: 'Project', exact: true })).toHaveAttribute(
    'data-value',
    research.id,
  );
  await openSurface(page, 'Execution log & timeline');
  await expect(
    inspector.getByRole('button', { name: 'Execution log', exact: true }),
  ).toHaveAttribute('aria-expanded', 'true');
  await inspector.getByRole('button', { name: 'Execution log', exact: true }).click();
  await expect(inspector.locator('.execution-body')).toHaveCount(0);
  await inspector.getByRole('button', { name: 'Execution log', exact: true }).click();
  await page.evaluate(() => localStorage.setItem('lykeion.study-inspector-width', '364'));
  await page.reload();
  await expect(inspector).toHaveCSS('width', '364px');
  await openSurface(page, 'Execution log & timeline');
  await inspector.getByRole('button', { name: 'Open timeline', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Runs', exact: true });
  await expect(dialog.getByText('Latest-attempt time', { exact: true })).toBeVisible();
  await expect(dialog.getByText('Attempt 2', { exact: false }).first()).toBeVisible();
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-study-inspector-refinements/task-2-artifacts/timeline.png',
  });
  await dialog.getByRole('button', { name: 'Close Runs' }).click();
  await openSurface(page, 'Details');
  await inspector.getByRole('button', { name: 'Details', exact: true }).click();
  await expect(inspector.getByRole('button', { name: 'Change', exact: true })).toHaveCount(0);
  await inspector.getByRole('button', { name: 'Details', exact: true }).click();
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-study-inspector-refinements/task-2-artifacts/inspector.png',
  });
  await page.goto('/#/projects/' + research.id);
  await expect(page.getByRole('heading', { name: 'Recorded Project', exact: true })).toBeVisible();
  await expect(page.locator(`.project-board [data-card="${f.card_id}"]`)).toHaveCount(1);
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
  const pane = page.locator('aside[aria-label="Right pane"]');
  const inspector = page.getByRole('separator', { name: 'Resize right pane' });
  await inspector.focus();
  await page.keyboard.press('Home');
  await expect(pane).toHaveCSS('width', '240px');
  const right = (await inspector.boundingBox())!;
  await page.mouse.move(right.x + right.width / 2, right.y + 60);
  await page.mouse.down();
  await page.mouse.move(right.x + right.width / 2 - 70, right.y + 60);
  await page.mouse.up();
  await expect(pane).toHaveCSS('width', '310px');
  await page.goto('/#/projects');
  await page.goto(`/#/studies/${f.card_id}`);
  await page.reload();
  await expect(page.locator('.sidebar')).toHaveCSS('width', '260px');
  await expect(pane).toHaveCSS('width', '310px');
});

test('Execution log and Details independently disclose; Runs keeps recorded costs and attempt timing honest', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  await page.goto(`/#/studies/${f.card_id}`);
  const meta = await openSurface(page, 'Execution log & timeline');
  await expect(meta.getByRole('button', { name: 'Execution log', exact: true })).toBeVisible();
  await meta.getByRole('button', { name: 'Execution log', exact: true }).click();
  await expect(meta.locator('.execution-body')).toHaveCount(0);
  await openSurface(page, 'Details');
  await expect(meta.locator('.details-body')).toBeVisible();
  await meta.getByRole('button', { name: 'Details', exact: true }).click();
  await expect(meta.locator('.details-body')).toHaveCount(0);
  await openSurface(page, 'Execution log & timeline');
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

test('Project creation/edit and agent profiles use canonical writes and old/current Project events', async ({
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
  await page
    .getByRole('dialog', { name: 'Edit Project', exact: true })
    .getByRole('textbox', { name: 'Project description' })
    .fill('Revised scope');
  await page.getByRole('button', { name: 'Save Project', exact: true }).click();
  await expect(
    page
      .getByRole('complementary', { name: 'Project properties' })
      .getByRole('textbox', { name: 'Project description' }),
  ).toHaveValue('Revised scope');
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
  await expect(page).toHaveURL(/#\/agents\/[0-9a-f-]+$/);
  await page.getByRole('tab', { name: 'Settings' }).click();
  await page.getByRole('textbox', { name: 'Agent description' }).fill('Curates recorded evidence');
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(page.locator('.agent-description')).toHaveText('Curates recorded evidence');
  const before = await (await page.request.get(`/api/cards/${f.card_id}`, { headers })).json();
  const runsBefore = await (
    await page.request.get(`/api/cards/${f.card_id}/runs`, { headers })
  ).json();
  await page.goto(`/#/studies/${f.card_id}`);
  const meta = await openSurface(page, 'Properties');
  for (const [old, current] of [
    [null, first],
    [first, second.id],
    [second.id, null],
  ]) {
    await selectPicker(meta.getByRole('combobox', { name: 'Project', exact: true }), current ?? '');
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
              data: { name: 'Refetch destination ' + Date.now(), slug: 'refetch-' + Date.now() },
            })
          ).json()
        : null;
    // The created row's realtime event would cancel the held refetch, so the
    // create would navigate early, before the test leaves its visit.
    await page.routeWebSocket(/\/ws$/, (socket) => {
      const server = socket.connectToServer();
      server.onMessage((raw) => {
        if (JSON.parse(String(raw)).type !== 'event') socket.send(raw);
      });
    });
    const path = kind === 'researches' ? '/projects' : '/agents';
    const collection = page.waitForResponse(
      (r) => r.request().method() === 'GET' && r.url().endsWith('/api/' + kind),
    );
    await page.goto('/#' + path);
    await collection;
    let release!: () => void, reached!: () => void;
    const held = new Promise<void>((r) => (reached = r)),
      gate = new Promise<void>((r) => (release = r));
    // Only the create's refetch is held: an earlier read, such as the socket's
    // on-open refresh, would not list the new row.
    let created = false;
    await page.route('**/api/' + kind, async (route) => {
      if (route.request().method() !== 'GET') {
        const response = await route.fetch();
        created = true;
        await route.fulfill({ response });
        return;
      }
      if (!created) {
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
      await page.getByRole('menuitem', { name: other.name }).click();
      await expect(page.locator('.workspace-switch')).toContainText(other.name);
      expect(other.id).not.toBe(f.workspace_id);
    } else await page.goto('/#/studies/' + f.card_id);
    release();
    await page.waitForTimeout(250);
    await expect(page).toHaveURL(
      kind === 'agents' ? /#\/studies$/ : new RegExp('#/studies/' + f.card_id + '$'),
    );
  });
}

test('open Project detail updates embedded linked studies after another client links and deletes', async ({
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
  const idle = async (title: string) =>
    (
      await page.request.post(`${LAB}/lab/idle-study`, {
        headers,
        data: { title, question: `${title} ${Date.now()}?` },
      })
    ).json() as Promise<{ id: string }>;
  const study = await idle('Before linked edit');
  const later = await idle('Linked by another client');
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
  await expect(page.locator(`.project-board [data-card="${study.id}"]`)).toContainText(
    'Before linked edit',
  );
  expect(
    (
      await page.request.patch(`/api/cards/${later.id}/properties`, {
        headers,
        data: { research_id: research.id },
      })
    ).status(),
  ).toBe(200);
  await expect(page.locator(`.project-board [data-card="${later.id}"]`)).toContainText(
    'Linked by another client',
  );
  expect((await page.request.delete(`/api/cards/${study.id}`, { headers })).status()).toBe(204);
  await expect(page.locator(`.project-board [data-card="${study.id}"]`)).toHaveCount(0);
  expect((await page.request.delete(`/api/cards/${later.id}`, { headers })).status()).toBe(204);
  await expect(page.locator('.project-empty')).toContainText('No studies linked');
});
