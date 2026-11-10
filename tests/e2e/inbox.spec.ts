import { expect, test, type Page } from '@playwright/test';
import {
  labAutoresearchRun,
  labReportAutoresearch,
  fullAutoresearchReport,
  labFixture,
  labInbox,
  labWorkspace,
  newStudy,
} from './lab';

// Each escalation waits for the lab's one machine to work its study.
test.setTimeout(240_000);

test('an escalation reaches the Inbox and is read on opening', async ({ page }) => {
  const title = `Sleep loss and microglial pruning ${Date.now()}`;
  const { item } = await escalation(page, title);
  await page.goto('/#/decisions');
  const list = page.getByRole('navigation', { name: 'Decisions' });
  const link = list.getByRole('link', { name: new RegExp(title) }).first();
  await expect(link).toBeVisible();
  await expect(link).toContainText('column stopped');
  await expect(unread(page, item.id)).toHaveCount(1);

  await page.screenshot({ path: `${artifacts}/open-unread.png`, fullPage: true });
  await link.click();
  await expect(shownStudy(page, title)).toBeVisible();
  await expect(unread(page, item.id)).toHaveCount(0);
  await expect(page.locator('.decision-rail')).toBeVisible();
  await page.locator('.sidebar-brand:visible [aria-current=page]').click();
  await expect(page.getByRole('complementary', { name: 'Workspace navigation' })).toBeVisible();
  await expect(shownStudy(page, title)).toBeVisible();
  await page.locator('.sidebar-brand:visible [aria-current=page]').click();
  await expect(link).toBeVisible();

  await expect
    .poll(async () => (await inbox(page)).find((i) => i.id === item.id)?.read_at)
    .toBeTruthy();
  await page.screenshot({ path: `${artifacts}/selected-read.png`, fullPage: true });
});

const artifacts = '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-18-artifacts';
type Item = { id: string; card_id: string; read_at: string | null; resolved_at: string | null };
async function inbox(page: Page, archived = false) {
  const response = await page.request.get(
    `/api/decisions${archived ? '?view=history' : '?view=open'}`,
    {
      headers: { 'X-Workspace-ID': await labWorkspace(page.request) },
    },
  );
  expect(response.status()).toBe(200);
  return ((await response.json()) ?? []) as Item[];
}
async function escalation(page: Page, name: string) {
  const card = await newStudy(page.request, `${name} ${Date.now()}?`);
  let item!: Item;
  await expect
    .poll(
      async () => {
        item = (await inbox(page)).find((i) => i.card_id === card.id)!;
        return Boolean(item);
      },
      { timeout: 90_000 },
    )
    .toBe(true);
  return { card, item };
}
const row = (page: Page, id: string) =>
  page.getByRole('navigation', { name: 'Decisions' }).locator(`a[href="#/decisions/${id}"]`);
// The unread dot sits beside a row's link, inside the row's wrapper.
const unread = (page: Page, id: string) =>
  page
    .getByRole('navigation', { name: 'Decisions' })
    .locator('.decision-row-wrap')
    .filter({ has: page.locator(`a[href="#/decisions/${id}"]`) })
    .locator('.unread');
// A selected decision shows its study's title in the decision card, not an embedded study.
const shownStudy = (page: Page, title: string) =>
  page.getByRole('article').getByRole('heading', { name: title });

test('a failed read shows an inline error and keeps the actual unread item', async ({ page }) => {
  const { item } = await escalation(page, 'Transport read failure');
  await page.route(`**/api/decisions/${item.id}/read`, (route) => route.abort('failed'));
  await page.goto('/#/decisions');
  await row(page, item.id).click();
  await expect(page.getByRole('alert')).toContainText(/Failed to fetch/);
  await expect(unread(page, item.id)).toHaveCount(1);
  expect((await inbox(page)).find((i) => i.id === item.id)?.read_at).toBeNull();
  await page.screenshot({ path: `${artifacts}/read-error.png`, fullPage: true });
});

