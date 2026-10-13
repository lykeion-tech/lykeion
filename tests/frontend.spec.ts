import { expect, test, type Page } from '@playwright/test';

async function openBoard(page: Page) {
  await page.goto('/#/tasks');
  await expect(page.getByRole('heading', { name: 'Tasks', exact: true })).toBeVisible();
  await expect(page.locator('.board-empty')).toBeVisible();
}
async function createDraft(page: Page, title: string) {
  await page.locator('.quick-actions').getByRole('button', { name: 'New', exact: true }).click();
  await page.getByRole('button', { name: 'Switch to Manual' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByRole('textbox', { name: 'Task title', exact: true }).fill(title);
  await dialog
    .getByRole('textbox', { name: 'Description', exact: true })
    .fill('Disposable browser test draft.');
  await dialog.getByRole('button', { name: 'Create Task' }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.locator('.task-card').filter({ hasText: title })).toBeVisible();
}

test('a first installation has no invented research records; Escape dismisses composer', async ({
  page,
}) => {
  await openBoard(page);
  await expect(page.locator('.task-card')).toHaveCount(0);
  await page.locator('.quick-actions').getByRole('button', { name: 'New', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(
    page.locator('.quick-actions').getByRole('button', { name: 'New', exact: true }),
  ).toBeFocused();
});

test('manual drafts persist, comments persist, and local stage advancement is rejected', async ({
  page,
}) => {
  await openBoard(page);
  await createDraft(page, 'Reproduce archived computational result');
  await page.reload();
  await page
    .locator('.task-card')
    .filter({ hasText: 'Reproduce archived computational result' })
    .click();
  await expect(page.getByLabel('Scientific stage')).toHaveValue('Grounding');
  await page.getByLabel('Scientific stage').selectOption('Hypotheses');
  await expect(
    page.getByRole('status').filter({ hasText: 'Local drafts cannot advance scientific stages' }),
  ).toBeVisible();
  await expect(page.getByLabel('Scientific stage')).toHaveValue('Grounding');
  await page
    .getByRole('textbox', { name: 'Comment', exact: true })
    .fill('Preserve the original analysis inputs.');
  await page.getByRole('button', { name: 'Send', exact: true }).click();
  await expect(
    page.locator('.transcript-entry').filter({ hasText: 'Preserve the original analysis inputs.' }),
  ).toBeVisible();
  await page.reload();
  await expect(page.getByLabel('Scientific stage')).toHaveValue('Grounding');
  await expect(
    page.locator('.transcript-entry').filter({ hasText: 'Preserve the original analysis inputs.' }),
  ).toBeVisible();
});

test('workspace tabs maintain independent back and forward histories', async ({ page }) => {
  await openBoard(page);
  await page.getByRole('link', { name: 'Researches', exact: true }).click();
  await page.getByRole('button', { name: 'New tab', exact: true }).click();
  await page.getByRole('link', { name: 'Agents', exact: true }).click();
  await expect(page.getByRole('tab')).toHaveCount(2);
  await page.getByRole('tab', { name: 'Researches', exact: true }).click();
  await expect(page).toHaveURL(/#\/projects$/);
  await page.getByRole('button', { name: 'Go back', exact: true }).click();
  await expect(page).toHaveURL(/#\/tasks$/);
  await page.getByRole('button', { name: 'Go forward', exact: true }).click();
  await expect(page).toHaveURL(/#\/projects$/);
  await page.getByRole('tab', { name: 'Agents', exact: true }).click();
  await expect(page).toHaveURL(/#\/agents$/);
  await page.getByRole('button', { name: 'Go back', exact: true }).click();
  await expect(page).toHaveURL(/#\/tasks$/);
  await page.getByRole('button', { name: 'Go forward', exact: true }).click();
  await expect(page).toHaveURL(/#\/agents$/);
  await page.reload();
  await expect(page.getByRole('tab', { name: 'Agents', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await page.getByRole('button', { name: 'Go back', exact: true }).click();
  await expect(page).toHaveURL(/#\/tasks$/);
});

test('invalid API credentials are rejected without replacing the local workspace', async ({
  page,
}) => {
  await openBoard(page);
  await createDraft(page, 'Keep this local draft');
  await page.getByRole('link', { name: 'Settings', exact: true }).click();
  await page.getByRole('button', { name: 'API connection', exact: true }).click();
  await page.getByPlaceholder('http://localhost:8080').fill('https://user:secret@example.invalid');
  await page.getByRole('button', { name: 'Save connection', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('without embedded credentials');
  await page.getByRole('link', { name: 'Tasks', exact: true }).click();
  await expect(
    page.locator('.task-card').filter({ hasText: 'Keep this local draft' }),
  ).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem('lykeion.api-url'))).toBeNull();
});

async function chooseLayout(page: Page, current: string, next: string) {
  await page.locator('.task-toolbar').getByRole('button', { name: current, exact: true }).click();
  await page.getByRole('menuitemradio', { name: next, exact: true }).click();
}

test('table columns and inline changes persist while scientific status stays gated', async ({
  page,
}) => {
  await openBoard(page);
  await createDraft(page, 'Editable table draft');
  await chooseLayout(page, 'Board', 'Table');
  await expect(page.getByRole('table')).toBeVisible();
  await page.getByRole('button', { name: 'Columns', exact: true }).click();
  await page.getByRole('menuitemcheckbox', { name: 'Project', exact: true }).click();
  await page.keyboard.press('Escape');
  await expect(
    page.locator('th').filter({ has: page.getByRole('button', { name: 'Project', exact: true }) }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Rename task', exact: true }).click();
  await page.getByRole('textbox', { name: 'Edit Task', exact: true }).fill('Revised table draft');
  await page.getByRole('textbox', { name: 'Edit Task', exact: true }).press('Enter');
  await expect(
    page.getByRole('button', { name: 'Revised table draft', exact: true }),
  ).toBeVisible();
  await page.getByLabel('Priority for LYK-1', { exact: true }).selectOption('high');
  await expect(page.getByLabel('Priority for LYK-1', { exact: true })).toHaveValue('high');
  await page.getByLabel('Status for LYK-1', { exact: true }).selectOption('Hypotheses');
  await expect(
    page.getByRole('status').filter({ hasText: 'Local drafts cannot advance scientific stages' }),
  ).toBeVisible();
  await expect(page.getByLabel('Status for LYK-1', { exact: true })).toHaveValue('Grounding');
  await page.reload();
  await expect(page.getByRole('table')).toBeVisible();
  await expect(
    page.locator('th').filter({ has: page.getByRole('button', { name: 'Project', exact: true }) }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Revised table draft', exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel('Priority for LYK-1', { exact: true })).toHaveValue('high');
  await expect(page.getByLabel('Status for LYK-1', { exact: true })).toHaveValue('Grounding');
});

test('swimlane grouping, collapse and layout selection work for a real local draft', async ({
  page,
}) => {
  await openBoard(page);
  await createDraft(page, 'Draft in an unassigned swimlane');
  await chooseLayout(page, 'Board', 'Swimlane');
  const lane = page.locator('.lane-title');
  await expect(lane).toContainText('Unassigned');
  await lane.click();
  await expect(lane).toHaveAttribute('aria-expanded', 'false');
  await expect(page.locator('.swimlane-cells')).toHaveCount(0);
  await lane.click();
  await expect(page.locator('.swimlane-cells .task-card')).toBeVisible();
  await page.getByRole('button', { name: 'Display', exact: true }).click();
  await page.getByLabel('Grouping', { exact: true }).selectOption('Project');
  await page.keyboard.press('Escape');
  await expect(lane).toContainText('No project');
  await page.reload();
  await expect(page.locator('.swimlane-scroll')).toBeVisible();
  await expect(page.locator('.lane-title')).toContainText('No project');
  await chooseLayout(page, 'Swimlane', 'List');
  await expect(page.locator('.task-list')).toBeVisible();
  await page.reload();
  await expect(page.locator('.task-list')).toBeVisible();
});

test('CSV export escapes spreadsheet formulas without changing stored task text', async ({
  page,
}) => {
  await openBoard(page);
  await createDraft(page, '=1+1');
  await chooseLayout(page, 'Board', 'Table');
  await page.getByRole('button', { name: 'Export', exact: true }).click();
  const downloaded = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Export CSV', exact: true }).click();
  const download = await downloaded;
  expect(download.suggestedFilename()).toBe('lykeion-tasks.csv');
  const stream = await download.createReadStream();
  if (!stream) throw new Error('The CSV download could not be read.');
  const chunks: Buffer[] = [];
  for await (const chunk of stream) chunks.push(Buffer.from(chunk));
  expect(Buffer.concat(chunks).toString('utf8')).toContain('"\'=1+1"');
  await expect(page.getByRole('button', { name: '=1+1', exact: true })).toBeVisible();
  const storedTitle = await page.evaluate(
    () => JSON.parse(localStorage.getItem('lykeion.workspace.v1')!).inquiries[0].title,
  );
  expect(storedTitle).toBe('=1+1');
});

test('task auxiliary forms save drafts and child links without claiming execution', async ({
  page,
}) => {
  await openBoard(page);
  await createDraft(page, 'Parent for auxiliary forms');
  await page.locator('.task-card').filter({ hasText: 'Parent for auxiliary forms' }).click();
  await page.getByRole('button', { name: 'New wakeup', exact: true }).click();
  const wakeup = page.getByRole('dialog', { name: 'New wakeup', exact: true });
  await wakeup.getByRole('button', { name: 'Choose a condition', exact: true }).click();
  await page.getByRole('menuitem', { name: /Someone replies/ }).click();
  await wakeup
    .getByPlaceholder('For example: check the result and continue if it looks right')
    .fill('Review a response when the backend is available.');
  await wakeup.getByRole('button', { name: 'Save draft', exact: true }).click();
  await expect(wakeup).toHaveCount(0);
  const saved = await page.evaluate(
    () => JSON.parse(localStorage.getItem('lykeion.workspace.v1')!).inquiries[0],
  );
  expect(saved.wakeupDrafts[0].status).toBe('draft');
  expect(saved.stage).toBe('Grounding');
  await page.getByRole('button', { name: 'Open timeline', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Runs', exact: true })).toContainText(
    'No execution records available',
  );
  await page.getByRole('button', { name: 'Close runs', exact: true }).click();
  await page.getByRole('button', { name: 'Add sub-tasks', exact: true }).click();
  const child = page.getByRole('dialog');
  await child
    .getByRole('textbox', { name: 'Task title', exact: true })
    .fill('Child of the auxiliary parent');
  await child.getByRole('button', { name: 'Create Task' }).click();
  await expect(child).toHaveCount(0);
  const records = await page.evaluate(
    () => JSON.parse(localStorage.getItem('lykeion.workspace.v1')!).inquiries,
  );
  expect(
    records.find((item: { title: string }) => item.title === 'Child of the auxiliary parent')
      .parentId,
  ).toBe(saved.id);
});

test('card context edits, pins and new tabs preserve the task and gate boundaries', async ({
  page,
}) => {
  await openBoard(page);
  await createDraft(page, 'Context actions draft');
  const card = page.locator('.task-card').filter({ hasText: 'Context actions draft' });
  await card.click({ button: 'right' });
  const context = page.getByRole('menu', { name: 'Task actions', exact: true });
  await context.getByRole('menuitem', { name: 'Priority', exact: true }).click();
  await page.getByRole('menuitemradio', { name: 'High', exact: true }).click();
  await expect(card).toContainText('high');
  await card.click({ button: 'right' });
  await context.getByRole('menuitem', { name: 'Pin to sidebar', exact: true }).click();
  await expect(
    page
      .locator('.pinned-task-group')
      .getByRole('link', { name: 'Context actions draft', exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page
      .locator('.pinned-task-group')
      .getByRole('link', { name: 'Context actions draft', exact: true }),
  ).toBeVisible();
  await card.click({ button: 'right' });
  await context.getByRole('menuitem', { name: 'Delete task', exact: true }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Nothing was deleted' })).toBeVisible();
  await expect(card).toBeVisible();
  await card.click({ button: 'right' });
  await context.getByRole('menuitem', { name: 'Open in new tab', exact: true }).click();
  await expect(page.getByRole('tab')).toHaveCount(2);
  await expect(page.getByLabel('Scientific stage')).toHaveValue('Grounding');
});

test('working-agent filter stays distinct from all assigned agents and can be cleared', async ({
  page,
}) => {
  await openBoard(page);
  await createDraft(page, 'Unassigned draft is not running');
  await page.getByRole('checkbox', { name: '0 agents working', exact: true }).click();
  await expect(page.getByText('Viewing tasks with agents working', { exact: true })).toBeVisible();
  await expect(page.getByText('No tasks match these filters', { exact: true })).toBeVisible();
  await expect(page.locator('.task-card')).toHaveCount(0);
  await page.getByRole('button', { name: 'Clear filters', exact: true }).click();
  await expect(
    page.locator('.task-card').filter({ hasText: 'Unassigned draft is not running' }),
  ).toBeVisible();
});
