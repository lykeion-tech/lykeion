import { expect, test, type Page } from '@playwright/test';
import {
  labAutoresearchRun,
  labReportAutoresearch,
  fullAutoresearchReport,
  labFixture,
  labHypotheses,
  labInbox,
  labRecords,
  labWorkspace,
  newStudy,
} from './lab';

test('an escalation reaches the Inbox and is read on opening', async ({ page }) => {
  const title = `Sleep loss and microglial pruning ${Date.now()}`;
  const { item } = await escalation(page, title);
  await page.goto('/#/inbox');
  const list = page.getByRole('navigation', { name: 'Inbox items' });
  const link = list.getByRole('link', { name: new RegExp(title) }).first();
  await expect(link).toBeVisible();
  await expect(link).toContainText('Escalated');
  await expect(link.locator('.unread')).toHaveCount(1);

  await page.screenshot({ path: `${artifacts}/open-unread.png`, fullPage: true });
  await link.click();
  await expect(
    page.getByRole('region', { name: 'Study' }).getByRole('heading', { name: title }),
  ).toBeVisible();
  await expect(link.locator('.unread')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Show inbox' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await page.getByRole('button', { name: 'Show navigation' }).click();
  await expect(page.getByRole('complementary', { name: 'Workspace navigation' })).toBeVisible();
  await expect(
    page.getByRole('region', { name: 'Study' }).getByRole('heading', { name: title }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Show inbox' }).click();
  await expect(link).toBeVisible();

  await expect
    .poll(async () => (await inbox(page)).find((i) => i.id === item.id)?.read_at)
    .toBeTruthy();
  await page.screenshot({ path: `${artifacts}/selected-read.png`, fullPage: true });
});

const artifacts = '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-18-artifacts';
type Item = { id: string; card_id: string; read_at: string | null; resolved_at: string | null };
async function inbox(page: Page, archived = false) {
  const response = await page.request.get(`/api/inbox${archived ? '?archived=1' : ''}`, {
    headers: { 'X-Workspace-ID': await labWorkspace(page.request) },
  });
  expect(response.status()).toBe(200);
  return ((await response.json()) ?? []) as Item[];
}
async function escalation(page: Page, name: string) {
  const card = await newStudy(page.request, `${name} ${Date.now()}?`);
  let item!: Item;
  await expect
    .poll(async () => {
      item = (await inbox(page)).find((i) => i.card_id === card.id)!;
      return Boolean(item);
    })
    .toBe(true);
  return { card, item };
}
const row = (page: Page, id: string) =>
  page.getByRole('navigation', { name: 'Inbox items' }).locator(`a[href="#/inbox/${id}"]`);

test('a failed read shows an inline error and keeps the actual unread item', async ({ page }) => {
  const { item } = await escalation(page, 'Transport read failure');
  await page.route(`**/api/inbox/${item.id}/read`, (route) => route.abort('failed'));
  await page.goto('/#/inbox');
  await row(page, item.id).click();
  await expect(page.getByRole('alert')).toContainText(/Failed to fetch/);
  await expect(row(page, item.id).locator('.unread')).toHaveCount(1);
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
      await labInbox(page.request, older.card_id, 'escalated', 'Earlier same-name workspace item');
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
    await page.route(`**/api/inbox/${a.item.id}/read`, async (route) => {
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
      await page.goto(`/#/inbox/${a.item.id}`);
      expect(await refused).toBe(404);
      if (destination === 'another workspace') {
        // The inbox shows its own rail; the workspace switcher lives in the navigation rail.
        await page.getByRole('button', { name: 'Show navigation', exact: true }).click();
        await page
          .getByRole('complementary', { name: 'Workspace navigation' })
          .getByRole('button', { name: /Beta-cell lab/ })
          .click();
        await page
          .getByRole('menu', { name: 'Workspace' })
          .getByRole('menuitem', { name: new RegExp(otherName) })
          .click();
        await expect(page).toHaveURL(/#\/studies$/);
        await expect
          .poll(() => page.evaluate(() => localStorage.getItem('lykeion.workspace')))
          .toBe(other.workspace_id);
        await page.goto('/#/inbox');
        await expect(page.getByText('Nothing needs you right now')).toBeVisible();
      } else {
        await row(page, b.item.id).click();
        await expect(
          page.getByRole('region', { name: 'Study' }).getByRole('heading', { name: b.card.title }),
        ).toBeVisible();
        await page.getByRole('textbox', { name: 'Comment' }).fill('Current visit draft');
        if (destination === 'same item after a return') {
          await row(page, a.item.id).click();
          await expect(row(page, a.item.id).locator('.unread')).toHaveCount(0);
          await page.getByRole('textbox', { name: 'Comment' }).fill('Return visit draft');
        }
      }
      const browserResponse = page.waitForResponse(
        (r) => r.request().method() === 'POST' && r.url().endsWith(`/api/inbox/${a.item.id}/read`),
      );
      release();
      await drained;
      await (await browserResponse).finished();
      await page.evaluate(() => new Promise(requestAnimationFrame));
      await expect(page.getByRole('alert')).toHaveCount(0);
      if (destination !== 'another workspace')
        await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveValue(
          destination === 'another item' ? 'Current visit draft' : 'Return visit draft',
        );
    } finally {
      release();
      await drained;
    }
  });
}

test('the embedded study fits long evidence and resets drafts on selection', async ({ page }) => {
  const a = await escalation(page, 'Embedded long evidence');
  const b = await escalation(page, 'Embedded next selection');
  await labHypotheses(page.request, a.card.id);
  await labRecords(page.request, a.card.id, 25);
  await page.goto(`/#/inbox/${a.item.id}`);
  const study = page.getByRole('region', { name: 'Study', exact: true });
  await expect(study.getByRole('heading', { name: a.card.title })).toBeVisible();
  const meta = study.locator('aside[aria-label="Right pane"]');
  expect((await meta.boundingBox())!.width).toBe(364);
  await page.getByRole('textbox', { name: 'Comment' }).fill('Embedded draft must reset');
  await page.screenshot({ path: `${artifacts}/embedded-long-content.png`, fullPage: true });
  const bounds = await study.evaluate((element) => ({
    width: element.clientWidth,
    scrollWidth: element.scrollWidth,
    body: document.documentElement.scrollWidth,
    viewport: innerWidth,
  }));
  expect(bounds.scrollWidth).toBeLessThanOrEqual(bounds.width);
  expect(bounds.body).toBeLessThanOrEqual(bounds.viewport);
  await page.getByRole('textbox', { name: 'Comment' }).fill('Unsaved previous study draft');
  await row(page, b.item.id).click();
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveValue('');
  await expect(page.getByRole('button', { name: 'Action' })).toHaveText('Comment');
});

test('Stop in the embedded study archives the original item and keeps the ended study selected', async ({
  page,
}) => {
  const { card, item } = await escalation(page, 'Embedded Stop resolution');
  await page.goto(`/#/inbox/${item.id}`);
  await expect(
    page.getByRole('region', { name: 'Study' }).getByRole('heading', { name: card.title }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Action' }).click();
  await page.getByRole('menuitem', { name: 'Stop' }).click();
  await page.getByRole('textbox', { name: 'Comment' }).fill('No usable primary data');
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Send' }).click();
  await expect.poll(async () => (await inbox(page, true)).some((i) => i.id === item.id)).toBe(true);
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveCount(0);
  await expect(page.getByRole('heading', { name: card.title })).toBeVisible();
  await page
    .getByRole('navigation', { name: 'Inbox items' })
    .getByRole('button', { name: 'Archived' })
    .click();
  await expect(row(page, item.id)).toBeVisible();
  await page.getByRole('button', { name: 'Back to Inbox' }).click();
  await expect(row(page, item.id)).toHaveCount(0);
});

test('embedded Properties keeps its own width and responds to the actual Inbox container', async ({
  page,
}) => {
  const { card, item } = await escalation(page, 'Embedded responsive Properties');
  await page.addInitScript(() => {
    if (!localStorage.getItem('lykeion.study-inspector-width'))
      localStorage.setItem('lykeion.study-inspector-width', '424');
  });
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto(`/#/inbox/${item.id}`);
  const study = page.getByRole('region', { name: 'Study', exact: true });
  const meta = study.locator('aside[aria-label="Right pane"]');
  // The pane floats over the study on narrow windows and when the Inbox leaves it under 640px.
  const floats = async () =>
    (await page.evaluate(() => innerWidth)) <= 980 || (await study.boundingBox())!.width < 640;
  const expectPaneMode = () =>
    expect
      .poll(async () => (await floats()) === ((await meta.getAttribute('role')) === 'dialog'))
      .toBe(true);
  const splitter = study.getByRole('separator', { name: 'Resize right pane' });
  await expect(study.getByRole('heading', { name: card.title })).toBeVisible();
  await page.setViewportSize({ width: 1100, height: 982 });
  await expectPaneMode();
  // With the rail open the study is still wide enough to dock the pane.
  expect(await floats()).toBe(false);
  const railToggle = page.getByRole('button', { name: 'Toggle left sidebar', exact: true }).first();
  await railToggle.click();
  await expectPaneMode();
  expect(await floats()).toBe(false);
  await expect(meta).toHaveCSS('width', '364px');
  await expect(meta).toBeVisible();
  expect((await meta.boundingBox())!.x).toBeGreaterThan(
    (await study.locator('.task-main').boundingBox())!.x,
  );
  expect(await page.evaluate(() => innerWidth)).toBe(1100);
  await railToggle.click();
  await expectPaneMode();
  await page.setViewportSize({ width: 1920, height: 1080 });
  await expect(meta).toHaveCSS('width', '364px');
  await splitter.focus();
  await page.keyboard.press('ArrowLeft');
  await expect(meta).toHaveCSS('width', '374px');
  expect(await page.evaluate(() => localStorage.getItem('lykeion.study-inspector-width'))).toBe(
    '424',
  );
  const compactPreference = await page.evaluate(() =>
    localStorage.getItem('lykeion.embedded-study-inspector-width'),
  );
  expect(compactPreference).toBe('374');
  await page.keyboard.press('End');
  await expect(meta).toHaveCSS('width', '640px');
  await page.setViewportSize({ width: 1200, height: 982 });
  const available = (await study.boundingBox())!.width - 400;
  await expect(splitter).toHaveAttribute('aria-valuemax', String(available));
  await expect(meta).toHaveCSS('width', `${available}px`);
  await page.setViewportSize({ width: 1100, height: 982 });
  await expectPaneMode();
  expect(
    await page.evaluate(() => localStorage.getItem('lykeion.embedded-study-inspector-width')),
  ).toBe('640');
  await page.setViewportSize({ width: 1920, height: 1080 });
  await expect(meta).toHaveCSS('width', '640px');
  await splitter.focus();
  await page.keyboard.press('Home');
  await page.keyboard.press('ArrowLeft');
  await page.keyboard.press('ArrowLeft');
  await expect(meta).toHaveCSS('width', '260px');
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-study-inspector-refinements/final-fix-artifacts/embedded-1920.png',
  });
  for (const width of [1100, 900]) {
    await page.setViewportSize({ width, height: 982 });
    await expectPaneMode();
    if (width === 900) {
      expect(await floats()).toBe(true);
      await expect(splitter).toBeHidden();
    }
    const main = await study.locator('.task-main').boundingBox();
    const area = await study.boundingBox();
    // A docked pane shares the study's width; a floating one leaves it whole.
    const docked = (await floats()) ? 0 : (await meta.boundingBox())!.width;
    expect(main!.width + docked).toBeCloseTo(area!.width, 0);
    await page.getByRole('textbox', { name: 'Comment' }).fill(`Usable at ${width}`);
    const composer = await study.locator('.pinned-composer').boundingBox();
    const evidence = await study.locator('.transcript-scroll').boundingBox();
    expect(evidence!.y + evidence!.height).toBeLessThanOrEqual(composer!.y + 1);
    expect(await study.evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(0);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
    ).toBeLessThanOrEqual(0);
    await page.screenshot({
      path: `.superpowers/sdd/2026-10-15-study-inspector-refinements/final-fix-artifacts/embedded-${width}.png`,
    });
  }
  await page.setViewportSize({ width: 1920, height: 1080 });
  await expect(meta).toHaveCSS('width', '260px');
  expect(
    await page.evaluate(() => localStorage.getItem('lykeion.embedded-study-inspector-width')),
  ).toBe('260');
  await page.goto(`/#/studies/${card.id}`);
  await expect(meta).toHaveCSS('width', '424px');
  await splitter.focus();
  await page.keyboard.press('ArrowLeft');
  await expect(meta).toHaveCSS('width', '434px');
  expect(
    await page.evaluate(() => localStorage.getItem('lykeion.embedded-study-inspector-width')),
  ).toBe('260');
  await page.goto(`/#/inbox/${item.id}`);
  await expect(meta).toHaveCSS('width', '260px');
  expect(await page.evaluate(() => localStorage.getItem('lykeion.study-inspector-width'))).toBe(
    '434',
  );
});

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
  await page.goto('/#/inbox');
  const list = page.getByRole('navigation', { name: 'Inbox items' });
  for (const [, , shown] of kinds)
    await expect(list.getByText(shown, { exact: true })).toBeVisible();
  await page.screenshot({ path: 'test-results/teams-scoring/inbox-kinds.png', fullPage: true });
  for (const i of [4, 1]) {
    await list.getByText(kinds[i]![2], { exact: true }).click();
    await expect(
      page.getByRole('region', { name: 'Study' }).getByRole('heading', { name }),
    ).toBeVisible();
    await expect(page).toHaveURL(/#\/inbox\/[0-9a-f-]{36}$/);
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
  const items = await (await page.request.get('/api/inbox', { headers })).json();
  const item = items.find((i: { autoresearch_run_id: string }) => i.autoresearch_run_id === runId);
  expect(item.card_id).toBeNull();
  await page.goto('/#/inbox');
  await row(page, item.id).click();
  const dialog = page.getByRole('dialog', { name: 'Autoresearch run' });
  await expect(dialog.getByRole('heading', { name: 'Report literature watch' })).toBeVisible();
  await expect(dialog.getByText('Running', { exact: true })).toBeVisible();
  await expect(dialog.getByText('end-of-full-report', { exact: false })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Study' })).toHaveCount(0);
  await expect
    .poll(
      async () =>
        (await (await page.request.get('/api/inbox?archived=1', { headers })).json()).find(
          (i: { id: string }) => i.id === item.id,
        )?.read_at,
    )
    .toBeTruthy();
  await dialog.getByRole('tab', { name: 'Raw', exact: true }).click();
  expect(await dialog.locator('pre.run-raw').textContent()).toBe(fullAutoresearchReport);
  await page.screenshot({ path: 'test-results/ui2/task15-inbox.png', fullPage: true });
  await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page).toHaveURL(new RegExp(`#/inbox/${item.id}$`));
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
  const items = await (await page.request.get('/api/inbox', { headers })).json();
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
  await page.route('**/api/inbox?archived=1', async (route) => {
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
    await page.goto('/#/inbox');
    await initial;
    holding = true;
    await row(page, item.id).click();
    await expect(page.getByRole('navigation', { name: 'Inbox items' })).toContainText(
      'Nothing needs you right now',
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
    await page.getByRole('button', { name: 'Archived', exact: true }).click();
    await expect(row(page, item.id)).toBeVisible();
    await expect(row(page, item.id).locator('.unread')).toHaveCount(0);
    const receipt = (
      await (await page.request.get('/api/inbox?archived=1', { headers })).json()
    ).find((i: { id: string }) => i.id === item.id);
    expect(receipt.read_at).toBeTruthy();
    expect(receipt.resolved_at).toBeTruthy();
  } finally {
    release();
  }
});

test('study descriptions render safe, readable Markdown in Studies and Inbox', async ({
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
  await labInbox(page.request, study.id, 'escalated', 'Inspect Markdown');
  const item = (await inbox(page)).find((i) => i.card_id === study.id)!;
  expect(item).toBeTruthy();
  for (const url of [`/#/studies/${study.id}`, `/#/inbox/${item.id}`]) {
    await page.goto(url);
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
  }
});