for (const destination of [
  'another item',
  'same item after a return',
  'another workspace',
] as const) {
  test(`a delayed read refusal cannot affect ${destination}`, async ({ page }) => {
    const a = await escalation(page, 'Old read visit');
    const b = await escalation(page, 'Current read visit');
    if (destination === 'another workspace') {
      const older = await labFixture(page.request);
      await labInbox(page.request, older.card_id, 'blocked', 'Earlier same-name workspace item');
    }
    const other = await labFixture(page.request);
    const otherName = `Inbox target ${other.workspace_id}`;
    if (destination === 'another workspace')
      expect(
        (
          await page.request.patch(`/api/workspaces/${other.workspace_id}`, {
            headers: { 'X-Workspace-ID': other.workspace_id },
            data: { name: otherName },
          })
        ).status(),
      ).toBe(200);
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    let accepted!: (status: number) => void;
    const refused = new Promise<number>((resolve) => {
      accepted = resolve;
    });
    let drain!: () => void;
    const drained = new Promise<void>((resolve) => {
      drain = resolve;
    });
    let first = true;
    await page.route(`**/api/decisions/${a.item.id}/read`, async (route) => {
      if (!first) return route.continue();
      first = false;
      try {
        const response = await route.fetch({
          headers: { ...route.request().headers(), 'X-Workspace-ID': other.workspace_id },
        });
        accepted(response.status());
        await gate;
        await route.fulfill({ response });
      } finally {
        drain();
      }
    });
    try {
      await page.goto(`/#/decisions/${a.item.id}`);
      expect(await refused).toBe(404);
      if (destination === 'another workspace') {
        // The inbox rail carries the Lab menu in its footer.
        await page.locator('.workspace-switch:visible').click();
        await page
          .getByRole('menu', { name: 'Workspace' })
          .getByRole('menuitem', { name: new RegExp(otherName) })
          .click();
        await expect(page).toHaveURL(/#\/studies$/);
        await expect
          .poll(() => page.evaluate(() => localStorage.getItem('lykeion.workspace')))
          .toBe(other.workspace_id);
        await page.goto('/#/decisions');
        await expect(page.getByText('No decisions yet')).toBeVisible();
      } else {
        await row(page, b.item.id).click();
        await expect(shownStudy(page, b.card.title)).toBeVisible();
        if (destination === 'same item after a return') {
          await row(page, a.item.id).click();
          await expect(unread(page, a.item.id)).toHaveCount(0);
          await expect(shownStudy(page, a.card.title)).toBeVisible();
        }
      }
      const browserResponse = page.waitForResponse(
        (r) =>
          r.request().method() === 'POST' && r.url().endsWith(`/api/decisions/${a.item.id}/read`),
      );
      release();
      await drained;
      await (await browserResponse).finished();
      await page.evaluate(() => new Promise(requestAnimationFrame));
      await expect(page.getByRole('alert')).toHaveCount(0);
      if (destination !== 'another workspace')
        await expect(
          shownStudy(page, destination === 'another item' ? b.card.title : a.card.title),
        ).toBeVisible();
    } finally {
      release();
      await drained;
    }
  });
}

test('each new Inbox kind reads and opens its study', async ({ page }) => {
  const name = `Kinds ${Date.now()}`;
  const card = await newStudy(page.request, `${name}?`, { title: name, assignee: null });
  const kinds: [string, string, string][] = [
    [
      'middle_score',
      `Study #${card.number}'s Grounding column scored in the middle; its weakest criterion was “Does it answer what the researcher asked?” (level 3 of 5).`,
      `${name} moved on with a middling score: Does it answer what the researcher asked?`,
    ],
    [
      'blocked',
      `Study #${card.number} scored low twice.`,
      `${name} scored low twice and is waiting for you`,
    ],
    [
      'no_tool',
      `An agent on study #${card.number}'s Grounding column found no skill or connector that fits what it needed: a mass-spec parser`,
      `${name}'s agent found no skill or connector for: a mass-spec parser`,
    ],
    [
      'scoring_unavailable',
      `Scoring is unavailable for study #${card.number}.`,
      `${name} couldn't be scored; Lykeion will retry`,
    ],
    ['done', `Study #${card.number} is done.`, `${name} is done`],
  ];
  for (const [kind, text] of kinds) await labInbox(page.request, card.id, kind, text);
  await page.goto('/#/decisions');
  const list = page.getByRole('navigation', { name: 'Decisions' });
  for (const [, , shown] of kinds)
    await expect(list.getByText(shown, { exact: true })).toBeVisible();
  await page.screenshot({ path: 'test-results/teams-scoring/inbox-kinds.png', fullPage: true });
  for (const i of [4, 1]) {
    await list.getByText(kinds[i]![2], { exact: true }).click();
    await expect(shownStudy(page, name)).toBeVisible();
    await expect(page).toHaveURL(/#\/decisions\/[0-9a-f-]{36}$/);
  }
});

test('Inbox report without card opens the run', async ({ page }) => {
  const f = await labReportAutoresearch(page.request);
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  const runId = await labAutoresearchRun(page.request, f.workspace_id, f.autoresearch_id, {
    status: 'running',
    report: fullAutoresearchReport,
    inbox: true,
  });
  const headers = { 'X-Workspace-ID': f.workspace_id };
  const items = await (await page.request.get('/api/decisions', { headers })).json();
  const item = items.find((i: { autoresearch_run_id: string }) => i.autoresearch_run_id === runId);
  expect(item.card_id).toBeNull();
  await page.goto('/#/decisions');
  await row(page, item.id).click();
  const dialog = page.getByRole('dialog', { name: 'Autoresearch run' });
  await expect(dialog.getByRole('heading', { name: 'Report literature watch' })).toBeVisible();
  await expect(dialog.getByText('Report available · Run still in progress')).toBeVisible();
  await expect(dialog.getByText('end-of-full-report', { exact: false })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Open study →' })).toHaveCount(0);
  await expect
    .poll(
      async () =>
        (await (await page.request.get('/api/decisions?view=history', { headers })).json()).find(
          (i: { id: string }) => i.id === item.id,
        )?.read_at,
    )
    .toBeTruthy();
  await page.screenshot({ path: 'test-results/ui2/task15-inbox.png', fullPage: true });
  await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page).toHaveURL(new RegExp(`#/decisions/${item.id}$`));
  await page.getByRole('button', { name: 'Open report', exact: true }).click();
  await expect(dialog).toBeVisible();
});

test('Inbox report context survives archive refresh and a closed run stays closed', async ({
  page,
}) => {
  const f = await labReportAutoresearch(page.request);
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  const runId = await labAutoresearchRun(page.request, f.workspace_id, f.autoresearch_id, {
    status: 'running',
    report: fullAutoresearchReport,
    inbox: true,
  });
  const headers = { 'X-Workspace-ID': f.workspace_id };
  const items = await (await page.request.get('/api/decisions', { headers })).json();
  const item = items.find((i: { autoresearch_run_id: string }) => i.autoresearch_run_id === runId);
  let holding = false;
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  let ready!: () => void;
  const initial = new Promise<void>((resolve) => {
    ready = resolve;
  });
  let drained!: () => void;
  const late = new Promise<void>((resolve) => {
    drained = resolve;
  });
  await page.route('**/api/decisions?view=history', async (route) => {
    const response = await route.fetch();
    if (holding) {
      await gate;
      await route.fulfill({ response });
      drained();
    } else {
      await route.fulfill({ response });
      ready();
    }
  });
  try {
    await page.goto('/#/decisions');
    await initial;
    holding = true;
    await row(page, item.id).click();
    await expect(page.getByRole('navigation', { name: 'Decisions' })).toContainText(
      'No decisions yet',
    );
    const dialog = page.getByRole('dialog', { name: 'Autoresearch run' });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText('end-of-full-report', { exact: false })).toBeVisible();
    await dialog.getByRole('button', { name: 'Close', exact: true }).click();
    release();
    await late;
    await expect(dialog).toHaveCount(0);
    await page.getByRole('button', { name: 'Open report', exact: true }).click();
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(row(page, item.id)).toBeVisible();
    await expect(unread(page, item.id)).toHaveCount(0);
    const receipt = (
      await (await page.request.get('/api/decisions?view=history', { headers })).json()
    ).find((i: { id: string }) => i.id === item.id);
    expect(receipt.read_at).toBeTruthy();
    expect(receipt.resolved_at).toBeTruthy();
  } finally {
    release();
  }
});

test('study descriptions render safe, readable Markdown in Studies and in Decisions', async ({
  page,
  context,
}) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  const question = [
    '## Study approach',
    '',
    'A **focused** description with [a source](https://example.com) and `inline code`.',
    '',
    '- First item',
    '  - Nested item',
    '- Second item',
    '',
    '1. Numbered item',
    '',
    `Long identifier: ${'a'.repeat(240)}`,
    '',
    '> A quoted observation.',
    '',
    '| Measure | Value |',
    '| --- | --- |',
    '| Mean | 3 |',
    '',
    '```python',
    'print(42)',
    '```',
    '',
    '<script>window.badMarkdown = true</script> [unsafe](javascript:alert(1))',
  ].join('\n');
  const study = await newStudy(page.request, question, { assignee: null, title: 'Markdown study' });
  await labInbox(page.request, study.id, 'blocked', 'Inspect Markdown');
  const item = (await inbox(page)).find((i) => i.card_id === study.id)!;
  expect(item).toBeTruthy();
  await page.goto(`/#/studies/${study.id}`);
  const description = page.getByRole('region', { name: 'Study description' });
  await expect(description.getByRole('heading', { name: 'Study approach' })).toBeVisible();
  await expect(description.locator('strong')).toHaveText('focused');
  await expect(description.locator('ul ul li')).toHaveText('Nested item');
  await expect(description.locator('ul').first()).toHaveCSS('list-style-type', 'disc');
  await expect(description.locator('ol').first()).toHaveCSS('list-style-type', 'decimal');
  await description.getByRole('button', { name: 'Copy code' }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('print(42)\n');
  await expect(description.locator('blockquote')).toContainText('A quoted observation.');
  await expect(description.getByRole('table')).toContainText('Mean');
  await expect(description.locator('pre code')).toContainText('print(42)');
  await expect(description.locator('a[href^="javascript:"],script,img')).toHaveCount(0);
  await expect(description).toHaveCSS('border-top-width', '0px');
  for (const width of [1512, 390]) {
    await page.setViewportSize({ width, height: 982 });
    await expect(description).toBeVisible();
    expect(await description.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  }
  await page.setViewportSize({ width: 1512, height: 982 });

  // A decision renders the study's question as the same safe Markdown.
  await page.goto(`/#/decisions/${item.id}`);
  const studyContext = page.getByRole('article').locator('.study-context');
  await expect(studyContext.getByRole('heading', { name: 'Study approach' })).toBeVisible();
  await expect(studyContext.locator('strong')).toHaveText('focused');
  await expect(studyContext.locator('ul ul li')).toHaveText('Nested item');
  await expect(studyContext.locator('blockquote')).toContainText('A quoted observation.');
  await expect(studyContext.getByRole('table')).toContainText('Mean');
  await expect(studyContext.locator('pre code')).toContainText('print(42)');
  await expect(page.getByRole('article').locator('script,img,a[href^="javascript:"]')).toHaveCount(
    0,
  );
  expect(await page.evaluate(() => 'badMarkdown' in window)).toBe(false);
  for (const width of [1512, 390]) {
    await page.setViewportSize({ width, height: 982 });
    await expect(studyContext).toBeVisible();
    expect(await studyContext.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  }
});
