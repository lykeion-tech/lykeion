import { expect, test } from './test';
import { LAB, labFixture, labMember, labPack, labTeam, newStudy } from './lab';

const artifacts = '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-13-artifacts';

test('To-do cards can move into and out of a project without starting', async ({ page }) => {
  const { workspace_id: workspace } = await labFixture(page.request);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), workspace);
  const headers = { 'X-Workspace-ID': workspace };
  const team = await labTeam(page.request, workspace);
  const project = await page.request.post('/api/researches', {
    headers,
    data: { name: 'To-do destination' },
  });
  expect(project.status()).toBe(201);
  const { id: projectId } = await project.json();
  const id = crypto.randomUUID();
  const proposal = await page.request.put('/api/study-proposals/' + id, {
    headers,
    data: {
      title: 'Project assignment check',
      description: 'Compare the results.',
      priority: 'high',
      stage: 'grounding',
      revision: 0,
      assignee: { type: 'team', pack_id: team },
      project_id: null,
    },
  });
  expect(proposal.status()).toBe(200);
  await page.goto('/#/studies');
  const actions = page.getByRole('button', { name: 'Actions for Project assignment check' });
  const picker = page.getByRole('dialog', { name: 'Add to project' });
  await actions.click();
  await page.getByRole('menuitem', { name: 'Add to project' }).click();
  await picker.getByRole('button', { name: 'To-do destination' }).click();
  await expect(picker).toBeHidden();
  await page.goto('/#/projects/' + projectId);
  await expect(actions).toBeVisible();
  await actions.click();
  await page.getByRole('menuitem', { name: 'Add to project' }).click();
  await expect(picker.getByRole('button', { name: 'To-do destination' })).toHaveAttribute('aria-pressed', 'true');
  await picker.getByRole('button', { name: 'Remove from project' }).click();
  await expect(actions).toHaveCount(0);
  const response = await page.request.get('/api/study-proposals', { headers });
  expect((await response.json()).items.find((row: { id: string }) => row.id === id)).toMatchObject({
    revision: 3,
    project_id: null,
    priority: 'high',
    description: 'Compare the results.',
  });
});

test('board columns omit add buttons and sidebar New opens the study composer', async ({
  page,
}) => {
  await page.goto('/#/studies');
  for (const name of [
    'To-do',
    'Grounding',
    'Hypotheses',
    'Experimentation',
    'Validation',
    'Done',
    'Blocked',
  ]) {
    const column = page.getByRole('region', { name, exact: true });
    await expect(column).toBeVisible();
    await expect(column.getByRole('button', { name: 'New study', exact: true })).toHaveCount(0);
  }
  await page.getByRole('button', { name: 'New', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'New study' })).toBeVisible();
});

