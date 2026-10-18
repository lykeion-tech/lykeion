import { expect, test, type Page } from '@playwright/test';
import { labFixture, labTeam } from './lab';

async function inFixture(page: Page) {
  const f = await labFixture(page.request);
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  const headers = { 'X-Workspace-ID': f.workspace_id };
  const agent = await page.request.post('/api/agents', {
    headers,
    data: { name: 'Watcher', harness: 'claude' },
  });
  expect(agent.status()).toBe(201);
  return headers;
}

const shots = 'test-results/teams-scoring';
const assign = async (dialog: ReturnType<Page['getByRole']>, page: Page, name: string) => {
  await dialog.getByRole('button', { name: 'Unassigned' }).click();
  await page.getByRole('menuitem', { name }).click();
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
  await dialog.getByRole('combobox', { name: 'Schedule days' }).selectOption('week');
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
  await expect
    .poll(
      async () =>
        ((await (await page.request.get(`/api/autoresearch/${id}`, { headers })).json()) as Detail)
          .status,
    )
    .toBe('paused');
  await page.getByRole('button', { name: 'Resume' }).click();
  await expect(page.getByRole('button', { name: 'Pause' })).toBeVisible();

  await page.getByRole('link', { name: '← Autoresearch' }).click();
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
  await expect(dialog.getByRole('button', { name: /Stages: Grounding → Reporting/ })).toBeVisible();
  await expect(create).toBeEnabled();
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
  await set.getByRole('combobox', { name: 'Grounding Team' }).selectOption({ index: 1 });
  await set
    .getByRole('combobox', { name: 'Agent for the other stages' })
    .selectOption({ label: 'Watcher' });
  await set.getByRole('button', { name: 'Use this set' }).click();
  await expect(
    dialog.getByRole('button', { name: 'Stages: Grounding → Reporting' }),
  ).toBeDisabled();
  await page.screenshot({ path: `${shots}/autoresearch-editor.png`, fullPage: true });
  await dialog.getByRole('button', { name: /Run only/ }).click();
  await dialog.getByRole('button', { name: 'Create autoresearch' }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByText('Assigned to a Team set · Grounding → Reporting')).toBeVisible();
  const id = page.url().split('/').pop()!;
  const got = (await (await page.request.get(`/api/autoresearch/${id}`, { headers })).json()) as {
    assignee_type: string;
    scope_from: string;
    scope_to: string;
  };
  expect(got).toMatchObject({
    assignee_type: 'team',
    scope_from: 'grounding',
    scope_to: 'reporting',
  });
  await page.screenshot({ path: `${shots}/autoresearch-detail.png`, fullPage: true });
});
