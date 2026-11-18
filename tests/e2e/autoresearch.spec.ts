import { selectPicker } from './select-picker';
import { expect, test, type Page } from '@playwright/test';
import {
  chooseCompact,
  labFixture,
  labTeam,
  labAutoresearchRun,
  labReportAutoresearch,
  fullAutoresearchReport,
} from './lab';

async function inFixture(page: Page) {
  const f = await labFixture(page.request);
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  const headers = { 'X-Workspace-ID': f.workspace_id };
  const agent = await page.request.post('/api/agents', {
    headers,
    data: { name: 'Watcher', stage: 'experimentation', harness: 'claude' },
  });
  expect(agent.status()).toBe(201);
  return headers;
}

const shots = 'test-results/teams-scoring';
const assign = async (dialog: ReturnType<Page['getByRole']>, page: Page, name: string) => {
  await dialog.getByRole('button', { name: 'Unassigned' }).click();
  await page.getByRole('menuitem', { name }).click();
  // The range starts on the agent's own stage.
  await expect(dialog.getByRole('button', { name: 'Stages: Experimentation' })).toBeVisible();
};

type Detail = {
  id: string;
  title: string;
  description: string;
  execution_mode: string;
  status: string;
  triggers: { kind: string; cron: string; tz: string }[];
};