test('the board shows seven columns and a new study arriving live in its column', async ({
  page,
}) => {
  await page.goto('/#/studies');
  const headers = page.locator('.board-column > header strong');
  await expect(headers).toHaveText([
    'To-do',
    'Grounding',
    'Hypotheses',
    'Experimentation',
    'Validation',
    'Done',
    'Blocked',
  ]);
  await expect(page.locator('.task-card')).toHaveCount(0);
  await page.screenshot({ path: `${artifacts}/empty-board.png`, fullPage: true });
  await page.getByRole('button', { name: 'Board', exact: true }).click();
  await expect(page.locator('.list-group')).toHaveText([
    'To-do0',
    'Grounding0',
    'Hypotheses0',
    'Experimentation0',
    'Validation0',
    'Done0',
    'Blocked0',
  ]);
  await expect(page.locator('.list-row')).toHaveCount(0);
  await page.screenshot({ path: `${artifacts}/empty-list.png`, fullPage: true });
  await page.getByRole('button', { name: 'List', exact: true }).click();
  const question = `Does islet amyloid track IL-1β in human donors? ${Date.now()}`;
  const study = await newStudy(page.request, question);
  // No reload: the study arrives over /ws and starts in Grounding.
  const grounding = page.getByRole('region', { name: 'Grounding' });
  const shown = grounding.locator('.task-card', { hasText: question.slice(0, 40) });
  await expect(shown).toBeVisible({ timeout: 30_000 });
  await expect(shown).toContainText(`BETA-${study.number}`);
  await expect(shown).toContainText(/Updated (just now|\d+m ago)/);
  await expect(page.getByRole('status', { name: /stud(?:y|ies) running/ })).toBeVisible();

  await shown.scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${artifacts}/populated-board.png`, fullPage: true });
  // The researcher's own study also shows on their personal board.
  await page.goto('/#/my-studies');
  await expect(shown).toBeVisible();
  await page.goto('/#/studies');
  await expect(shown).toBeVisible();

  await page.getByRole('button', { name: 'Board' }).click();
  await expect(page.locator('.list-group', { hasText: 'Grounding' })).toBeVisible();
  await expect(page.locator('.list-row', { hasText: question.slice(0, 40) })).toBeVisible();
  await page.screenshot({ path: `${artifacts}/populated-list.png`, fullPage: true });
  await page.getByRole('button', { name: 'List' }).click();
  await expect(page.locator('.board-scroll')).toBeVisible();
});

test('the filter narrows the board to a column', async ({ page }) => {
  const fixture = await labFixture(page.request);
  await page.addInitScript(
    (id) => localStorage.setItem('lykeion.workspace', id),
    fixture.workspace_id,
  );
  await page.goto('/#/studies');
  await expect(
    page.getByRole('region', { name: 'Blocked' }).locator(`[data-card="${fixture.card_id}"]`),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Filter' }).click();
  await page.getByRole('menuitem', { name: 'Column' }).click();
  await page.getByRole('menuitemcheckbox', { name: /Hypotheses/ }).click();
  await expect(page.locator('.filter-chip')).toHaveText(/Column: Hypotheses/);
  await expect(page.locator(`[data-card="${fixture.card_id}"]`)).toHaveCount(0);
  await page.keyboard.press('Escape');
  await page.keyboard.press('Escape');
  await page.screenshot({ path: `${artifacts}/filtered-board.png`, fullPage: true });
});

test('clearing a real blocked study flag moves it live and display preferences persist', async ({
  page,
}) => {
  const fixture = await labFixture(page.request);
  await page.addInitScript(
    (id) => localStorage.setItem('lykeion.workspace', id),
    fixture.workspace_id,
  );
  await page.goto('/#/studies');
  const blocked = page.getByRole('region', { name: 'Blocked' });
  const hypotheses = page.getByRole('region', { name: 'Hypotheses' });
  const card = page.locator(`[data-card="${fixture.card_id}"]`);
  await expect(blocked.locator(`[data-card="${fixture.card_id}"]`)).toBeVisible();
  await expect(card.locator('.chip-escalated')).toBeVisible();
  await expect(hypotheses.locator('.task-card')).toHaveCount(0);
  const document = await page.evaluate(() => performance.timeOrigin);
  expect((await page.request.post(`${LAB}/lab/hypotheses/${fixture.card_id}`)).status()).toBe(204);
  await expect(hypotheses.locator(`[data-card="${fixture.card_id}"]`)).toBeVisible();
  await expect(blocked.locator('.task-card')).toHaveCount(0);
  await expect(card.locator('.chip-escalated')).toHaveCount(0);
  // Working with no agent running shows no state chip.
  await expect(card.locator('.chip-working')).toHaveCount(0);
  expect(await page.evaluate(() => performance.timeOrigin)).toBe(document);
  await page.screenshot({ path: `${artifacts}/cleared-flag-board.png`, fullPage: true });
  await page.getByRole('button', { name: 'Display', exact: true }).click();
  const properties = page.locator('.display-popover').getByRole('switch');
  await expect(properties).toHaveText([
    'Question',
    'Owner',
    'Assignee',
    'Deadline',
    'Project',
    'Role',
    'Updated',
  ]);
  await expect(card).toContainText('Updated');
  await properties.filter({ hasText: 'Updated' }).click();
  await expect(card).not.toContainText('Updated');
  await expect(card).toContainText('Ana Ruiz');
  await properties.filter({ hasText: 'Owner' }).click();
  await expect(card).not.toContainText('Ana Ruiz');
  await page.screenshot({ path: `${artifacts}/display-properties.png`, fullPage: true });
  await page.reload();
  await expect(card).toBeVisible();
  await expect(card).not.toContainText('Updated');
  await page.getByRole('button', { name: 'Display', exact: true }).click();
  await expect(page.locator('.display-popover [role="switch"][aria-checked="false"]')).toHaveCount(
    2,
  );
  await page.keyboard.press('Escape');
  await expect(page.locator('.display-popover')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Display', exact: true })).toBeFocused();
});

test('Display and filter keyboard paths retain focus', async ({ page }) => {
  await page.goto('/#/studies');
  const property = (name: string) =>
    page.locator('.display-popover').getByRole('switch', { name, exact: true });
  await page.getByRole('button', { name: 'Display', exact: true }).focus();
  await page.keyboard.press('ArrowDown');
  await expect(property('Question')).toBeFocused();
  await page.keyboard.press('End');
  await expect(property('Updated')).toBeFocused();
  await page.keyboard.press('Home');
  await expect(property('Question')).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(property('Owner')).toBeFocused();
  await page.keyboard.press('ArrowUp');
  await expect(property('Question')).toBeFocused();
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Filter', exact: true }).focus();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowRight');
  await expect(page.getByRole('menuitemcheckbox', { name: 'To-do', exact: true })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('menuitem', { name: 'Column', exact: true })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('button', { name: 'Filter', exact: true })).toBeFocused();
});

test('long board columns keep their header and cards inside the column while scrolling', async ({
  page,
}) => {
  const fixture = await labFixture(page.request);
  const headers = { 'X-Workspace-ID': fixture.workspace_id };
  for (let index = 0; index < 10; index++) {
    const response = await page.request.post(`${LAB}/lab/idle-study`, {
      headers,
      data: {
        title: `Long board study ${index}: a question that wraps across several lines`,
        question: `Board scrolling regression ${fixture.workspace_id} ${index}`,
      },
    });
    expect(response.status()).toBe(201);
  }
  await page.setViewportSize({ width: 1000, height: 600 });
  await page.addInitScript(
    (id) => localStorage.setItem('lykeion.workspace', id),
    fixture.workspace_id,
  );
  await page.goto('/#/studies');
  const board = page.locator('.board-scroll');
  // Idle studies are finished, so they stack in Done.
  const column = page.getByRole('region', { name: 'Done', exact: true });
  await expect(column.locator('.task-card')).toHaveCount(10);
  const headerBefore = await column.locator('header').boundingBox();
  const last = column.locator('.task-card').last();
  await last.scrollIntoViewIfNeeded();
  await expect(last).toBeInViewport();
  const geometry = await column.evaluate((column) => {
    const board = column.closest('.board-scroll')!;
    const bounds = column.getBoundingClientRect();
    const header = column.querySelector('header')!.getBoundingClientRect();
    const last = column
      .querySelector('.study-card:last-of-type .task-card')!
      .getBoundingClientRect();
    return {
      boardTop: board.getBoundingClientRect().top,
      headerTop: header.top,
      columnBottom: bounds.bottom,
      lastBottom: last.bottom,
      boardClientHeight: board.clientHeight,
      boardScrollHeight: board.scrollHeight,
    };
  });
  expect(geometry.headerTop).toBeCloseTo(headerBefore!.y, 0);
  expect(geometry.headerTop).toBeGreaterThanOrEqual(geometry.boardTop);
  expect(geometry.lastBottom).toBeLessThanOrEqual(geometry.columnBottom);
  expect(geometry.boardScrollHeight).toBeLessThanOrEqual(geometry.boardClientHeight + 1);
  const blocked = page.getByRole('region', { name: 'Blocked', exact: true });
  await blocked.scrollIntoViewIfNeeded();
  await expect(blocked).toBeInViewport();
  expect(await board.evaluate((element) => element.scrollLeft)).toBeGreaterThan(0);
  await expect(blocked.locator('header')).toBeInViewport();
});

test('To-do shares proposals in the right boards and preserves edits across revision conflicts', async ({
  page,
  browser,
}) => {
  const { workspace_id: workspace } = await labFixture(page.request);
  const headers = { 'X-Workspace-ID': workspace };
  const team = await labPack(page.request, 'Replication team', workspace, { stage: 'grounding' });
  expect((await page.request.post('/api/teams/' + team.id + '/add', { headers })).status()).toBe(
    200,
  );
  const project = await page.request.post('/api/researches', {
    headers,
    data: { name: 'Replication' },
  });
  expect(project.status()).toBe(201);
  const { id: projectId } = await project.json();
  const otherProject = await page.request.post('/api/researches', {
    headers,
    data: { name: 'Archives' },
  });
  expect(otherProject.status()).toBe(201);
  const colleague = await labMember(browser, page.request, workspace);
  try {
    const colleagueTeam = await labPack(colleague.context.request, 'colleague-team', workspace);
    expect(
      (
        await colleague.context.request.post('/api/teams/' + colleagueTeam.id + '/add', { headers })
      ).status(),
    ).toBe(200);
    const content = {
      description: 'Colleague proposal',
      revision: 0,
      stage: 'grounding',
      assignee: { type: 'team', pack_id: colleagueTeam.id },
      project_id: (await otherProject.json()).id,
    };
    const shared = await colleague.context.request.put(
      '/api/study-proposals/' + crypto.randomUUID(),
      { headers, data: content },
    );
    expect(shared.status()).toBe(200);
    await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), workspace);
    await page.goto('/#/projects/' + projectId);
    const todo = page.getByRole('region', { name: 'To-do' });
    const dialog = page.getByRole('dialog', { name: 'To-do' });
    await expect(page.getByRole('heading', { name: 'No studies', exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Queue study' })).toHaveCount(0);
    await page.getByRole('button', { name: 'New', exact: true }).click();
    const composer = page.getByRole('dialog', { name: 'New study' });
    await composer.getByRole('textbox', { name: 'Study title' }).fill('Replication plan');
    const description = composer.getByRole('textbox', { name: 'Describe your study' });
    await description.fill('My planned replication');
    await composer.getByRole('button', { name: 'Stages', exact: true }).click();
    await composer.getByRole('menuitemcheckbox', { name: 'To-do', exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await expect(description).toHaveValue('My planned replication');
    const stages = composer.getByRole('menu', { name: 'Study stages' });
    await expect(
      composer.getByRole('button', { name: 'Stages: To-do', exact: true }),
    ).toBeVisible();
    await expect(stages.getByRole('menuitemcheckbox', { checked: true })).toHaveText(['To-do']);
    await page.keyboard.press('Escape');
    const save = composer.getByRole('button', { name: /^Create To-do/ });
    await expect(save).toBeDisabled();
    await composer.getByRole('button', { name: 'Assignees' }).click();
    await composer.getByRole('menuitem', { name: /Replication team/i }).click();
    await composer.getByRole('button', { name: 'Stages: To-do', exact: true }).click();
    await stages.getByRole('menuitemcheckbox', { name: 'Grounding' }).click();
    await expect(composer.getByRole('button', { name: /^Create study/ })).toBeEnabled();
    await stages.getByRole('menuitemcheckbox', { name: 'To-do', exact: true }).click();
    await page.keyboard.press('Escape');
    await composer.getByRole('button', { name: 'Study priority' }).click();
    await composer.getByRole('menuitemradio', { name: 'Urgent' }).click();
    await composer.getByRole('button', { name: 'No project' }).click();
    await composer.getByRole('menuitemradio', { name: 'Replication', exact: true }).click();
    await page.route(
      /\/api\/study-proposals\/[^/?]+$/,
      async (route) => {
        const committed = await route.fetch();
        expect(committed.status()).toBe(200);
        await route.abort('connectionreset');
      },
      { times: 1 },
    );
    await save.click();
    await expect(composer.getByRole('alert')).toBeVisible();
    await expect(description).toHaveValue('My planned replication');
    await save.click();
    await expect(composer).toHaveCount(0);
    await expect(todo.getByText('My planned replication')).toBeVisible();
    await expect(todo.getByText('Colleague proposal')).toHaveCount(0);
    await page.getByRole('button', { name: 'Board', exact: true }).click();
    await expect(todo.locator('.list-row')).toHaveCount(1);
    await expect(todo.locator('.list-row')).toContainText('My planned replication');
    await page.getByRole('button', { name: 'List', exact: true }).click();

    await page.goto('/#/studies');
    await expect(todo.getByText('Colleague proposal')).toBeVisible();
    await expect(todo.getByRole('button', { name: 'Actions for Colleague proposal' })).toHaveCount(
      0,
    );
    await todo.getByText('Colleague proposal').click();
    await expect(dialog.getByLabel('Description')).toBeDisabled();
    await expect(dialog.getByRole('button', { name: /^Stage:/ })).toBeDisabled();
    await dialog.getByRole('button', { name: 'Close' }).click();
    await page.goto('/#/my-studies');
    await page.getByRole('button', { name: 'Board', exact: true }).click();
    await expect(todo.locator('.list-row')).toHaveCount(1);
    await expect(todo.getByText('Colleague proposal')).toHaveCount(0);
    await page.getByRole('button', { name: 'List', exact: true }).click();
    await todo.getByText('My planned replication').click();
    await expect(dialog.getByRole('button', { name: 'Save changes' })).toHaveCount(0);
    await expect(dialog.getByRole('button', { name: 'Discard' })).toHaveCount(0);
    const stagePicker = dialog.getByRole('button', { name: /^Stage:/ });
    const assigneePicker = dialog.getByRole('button', { name: /^Assignee:/ });
    const projectPicker = dialog.getByRole('button', { name: /^Project:/ });
    const fields = [dialog.getByLabel('Description'), stagePicker, assigneePicker, projectPicker];
    for (const width of [375, 1233]) {
      await page.setViewportSize({ width, height: 864 });
      const dialogBox = (await dialog.boundingBox())!;
      for (const field of fields) {
        const box = (await field.boundingBox())!;
        expect(box.x - dialogBox.x).toBeCloseTo(21, 0);
        expect(dialogBox.x + dialogBox.width - box.x - box.width).toBeCloseTo(21, 0);
      }
    }
    await expect(stagePicker).toHaveCSS('height', '37px');
    await stagePicker.click();
    const stageOptions = dialog.getByRole('listbox', { name: 'Stage options' });
    await expect(
      stageOptions.getByRole('option', { name: 'Grounding', selected: true }),
    ).toBeVisible();
    await stageOptions.getByRole('option', { name: 'Hypotheses' }).click();
    await expect(assigneePicker).toHaveAccessibleName('Assignee: Choose an agent or Team');
    await stagePicker.click();
    await stageOptions.getByRole('option', { name: 'Grounding' }).click();
    await assigneePicker.click();
    await dialog.getByRole('option', { name: /Replication team/i }).click();
    await projectPicker.click();
    await expect(
      dialog.getByRole('option', { name: 'Replication', selected: true, exact: true }),
    ).toBeVisible();
    await projectPicker.press('Escape');
    await expect(dialog).toBeVisible();
    await expect(projectPicker).toBeFocused();
    await dialog.getByLabel('Description').fill('Unsaved local description');
    const list = await page.request.get('/api/study-proposals', { headers });
    expect(list.ok()).toBe(true);
    const queued = (await list.json()).items.filter(
      (p: { project_id: string }) => p.project_id === projectId,
    );
    expect(queued).toHaveLength(1);
    const [saved] = queued;
    expect(saved).toMatchObject({
      title: 'Replication plan',
      priority: 'urgent',
      description: 'My planned replication',
      stage: 'grounding',
      assignee: { type: 'team', pack_id: team.id },
    });
    const path = '/api/study-proposals/' + saved.id;
    const remote = await page.request.put(path, {
      headers,
      data: {
        revision: saved.revision,
        title: 'Phone title',
        priority: 'high',
        description: 'Updated on another device',
        stage: saved.stage,
        project_id: saved.project_id,
        assignee: saved.assignee,
      },
    });
    expect(remote.status()).toBe(200);
    await expect(dialog.getByRole('button', { name: 'Start study' })).toBeDisabled();
    await expect(dialog.getByLabel('Description')).toHaveValue('Unsaved local description');
    await dialog.getByRole('button', { name: 'Load latest revision' }).click();
    await expect(dialog.getByLabel('Description')).toHaveValue('Updated on another device');
    await dialog.getByLabel('Description').fill('Final replication design');
    await dialog.getByRole('button', { name: 'Close' }).click();
    await todo.getByText('Final replication design').waitFor();
    const edited = (
      await (await page.request.get('/api/study-proposals', { headers })).json()
    ).items.find((p: { id: string }) => p.id === saved.id);
    expect(edited).toMatchObject({ title: 'Phone title', priority: 'high' });
    await todo.getByText('Final replication design').click();
    await dialog.getByLabel('Description').fill('Launch replication design');
    // The server commits the launch but the first response never reaches the owner.
    await page.route(
      '**' + path + '/start',
      async (route) => {
        const committed = await route.fetch();
        expect(committed.status()).toBe(200);
        await route.abort('connectionreset');
      },
      { times: 1 },
    );
    await dialog.getByRole('button', { name: 'Start study' }).click();
    await expect(dialog.getByRole('alert')).toBeVisible();
    await dialog.getByRole('button', { name: 'Start study' }).click();
    await expect(page).toHaveURL(/#\/my-studies\/[a-f0-9-]+/);
    await page.goto('/#/studies');
    await expect(todo.getByText('Launch replication design')).toHaveCount(0);
    await expect(
      page
        .getByRole('region', { name: 'Grounding' })
        .getByText('Launch replication design', { exact: true })
        .first(),
    ).toBeVisible();
    await colleague.page.goto('/#/my-studies');
    const colleagueTodo = colleague.page.getByRole('region', { name: 'To-do' });
    const actions = colleagueTodo.getByRole('button', { name: 'Actions for Colleague proposal' });
    await actions.click();
    const remove = colleague.page.getByRole('menuitem', { name: 'Delete', exact: true });
    await expect(remove).toHaveClass(/\bdanger-item\b/);
    colleague.page.once('dialog', (confirmation) => confirmation.dismiss());
    await remove.click();
    await expect(colleagueTodo.getByText('Colleague proposal')).toBeVisible();
    await actions.click();
    colleague.page.once('dialog', (confirmation) => confirmation.accept());
    await remove.click();
    await expect(todo.getByText('Colleague proposal')).toHaveCount(0);
  } finally {
    await colleague.context.close();
  }
});

test('a failed proposal read can retry independently of the study board', async ({ page }) => {
  await page.route('**/api/study-proposals?**', (route) =>
    route.fulfill({
      status: 503,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'Temporarily unavailable' }),
    }),
  );
  await page.goto('/#/studies');
  const todo = page.getByRole('region', { name: 'To-do' });
  await expect(todo.getByRole('alert')).toContainText('Couldn’t load proposals');
  await expect(todo.getByText('No to-dos', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('region', { name: 'Grounding' })).toBeVisible();
  await page.unroute('**/api/study-proposals?**');
  await todo.getByRole('button', { name: 'Retry' }).click();
  await expect(todo.getByText('No to-dos', { exact: true })).toBeVisible();
});