test('a template opens the editor, and the schedule reaches the server as cron', async ({
  page,
}) => {
  const headers = await inFixture(page);
  await page.goto('/#/autoresearch');
  await expect(page.getByRole('link', { name: 'Autoresearch' })).toHaveClass(/selected/);
  await expect(page.getByRole('link', { name: 'Autoresearch' }).locator('svg')).toHaveClass(
    /lucide-zap/,
  );
  await expect(page.getByText('No autoresearch yet')).toBeVisible();
  await page.getByRole('button', { name: /Literature watch/ }).click();
  const dialog = page.getByRole('dialog', { name: 'New autoresearch' });
  await expect(dialog.getByRole('textbox', { name: 'Autoresearch name' })).toHaveValue(
    'Literature watch',
  );
  await dialog.getByRole('textbox', { name: 'Runbook' }).fill('Find new papers on ALDH1A3.');
  await assign(dialog, page, 'Watcher');
  await dialog.getByRole('button', { name: /Run only/ }).click();
  await dialog.getByLabel('Scheduled time').fill('07:30');
  await selectPicker(dialog.getByRole('combobox', { name: 'Schedule days' }), 'week');
  await dialog.getByRole('button', { name: 'Mon', exact: true }).click(); // Monday starts chosen
  await dialog.getByRole('button', { name: 'Wed', exact: true }).click();
  await dialog.getByRole('button', { name: 'Fri', exact: true }).click();
  await dialog.getByRole('button', { name: 'Create autoresearch' }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page).toHaveURL(/#\/autoresearch\/[0-9a-f-]{36}$/);
  await expect(page.getByRole('heading', { name: 'Literature watch' })).toBeVisible();

  const id = page.url().split('/').pop()!;
  const got = (await (
    await page.request.get(`/api/autoresearch/${id}`, { headers })
  ).json()) as Detail;
  expect(got.description).toBe('Find new papers on ALDH1A3.');
  expect(got.execution_mode).toBe('run_only');
  expect(got.triggers).toHaveLength(1);
  expect(got.triggers[0]).toMatchObject({ kind: 'schedule', cron: '30 7 * * 3,5' });
  await expect(page.getByText('30 7 * * 3,5')).toBeVisible();

  await page.getByRole('button', { name: 'Pause' }).click();
  await expect(page.getByRole('button', { name: 'Resume' })).toBeVisible();
  await expect(page.getByText(/next run/)).toHaveCount(0);
  await expect
    .poll(
      async () =>
        ((await (await page.request.get(`/api/autoresearch/${id}`, { headers })).json()) as Detail)
          .status,
    )
    .toBe('paused');
  await page.getByRole('button', { name: 'Resume' }).click();
  await expect(page.getByRole('button', { name: 'Pause' })).toBeVisible();
  await expect(page.getByText(/next run/)).toBeVisible();

  await page.getByRole('link', { name: 'Autoresearch', exact: true }).click();
  await expect(page.getByRole('link', { name: /Literature watch/ })).toBeVisible();
});

test('a webhook trigger shows its URL once, and Delete removes the autoresearch', async ({
  page,
}) => {
  const headers = await inFixture(page);
  await page.goto('/#/autoresearch');
  await page.getByRole('button', { name: 'Start from scratch' }).click();
  const dialog = page.getByRole('dialog', { name: 'New autoresearch' });
  await dialog.getByRole('textbox', { name: 'Autoresearch name' }).fill('On new results');
  await dialog.getByRole('textbox', { name: 'Runbook' }).fill('Open a study for each result.');
  await assign(dialog, page, 'Watcher');
  await dialog.getByRole('button', { name: 'Webhook' }).click();
  await dialog.getByRole('button', { name: 'Create autoresearch' }).click();
  await expect(dialog).toHaveCount(0);
  const url = page.getByRole('textbox', { name: 'Webhook URL' });
  await expect(url).toHaveValue(/\/api\/webhooks\/autoresearch\/.+/);
  const id = page.url().split('/').pop()!;
  const got = (await (
    await page.request.get(`/api/autoresearch/${id}`, { headers })
  ).json()) as Detail;
  expect(got.execution_mode).toBe('create_card');
  expect(got.triggers.map((t) => t.kind)).toEqual(['webhook']);

  await page.reload();
  await expect(page.getByRole('heading', { name: 'On new results' })).toBeVisible();
  await expect(page.getByRole('textbox', { name: 'Webhook URL' })).toHaveCount(0);

  page.once('dialog', (d) => d.accept());
  await page.getByRole('button', { name: 'Delete' }).click();
  await expect(page).toHaveURL(/#\/autoresearch$/);
  expect((await page.request.get(`/api/autoresearch/${id}`, { headers })).status()).toBe(404);
});

test('an Autoresearch needs an assignee', async ({ page }) => {
  await inFixture(page);
  await page.goto('/#/autoresearch');
  await page.getByRole('button', { name: 'Start from scratch' }).click();
  const dialog = page.getByRole('dialog', { name: 'New autoresearch' });
  await dialog.getByRole('textbox', { name: 'Autoresearch name' }).fill('Needs someone');
  await dialog.getByRole('textbox', { name: 'Runbook' }).fill('Watch.');
  const create = dialog.getByRole('button', { name: 'Create autoresearch' });
  await expect(create).toBeDisabled();
  await dialog.getByRole('button', { name: 'Unassigned' }).click();
  const menu = page.getByRole('menu');
  await expect(menu.getByRole('menuitem', { name: 'Unassigned' })).toHaveCount(0);
  await expect(menu.getByText('Members')).toHaveCount(0);
  await page.screenshot({ path: `${shots}/autoresearch-picker.png`, fullPage: true });
  await menu.getByRole('menuitem', { name: 'Watcher' }).click();
  await expect(dialog.getByRole('button', { name: 'Stages: Experimentation' })).toBeVisible();
  await expect(create).toBeEnabled();
});

test('an invalid schedule keeps the editor open without creating an Autoresearch', async ({
  page,
}) => {
  const headers = await inFixture(page);
  await page.goto('/#/autoresearch');
  await page.getByRole('button', { name: 'Start from scratch' }).click();
  const dialog = page.getByRole('dialog', { name: 'New autoresearch' });
  await dialog.getByRole('textbox', { name: 'Autoresearch name' }).fill('Invalid schedule');
  await dialog.getByRole('textbox', { name: 'Runbook' }).fill('Watch.');
  await assign(dialog, page, 'Watcher');
  await dialog.getByRole('button', { name: 'Custom cron expression' }).click();
  await dialog.getByLabel('Cron expression').fill('not a cron');
  await dialog.getByRole('button', { name: 'Create autoresearch' }).click();
  await expect(dialog.getByRole('alert')).toBeVisible();
  const list = await (await page.request.get('/api/autoresearch', { headers })).json();
  expect(list).toHaveLength(0);
  await dialog.getByLabel('Cron expression').fill('0 9 * * *');
  await dialog.getByRole('button', { name: 'Create autoresearch' }).click();
  await expect(dialog).toHaveCount(0);
});

test('a Team set Autoresearch shows its range', async ({ page }) => {
  const headers = await inFixture(page);
  await labTeam(page.request, headers['X-Workspace-ID']!);
  await page.goto('/#/autoresearch');
  await page.getByRole('button', { name: 'Start from scratch' }).click();
  const dialog = page.getByRole('dialog', { name: 'New autoresearch' });
  await dialog.getByRole('textbox', { name: 'Autoresearch name' }).fill('Set watch');
  await dialog.getByRole('textbox', { name: 'Runbook' }).fill('Run the set.');
  await dialog.getByRole('button', { name: 'Unassigned' }).click();
  await page.getByRole('menuitem', { name: 'New set…' }).click();
  const set = page.getByRole('dialog', { name: 'New Team set' });
  // The agent fills the one stage no Team covers, and it works only that stage.
  for (const stage of ['Grounding', 'Hypotheses', 'Validation'])
    await chooseCompact(page, `${stage} Team`, 1);
  await chooseCompact(page, 'Agent for the other stages', 'Watcher');
  await set.getByRole('button', { name: 'Use this set' }).click();
  await expect(
    dialog.getByRole('button', { name: 'Stages: Grounding → Validation' }),
  ).toBeDisabled();
  await page.screenshot({ path: `${shots}/autoresearch-editor.png`, fullPage: true });
  await dialog.getByRole('button', { name: /Run only/ }).click();
  await dialog.getByRole('button', { name: 'Create autoresearch' }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByText('Assigned to a Team set · Grounding → Validation')).toBeVisible();
  const id = page.url().split('/').pop()!;
  const got = (await (await page.request.get(`/api/autoresearch/${id}`, { headers })).json()) as {
    assignee_type: string;
    scope_from: string;
    scope_to: string;
  };
  expect(got).toMatchObject({
    assignee_type: 'team',
    scope_from: 'grounding',
    scope_to: 'validation',
  });
  await page.screenshot({ path: `${shots}/autoresearch-detail.png`, fullPage: true });
});

test('a single-Team Autoresearch sends no scope and shows the Team’s column', async ({ page }) => {
  const headers = await inFixture(page);
  await labTeam(page.request, headers['X-Workspace-ID']!);
  let sent: Record<string, unknown> = {};
  await page.route('**/api/autoresearch/with-trigger', (route) => {
    if (route.request().method() === 'POST') sent = route.request().postDataJSON();
    return route.continue();
  });
  await page.goto('/#/autoresearch');
  await page.getByRole('button', { name: 'Start from scratch' }).click();
  const dialog = page.getByRole('dialog', { name: 'New autoresearch' });
  await dialog.getByRole('textbox', { name: 'Autoresearch name' }).fill('One Team');
  await dialog.getByRole('textbox', { name: 'Runbook' }).fill('Ground it.');
  await dialog.getByRole('button', { name: 'Unassigned' }).click();
  await page.getByRole('menuitem', { name: /Built for Grounding/ }).click();
  await dialog.getByRole('button', { name: 'Create autoresearch' }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByText('Assigned to a Team · Grounding')).toBeVisible();
  expect(sent).not.toHaveProperty('scope');
  expect(sent).toHaveProperty('assignee.type', 'team');
});

test('run-only history opens the full report', async ({ page }) => {
  const f = await reportFixture(page, 'completed', fullAutoresearchReport);
  await page.goto(`/#/autoresearch/${f.autoresearch_id}`);
  await page.getByRole('button', { name: /Completed.*Open report/ }).click();
  const dialog = page.getByRole('dialog', { name: 'Autoresearch run' });
  await expect(dialog.getByRole('heading', { name: 'Report literature watch' })).toBeVisible();
  await expect(dialog.getByText('end-of-full-report', { exact: false })).toBeVisible();
  await expect(dialog.getByText('Full report · As submitted by the agent')).toBeVisible();
  await expect(dialog.getByText(/Planned/)).toBeVisible();
  await expect(dialog.locator('script, img')).toHaveCount(0);
  await expect(
    dialog.locator('a[href^="javascript:"], a[href^="file:"], a[href^="codex:"]'),
  ).toHaveCount(0);
  expect(await page.evaluate(() => 'reportExecuted' in window)).toBe(false);
  await expect(dialog.getByRole('tab')).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page).toHaveURL(new RegExp(f.autoresearch_id));
});

test('report arrival does not complete a running run', async ({ page }) => {
  const f = await reportFixture(page, 'running');
  await page.goto(`/#/autoresearch/${f.autoresearch_id}`);
  await page.getByRole('button', { name: /Running.*View run/ }).click();
  const dialog = page.getByRole('dialog', { name: 'Autoresearch run' });
  await expect(dialog.getByText('Waiting for a report', { exact: true })).toBeVisible();
  await labAutoresearchRun(page.request, f.workspace_id, f.autoresearch_id, {
    run_id: f.report_run_id,
    status: 'running',
    report: fullAutoresearchReport,
  });
  await expect(dialog.getByText('end-of-full-report', { exact: false })).toBeVisible();
  await expect(dialog.getByText('Report available · Run still in progress')).toBeVisible();
  await labAutoresearchRun(page.request, f.workspace_id, f.autoresearch_id, {
    run_id: f.report_run_id,
    status: 'running',
    report: fullAutoresearchReport + '\nrefresh-tail',
  });
  await expect(dialog.getByText('refresh-tail')).toBeVisible();
  await expect(dialog.getByText('Report available · Run still in progress')).toBeVisible();
  await page.screenshot({ path: 'test-results/ui2/task15-running.png', fullPage: true });
});

test('failed run can keep a report', async ({ page }) => {
  const f = await reportFixture(page, 'failed', fullAutoresearchReport, 'provider_network');
  await page.goto(`/#/autoresearch/${f.autoresearch_id}`);
  await page.getByRole('button', { name: /Failed.*Open report/ }).click();
  const dialog = page.getByRole('dialog', { name: 'Autoresearch run' });
  await expect(dialog.getByText('provider_network', { exact: true })).toBeVisible();
  await expect(dialog.getByText('end-of-full-report', { exact: false })).toBeVisible();
  await page.screenshot({ path: 'test-results/ui2/task15-failed-report.png', fullPage: true });
});

test('completed run without a report says so', async ({ page }) => {
  const f = await reportFixture(page, 'completed');
  await page.goto(`/#/autoresearch/${f.autoresearch_id}`);
  await page.getByRole('button', { name: /Completed.*View run/ }).click();
  const dialog = page.getByRole('dialog', { name: 'Autoresearch run' });
  await expect(dialog.getByText('No report was submitted', { exact: true })).toBeVisible();
  await expect(dialog.getByRole('tab')).toHaveCount(0);
});

test('create-card history opens the linked study', async ({ page }) => {
  const f = await labReportAutoresearch(page.request, 'create_card');
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  await labAutoresearchRun(page.request, f.workspace_id, f.autoresearch_id, {
    status: 'completed',
    card_id: f.card_id,
  });
  await page.goto(`/#/autoresearch/${f.autoresearch_id}`);
  await page.getByRole('link', { name: /Completed.*Open study/ }).click();
  await expect(page).toHaveURL(new RegExp(`#/studies/${f.card_id}$`));
  await expect(page.getByRole('region', { name: 'Study', exact: true })).toBeVisible();
  await expect(page.getByRole('dialog', { name: 'Autoresearch run' })).toHaveCount(0);
});

async function reportFixture(page: Page, status: string, report = '', reason = '') {
  const f = await labReportAutoresearch(page.request);
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  const runId = await labAutoresearchRun(page.request, f.workspace_id, f.autoresearch_id, {
    status,
    report,
    reason,
  });
  return { ...f, report_run_id: runId };
}

test('failed report reads retry and refresh failures retain the shown report', async ({ page }) => {
  const f = await reportFixture(page, 'running', fullAutoresearchReport);
  let refuse = true;
  await page.route(`**/api/autoresearch-runs/${f.report_run_id}`, (route) =>
    refuse ? route.abort('failed') : route.continue(),
  );
  await page.goto(`/#/autoresearch/${f.autoresearch_id}`);
  await page.getByRole('button', { name: /Running.*Open report/ }).click();
  const dialog = page.getByRole('dialog', { name: 'Autoresearch run' });
  await expect(dialog.getByRole('alert')).toBeVisible();
  refuse = false;
  await dialog.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(dialog.getByText('end-of-full-report', { exact: false })).toBeVisible();
  refuse = true;
  await labAutoresearchRun(page.request, f.workspace_id, f.autoresearch_id, {
    run_id: f.report_run_id,
    status: 'running',
    report: fullAutoresearchReport + '\nafter-retry',
  });
  await expect(dialog.getByRole('alert')).toBeVisible();
  await expect(dialog.getByText('end-of-full-report', { exact: false })).toBeVisible();
  await expect(dialog.getByText('after-retry')).toHaveCount(0);
  refuse = false;
  await dialog.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(dialog.getByText('after-retry')).toBeVisible();
});

test('report module-load failure preserves complete safe recorded report', async ({ page }) => {
  const f = await reportFixture(page, 'completed', fullAutoresearchReport);
  await page.route('**/RecordedMarkdown.vue*', (route) => route.abort('failed'));
  await page.goto(`/#/autoresearch/${f.autoresearch_id}`);
  await page.getByRole('button', { name: /Completed.*Open report/ }).click();
  const dialog = page.getByRole('dialog', { name: 'Autoresearch run' });
  await expect(dialog.getByRole('status')).toHaveText(
    'Markdown formatting is unavailable; showing recorded text.',
  );
  await expect(dialog.locator('.recorded-text-fallback')).toHaveText(fullAutoresearchReport);
  await expect(dialog.locator('script,img,iframe')).toHaveCount(0);
});
