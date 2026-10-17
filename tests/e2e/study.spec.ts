import { expect, test } from '@playwright/test';
import { newStudy } from './lab';

test('the study page shows the question, the run turn, events, comments and the metadata', async ({
  page,
}) => {
  const question = `Microglia pruning after sleep loss ${Date.now()}?`;
  const study = await newStudy(page.request, question, {
    title: 'Microglia pruning after sleep loss',
  });
  await page.goto(`/#/studies/${study.id}`);
  const main = page.getByRole('region', { name: 'Study' });
  await expect(
    main.getByRole('heading', { name: 'Microglia pruning after sleep loss' }),
  ).toBeVisible();
  await expect(main.getByText(question)).toBeVisible();

  // The lab's agent arrives live: one run turn, its two commands, its text, the escalation line.
  await expect(main.getByText('Scoper', { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(main.getByText('Claude Code on lab-mac')).toBeVisible();
  await main.locator('.run-activity:not(.full-history) > summary').first().click();
  await expect(main.getByText('lyk card show', { exact: true })).toBeVisible();
  await expect(main.getByText('Read the card; the lab script stops here.')).toBeVisible();
  await expect(main.getByText('Escalated: waiting for a person')).toBeVisible();

  await main
    .getByRole('textbox', { name: 'Comment' })
    .fill('Prefer human islet data over mouse models.');
  await main.getByRole('button', { name: 'Send' }).click();
  await expect(main.locator('.bubble.mine', { hasText: 'Prefer human islet data' })).toBeVisible();
  await expect(main.getByRole('textbox', { name: 'Comment' })).toHaveValue('');

  const meta = page.getByRole('complementary', { name: 'Study details' });
  await expect(meta.locator('.metadata-owner')).toContainText('Ana Ruiz');
  await expect(meta.getByText('Grounding')).toBeVisible();
  await meta.getByRole('button', { name: 'Change' }).click();
  await meta.getByRole('spinbutton', { name: 'Budget in USD' }).fill('3');
  await meta.getByRole('button', { name: 'Save budget' }).click();
  await expect(meta.getByText(/of \$3\.00/)).toBeVisible();

  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-16-artifacts/study-comments-commands-metadata.png',
    fullPage: true,
  });
  await page.setViewportSize({ width: 1920, height: 1080 });
  await expect(meta.getByText('Stage', { exact: true })).toBeVisible();
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-study-inspector-refinements/task-2-artifacts/recorded-spend-inspector.png',
  });
  await meta.getByRole('button', { name: 'Open timeline', exact: true }).click();
  const runDialog = page.getByRole('dialog', { name: 'Runs', exact: true });
  await expect(runDialog.locator('.run-metrics')).toContainText('$0.01');
  await expect(runDialog.locator('.timeline-chart .cost-stroke')).not.toHaveAttribute(
    'd',
    'M0,100 L1000,100',
  );
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-study-inspector-refinements/task-2-artifacts/recorded-spend-timeline.png',
  });
  await runDialog.getByRole('button', { name: 'Close Runs' }).click();
  await page.getByRole('button', { name: 'Toggle details' }).click();
  await expect(meta).toHaveCount(0);
});

import { labRecords } from './lab';

test('long text and many records never scroll the study page sideways', async ({ page }) => {
  const token = 'x'.repeat(300);
  const question = `${'Does islet amyloid deposition track IL-1β in human donors across age, sex and BMI? '.repeat(25)}${token} ${Date.now()}`;
  expect(question.length).toBeGreaterThan(2000);
  const title = `${'A very long study title about beta-cell dedifferentiation '.repeat(4)}${Date.now()}`;
  const study = await newStudy(page.request, question, { title });
  await page.goto(`/#/studies/${study.id}`);
  await expect(page.getByText('Escalated: waiting for a person')).toBeVisible({ timeout: 30_000 });
  await labRecords(page.request, study.id, 40);
  await page.reload();
  await expect(page.locator('.record-line .key').filter({ hasText: /^E\d+$/ })).toHaveCount(40);
  await expect(page.locator('.record-line')).toHaveCount(41);
  const overflow = await page.evaluate(() => ({
    page: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    transcript: (() => {
      const el = document.querySelector('.transcript-scroll')!;
      return el.scrollWidth - el.clientWidth;
    })(),
  }));
  expect(overflow.page).toBeLessThanOrEqual(0);
  expect(overflow.transcript).toBeLessThanOrEqual(0);
  await expect(page.getByRole('textbox', { name: 'Comment' })).toBeInViewport();
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-16-artifacts/long-question-records.png',
    fullPage: true,
  });
});

import { LAB, labFixture } from './lab';

test('recorded Jev details retain zero, unknown and long reason; current attempt loads beyond 500', async ({
  page,
}) => {
  const fixture = await labFixture(page.request);
  await page.addInitScript(
    (ws) => localStorage.setItem('lykeion.workspace', ws),
    fixture.workspace_id,
  );
  const messageRequests: string[] = [];
  page.on('request', (r) => {
    if (r.url().includes(`/api/agent-runs/${fixture.run_id}/messages`))
      messageRequests.push(r.url());
  });
  await page.goto(`/#/studies/${fixture.card_id}`);
  const main = page.getByRole('region', { name: 'Study' });
  await expect(main.locator('.markdown-body').last()).toContainText('Attempt 2 message 501');
  await expect(main.getByText(/Attempt 1 message/)).toHaveCount(0);
  expect(messageRequests.some((u) => u.includes('attempt=2&after=500'))).toBe(true);
  expect(messageRequests.every((u) => u.includes('attempt=2'))).toBe(true);
  const zero = main.locator('.gate-line', { hasText: 'evidence.zero' });
  await zero.getByRole('button', { name: /Details/ }).click();
  const details = main.locator('.gate-details').first();
  await expect(
    details.getByText('Jev asked: Does the recorded evidence support the claim?'),
  ).toBeVisible();
  await expect(
    details.getByText('It chose: supports, with probability 0.00', { exact: true }),
  ).toBeVisible();
  await expect(details).toContainText('Threshold: passes at p ≥ 0.7, fails below 0.35');
  expect(fixture.pass_threshold).toBe(0.7);
  await expect(main.locator('.gate-reason')).toHaveText(fixture.long_reason);
  await expect(zero.locator('.why')).toHaveText(fixture.long_reason);
  await main
    .locator('.gate-line', { hasText: 'evidence.unknown' })
    .getByRole('button', { name: /Details/ })
    .click();
  await expect(main.locator('.gate-details').last()).toContainText(
    'It chose: unclear, with probability —',
  );
  const overflow = await main.evaluate((e) => e.scrollWidth - e.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-16-artifacts/gate-details.png',
    fullPage: true,
  });
  await main.locator('.markdown-body').last().scrollIntoViewIfNeeded();
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-16-artifacts/current-attempt-501.png',
    fullPage: true,
  });
});

test('a real socket reconnect refreshes without replay and workspace switching isolates the study', async ({
  page,
}) => {
  const fixture = await labFixture(page.request);
  const secondName = `Switch lab ${Date.now()}`;
  const created = await page.request.post('/api/workspaces', {
    data: { name: secondName, slug: `switch-${Date.now()}` },
  });
  expect(created.status()).toBe(201);
  const second = (await created.json()).id as string;
  expect(
    (
      await page.request.patch(`/api/workspaces/${second}/settings`, {
        data: { settings: { wip_limit: 0 } },
      })
    ).status(),
  ).toBe(204);
  const card = await page.request.post('/api/cards', {
    headers: { 'X-Workspace-ID': second },
    data: {
      question: `A separate workspace question ${Date.now()}?`,
      title: 'Separate workspace study',
    },
  });
  expect(card.status()).toBe(201);
  const secondCard = (await card.json()).id as string;
  await page.addInitScript(
    (ws) => localStorage.setItem('lykeion.workspace', ws),
    fixture.workspace_id,
  );
  let releaseReconnect!: () => void;
  const reconnectGate = new Promise<void>((resolve) => {
    releaseReconnect = resolve;
  });
  let connection = 0;
  let closeSocket!: () => Promise<void>;
  let droppedEvents = 0;
  const subscribed: string[] = [];
  await page.routeWebSocket(/\/ws$/, async (socket) => {
    const index = ++connection;
    if (index === 2) await reconnectGate;
    const server = socket.connectToServer();
    socket.onMessage((raw) => {
      const message = JSON.parse(String(raw));
      if (message.type === 'subscribe') subscribed.push(message.workspace);
      server.send(raw);
    });
    server.onMessage((raw) => {
      if (index === 2) {
        if (JSON.parse(String(raw)).type === 'event') droppedEvents++;
      } else socket.send(raw);
    });
    if (index === 1)
      closeSocket = async () => {
        await socket.close();
        await server.close();
      };
  });
  await page.goto(`/#/studies/${fixture.card_id}`);
  const main = page.getByRole('region', { name: 'Study' });
  await expect(main.locator('.markdown-body').last()).toContainText('Attempt 2 message 501');
  await expect.poll(() => subscribed).toEqual([fixture.workspace_id]);
  await closeSocket();
  const missed = 'Written while disconnected; recovered by HTTP refresh';
  expect(
    (
      await page.request.post(`/api/cards/${fixture.card_id}/comments`, {
        headers: { 'X-Workspace-ID': fixture.workspace_id },
        data: { body: missed },
      })
    ).status(),
  ).toBe(201);
  releaseReconnect();
  await expect(main.locator('.bubble', { hasText: missed })).toBeVisible();
  await expect.poll(() => droppedEvents).toBeGreaterThan(0);
  expect(subscribed.slice(0, 2)).toEqual([fixture.workspace_id, fixture.workspace_id]);
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-16-artifacts/reconnect-no-replay.png',
    fullPage: true,
  });
  await page
    .getByRole('complementary', { name: 'Workspace navigation' })
    .getByRole('button', { name: /Browser fixtures/ })
    .click();
  await page
    .getByRole('menu', { name: 'Workspace' })
    .getByRole('menuitem', { name: new RegExp(secondName) })
    .click();
  await expect(page).toHaveURL(/#\/studies$/);
  await expect(page.getByRole('region', { name: 'Study' })).toHaveCount(0);
  await page
    .getByRole('region', { name: 'Backlog' })
    .getByRole('button', { name: /Separate workspace study/ })
    .click();
  await expect(main.getByRole('heading', { name: 'Separate workspace study' })).toBeVisible();
  await expect.poll(() => subscribed.at(-1)).toBe(second);
  expect(
    (
      await page.request.post(`/api/cards/${fixture.card_id}/comments`, {
        headers: { 'X-Workspace-ID': fixture.workspace_id },
        data: { body: 'Old workspace activity must stay outside this study' },
      })
    ).status(),
  ).toBe(201);
  const fresh = 'A current workspace live comment';
  expect(
    (
      await page.request.post(`/api/cards/${secondCard}/comments`, {
        headers: { 'X-Workspace-ID': second },
        data: { body: fresh },
      })
    ).status(),
  ).toBe(201);
  await expect(main.locator('.bubble', { hasText: fresh })).toBeVisible();
  await expect(main.getByText(missed)).toHaveCount(0);
  await expect(main.getByText('Old workspace activity must stay outside this study')).toHaveCount(
    0,
  );
  expect(await page.evaluate(() => localStorage.getItem('lykeion.workspace'))).toBe(second);
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-16-artifacts/workspace-switch.png',
    fullPage: true,
  });
});

async function linkedStudies(page: import('@playwright/test').Page) {
  const created = await page.request.post('/api/workspaces', {
    data: { name: 'Study lifecycle lab', slug: `lifecycle-${Date.now()}` },
  });
  expect(created.status()).toBe(201);
  const ws = (await created.json()).id as string;
  expect(
    (
      await page.request.patch(`/api/workspaces/${ws}/settings`, {
        data: { settings: { wip_limit: 0 } },
      })
    ).status(),
  ).toBe(204);
  async function create(title: string, budget: number, follows: string[] = []) {
    const response = await page.request.post('/api/cards', {
      headers: { 'X-Workspace-ID': ws },
      data: {
        title,
        question: `${title} ${Date.now()}?`,
        budget_usd: budget,
        follows_from: follows,
      },
    });
    expect(response.status()).toBe(201);
    return (await response.json()).id as string;
  }
  const b = await create('Lifecycle B', 9);
  const a = await create('Lifecycle A', 5, [b]);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), ws);
  await page.goto(`/#/studies/${a}`);
  await expect(
    page.getByRole('region', { name: 'Study' }).getByRole('heading', { name: 'Lifecycle A' }),
  ).toBeVisible();
  return { ws, a, b };
}

async function studyHash(page: import('@playwright/test').Page, id: string, title: string) {
  await page.evaluate((card) => {
    location.hash = `/studies/${card}`;
  }, id);
  await expect(
    page.getByRole('region', { name: 'Study' }).getByRole('heading', { name: title }),
  ).toBeVisible();
}

async function holdStudyResponse(
  page: import('@playwright/test').Page,
  path: string,
  method: string,
  beforeFetch?: () => Promise<void>,
) {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  let received!: (status: number) => void;
  const accepted = new Promise<number>((resolve) => {
    received = resolve;
  });
  let done!: () => void;
  const drained = new Promise<void>((resolve) => {
    done = resolve;
  });
  let error = '';
  let intent: { workspace?: string; body?: unknown } = {};
  await page.route(`**${path}`, async (route) => {
    if (route.request().method() !== method) return route.continue();
    try {
      intent = {
        workspace: route.request().headers()['x-workspace-id'],
        body: method === 'POST' ? route.request().postDataJSON() : undefined,
      };
      await beforeFetch?.();
      const response = await route.fetch();
      if (response.status() >= 400) error = (await response.json()).error;
      received(response.status());
      await gate;
      await route.fulfill({ response });
    } finally {
      done();
    }
  });
  return {
    accepted,
    release,
    drained,
    get error() {
      return error;
    },
    get intent() {
      return intent;
    },
    async finish() {
      const received = page.waitForResponse(
        (response) => response.request().method() === method && response.url().endsWith(path),
      );
      release();
      await drained;
      await (await received).finished();
      await page.evaluate(() => new Promise(requestAnimationFrame));
    },
  };
}

test('study navigation resets the budget editor and comment draft', async ({ page }) => {
  const { a } = await linkedStudies(page);
  const main = page.getByRole('region', { name: 'Study' });
  const meta = page.getByRole('complementary', { name: 'Study details' });
  await main.getByRole('textbox', { name: 'Comment' }).fill('Draft for A');
  await meta.getByRole('button', { name: 'Change' }).click();
  await meta.getByRole('spinbutton', { name: 'Budget in USD' }).fill('77');
  await meta.getByRole('link', { name: 'Lifecycle B' }).click();
  await expect(main.getByRole('heading', { name: 'Lifecycle B' })).toBeVisible();
  await expect(meta.getByRole('spinbutton', { name: 'Budget in USD' })).toHaveCount(0, {
    timeout: 2000,
  });
  await expect(main.getByRole('textbox', { name: 'Comment' })).toHaveValue('');
  await meta.getByRole('button', { name: 'Change' }).click();
  await expect(meta.getByRole('spinbutton', { name: 'Budget in USD' })).toHaveValue('9');
  await studyHash(page, a, 'Lifecycle A');
  await expect(meta.getByRole('spinbutton', { name: 'Budget in USD' })).toHaveCount(0);
  await expect(main.getByRole('textbox', { name: 'Comment' })).toHaveValue('');
});

test('a delayed comment from an earlier visit cannot clear a returned study draft', async ({
  page,
}) => {
  const { a, b, ws } = await linkedStudies(page);
  const held = await holdStudyResponse(page, `/api/cards/${a}/comments`, 'POST');
  try {
    const main = page.getByRole('region', { name: 'Study' });
    await main.getByRole('textbox', { name: 'Comment' }).fill('Sent on the first A visit');
    await main.getByRole('button', { name: 'Send' }).click();
    expect(await held.accepted).toBe(201);
    const actual = await page.request.get(`/api/cards/${a}/comments`, {
      headers: { 'X-Workspace-ID': ws },
    });
    expect((await actual.json()).map((c: { body: string }) => c.body)).toContain(
      'Sent on the first A visit',
    );
    await studyHash(page, b, 'Lifecycle B');
    await studyHash(page, a, 'Lifecycle A');
    await main.getByRole('textbox', { name: 'Comment' }).fill('Draft from the returned A visit');
    await held.finish();
    await expect(main.getByRole('textbox', { name: 'Comment' })).toHaveValue(
      'Draft from the returned A visit',
      { timeout: 2000 },
    );
    await expect(main.getByRole('button', { name: 'Send' })).toBeEnabled();
  } finally {
    held.release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('a delayed deletion cannot close another study', async ({ page }) => {
  const { a, b } = await linkedStudies(page);
  const held = await holdStudyResponse(page, `/api/cards/${a}`, 'DELETE');
  try {
    page.once('dialog', (dialog) => dialog.accept());
    await page.getByRole('button', { name: 'Delete study' }).click();
    expect(await held.accepted).toBe(204);
    await studyHash(page, b, 'Lifecycle B');
    await held.finish();
    await expect(page).toHaveURL(new RegExp(`#/studies/${b}$`), { timeout: 2000 });
    await expect(
      page.getByRole('region', { name: 'Study' }).getByRole('heading', { name: 'Lifecycle B' }),
    ).toBeVisible();
  } finally {
    held.release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('a delayed budget save retains its original intent and cannot close another editor', async ({
  page,
}) => {
  const { a, ws } = await linkedStudies(page);
  const held = await holdStudyResponse(page, `/api/cards/${a}/budget`, 'POST');
  try {
    const meta = page.getByRole('complementary', { name: 'Study details' });
    await meta.getByRole('button', { name: 'Change' }).click();
    await meta.getByRole('spinbutton', { name: 'Budget in USD' }).fill('17');
    await meta.getByRole('button', { name: 'Save budget' }).click();
    expect(await held.accepted).toBe(200);
    expect(held.intent).toEqual({ workspace: ws, body: { usd: 17 } });
    expect(
      (
        await (
          await page.request.get(`/api/cards/${a}`, { headers: { 'X-Workspace-ID': ws } })
        ).json()
      ).budget_usd,
    ).toBe(17);
    await meta.getByRole('link', { name: 'Lifecycle B' }).click();
    await expect(
      page.getByRole('region', { name: 'Study' }).getByRole('heading', { name: 'Lifecycle B' }),
    ).toBeVisible();
    await meta.getByRole('button', { name: 'Change' }).click();
    await meta.getByRole('spinbutton', { name: 'Budget in USD' }).fill('31');
    await held.finish();
    await expect(meta.getByRole('spinbutton', { name: 'Budget in USD' })).toHaveValue('31');
    await page.screenshot({
      path: '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-16-artifacts/fix1-budget-scope.png',
      fullPage: true,
    });
  } finally {
    held.release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('an old comment finally cannot re-enable a newer pending submission', async ({ page }) => {
  const { a, b, ws } = await linkedStudies(page);
  const old = await holdStudyResponse(page, `/api/cards/${a}/comments`, 'POST');
  const current = await holdStudyResponse(page, `/api/cards/${b}/comments`, 'POST');
  try {
    const main = page.getByRole('region', { name: 'Study' });
    await main.getByRole('textbox', { name: 'Comment' }).fill('First visit sent comment');
    await main.getByRole('button', { name: 'Send' }).click();
    expect(await old.accepted).toBe(201);
    expect(old.intent).toEqual({ workspace: ws, body: { body: 'First visit sent comment' } });
    await studyHash(page, b, 'Lifecycle B');
    await main.getByRole('textbox', { name: 'Comment' }).fill('Current B pending submission');
    await main.getByRole('button', { name: 'Send' }).click();
    expect(await current.accepted).toBe(201);
    await old.finish();
    await expect(main.getByRole('textbox', { name: 'Comment' })).toHaveValue(
      'Current B pending submission',
    );
    await expect(main.getByRole('button', { name: 'Send' })).toBeDisabled();
    await current.finish();
    await expect(main.getByRole('textbox', { name: 'Comment' })).toHaveValue('');
    await expect(
      main.locator('.bubble', { hasText: 'Current B pending submission' }),
    ).toBeVisible();
  } finally {
    old.release();
    current.release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

for (const operation of ['comment', 'budget', 'delete'] as const) {
  test(`late real ${operation} refusals cannot affect a new study scope`, async ({ page }) => {
    const { a, b, ws } = await linkedStudies(page);
    const path = `/api/cards/${a}${operation === 'delete' ? '' : operation === 'budget' ? '/budget' : '/comments'}`;
    const held = await holdStudyResponse(
      page,
      path,
      operation === 'delete' ? 'DELETE' : 'POST',
      async () => {
        if (operation === 'delete')
          expect((await page.request.post(`${LAB}/lab/hypotheses/${a}`)).status()).toBe(204);
        else
          expect(
            (
              await page.request.delete(`/api/cards/${a}`, { headers: { 'X-Workspace-ID': ws } })
            ).status(),
          ).toBe(204);
      },
    );
    try {
      const main = page.getByRole('region', { name: 'Study' });
      const meta = page.getByRole('complementary', { name: 'Study details' });
      if (operation === 'comment') {
        await main
          .getByRole('textbox', { name: 'Comment' })
          .fill('Comment refused after concurrent deletion');
        await main.getByRole('button', { name: 'Send' }).click();
      } else if (operation === 'budget') {
        await meta.getByRole('button', { name: 'Change' }).click();
        await meta.getByRole('spinbutton', { name: 'Budget in USD' }).fill('21');
        await meta.getByRole('button', { name: 'Save budget' }).click();
      } else {
        page.once('dialog', (dialog) => dialog.accept());
        await page.getByRole('button', { name: 'Delete study' }).click();
      }
      expect(await held.accepted).toBe(operation === 'delete' ? 409 : 404);
      expect(held.error).not.toBe('');
      expect(held.intent.workspace).toBe(ws);
      if (operation === 'delete') {
        await page.getByRole('button', { name: 'Back to studies' }).click();
        await expect(main).toHaveCount(0);
        await page
          .getByRole('region', { name: 'Backlog' })
          .getByRole('button', { name: /Lifecycle B/ })
          .click();
      } else await studyHash(page, b, 'Lifecycle B');
      await expect(main.getByRole('heading', { name: 'Lifecycle B' })).toBeVisible();
      await main.getByRole('textbox', { name: 'Comment' }).fill('Keep the new scope draft');
      await meta.getByRole('button', { name: 'Change' }).click();
      await meta.getByRole('spinbutton', { name: 'Budget in USD' }).fill('41');
      await held.finish();
      await expect(main.getByRole('textbox', { name: 'Comment' })).toHaveValue(
        'Keep the new scope draft',
      );
      await expect(meta.getByRole('spinbutton', { name: 'Budget in USD' })).toHaveValue('41');
      await expect(main.getByRole('alert')).toHaveCount(0);
      await expect(page.locator('.toast', { hasText: held.error })).toHaveCount(0);
      await expect(page).toHaveURL(new RegExp(`#/studies/${b}$`));
    } finally {
      held.release();
      await page.unrouteAll({ behavior: 'wait' });
    }
  });
}

test('study events page beyond 500 and refresh later steering live', async ({ page }) => {
  const f = await labFixture(page.request);
  expect((await page.request.post(`${LAB}/lab/events/${f.card_id}?n=501`)).status()).toBe(204);
  const headers = { 'X-Workspace-ID': f.workspace_id };
  expect(
    (
      await page.request.post(`/api/cards/${f.card_id}/budget`, { headers, data: { usd: 13 } })
    ).status(),
  ).toBe(200);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
  const pages: string[] = [];
  page.on('request', (r) => {
    if (r.url().includes(`/api/cards/${f.card_id}/events`)) pages.push(r.url());
  });
  await page.goto(`/#/studies/${f.card_id}`);
  await expect(page.locator('.event-line', { hasText: 'Budget changed' })).toHaveCount(1);
  expect(pages.some((url) => /after=[1-9]/.test(url))).toBe(true);
  const origin = await page.evaluate(() => performance.timeOrigin);
  expect(
    (
      await page.request.post(`/api/cards/${f.card_id}/budget`, { headers, data: { usd: 14 } })
    ).status(),
  ).toBe(200);
  await expect(page.locator('.event-line', { hasText: 'Budget changed' })).toHaveCount(2);
  expect(await page.evaluate(() => performance.timeOrigin)).toBe(origin);
});

test('expanded run preserves every ordered recorded message and full tool content', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
  await page.goto(`/#/studies/${f.card_id}`);
  await expect(page.locator('.markdown-body').last()).toContainText('Attempt 2 message 501');
  await expect(page.locator('.markdown-body').first()).toContainText('Attempt 2 message 001');
  await page.locator('.full-history > summary').click();
  const messages = page.locator('.full-history .activity-steps > li');
  await expect(messages).toHaveCount(501);
  await expect(messages.nth(0)).toContainText('Attempt 2 message 001');
  await messages.nth(1).locator('.step-payload > summary').click();
  await expect(messages.nth(1).locator('pre')).toContainText('END OF FULL TOOL INPUT');
  await expect(messages.nth(2)).toContainText('Recorded tool refusal');
  await expect(messages.nth(3)).toContainText('Recorded continuity status');
  await expect(messages.nth(500)).toContainText('Attempt 2 message 501');
  await messages.nth(1).evaluate((el) => el.scrollIntoView({ block: 'start' }));
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/final-fix-artifacts/expanded-run.png',
    fullPage: true,
  });
  expect(
    await page.locator('.transcript-scroll').evaluate((el) => el.scrollWidth - el.clientWidth),
  ).toBeLessThanOrEqual(0);
});

test('transcript and run message refusals show retries and keep already loaded data', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  const comment = 'A real cached comment retained through failed reads';
  const headers = { 'X-Workspace-ID': f.workspace_id };
  expect(
    (
      await page.request.post(`/api/cards/${f.card_id}/comments`, {
        headers,
        data: { body: comment },
      })
    ).status(),
  ).toBe(201);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
  let refuse = true;
  for (const path of [
    `/api/cards/${f.card_id}/comments`,
    `/api/agent-runs/${f.run_id}/messages**`,
  ]) {
    await page.route(`**${path}`, async (route) => {
      if (!refuse) return route.continue();
      const response = await route.fetch({
        headers: {
          ...route.request().headers(),
          'X-Workspace-ID': '00000000-0000-0000-0000-000000000001',
        },
      });
      expect(response.status()).toBe(404);
      await route.fulfill({ response });
    });
  }
  await page.goto(`/#/studies/${f.card_id}`);
  await expect(page.locator('.study-read-error', { hasText: 'Comments' })).toBeVisible();
  await expect(page.locator('.run-read-error')).toBeVisible();
  await page
    .locator('.study-read-error')
    .first()
    .evaluate((el) => el.scrollIntoView({ block: 'start' }));
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/final-fix-artifacts/study-read-errors.png',
    fullPage: true,
  });
  refuse = false;
  await page.getByRole('button', { name: 'Retry Comments', exact: true }).click();
  await page.getByRole('button', { name: 'Retry run messages', exact: true }).click();
  await expect(page.locator('.bubble', { hasText: comment })).toBeVisible();
  await expect(page.locator('.markdown-body').last()).toContainText('Attempt 2 message 501');
  refuse = true;
  await page.evaluate(async () => {
    const app = document.querySelector('#app') as Element & {
      __vue_app__: {
        _context: { provides: Record<string, { invalidateQueries: () => Promise<void> }> };
      };
    };
    await app.__vue_app__._context.provides.VUE_QUERY_CLIENT!.invalidateQueries();
  });
  await expect(page.locator('.study-read-error', { hasText: 'Comments' })).toBeVisible();
  await expect(page.locator('.run-read-error')).toBeVisible();
  await expect(page.locator('.bubble', { hasText: comment })).toBeVisible();
  await expect(page.locator('.markdown-body').last()).toContainText('Attempt 2 message 501');
  refuse = false;
  await page.getByRole('button', { name: 'Retry Comments', exact: true }).click();
  await page.getByRole('button', { name: 'Retry run messages', exact: true }).click();
  await expect(page.locator('.study-read-error')).toHaveCount(0);
  await expect(page.locator('.run-read-error')).toHaveCount(0);
});

test('a real study deletion refusal remains inline on the study', async ({ page }) => {
  const { a } = await linkedStudies(page);
  await page.route(`**/api/cards/${a}`, async (route) => {
    if (route.request().method() !== 'DELETE') return route.continue();
    expect((await page.request.post(`${LAB}/lab/hypotheses/${a}`)).status()).toBe(204);
    const response = await route.fetch();
    expect(response.status()).toBe(409);
    await route.fulfill({ response });
  });
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Delete study' }).click();
  await expect(page.getByRole('region', { name: 'Study' }).getByRole('alert')).toContainText(
    'Backlog',
  );
});

import { labNoTeam, labRelease, labRevise, labShip, labUnship, labWorkspace } from './lab';

test('a revised study shows its starting point, and Revert restores the original question', async ({
  page,
}) => {
  const original = `Do beta cells re-express ALDH1A3, and does it track duration? ${Date.now()}`;
  const study = await newStudy(page.request, original);
  await page.goto(`/#/studies/${study.id}`);
  const main = page.getByRole('region', { name: 'Study' });
  await expect(main.getByText('Escalated: waiting for a person')).toBeVisible({ timeout: 30_000 });
  await expect(main.locator('.starting-point')).toHaveCount(0);
  const revised = `Do beta cells re-express ALDH1A3? ${Date.now()}`;
  await labRevise(page.request, study.id, revised, 'No donor metadata on disease duration');
  await page.reload();
  const block = main.locator('.starting-point');
  await expect(block).toContainText('Starting point · revised');
  await expect(block.locator('.now')).toHaveText(revised);
  await expect(block.locator('.was')).toHaveText(original);
  await expect(block.locator('.why')).toContainText('No donor metadata on disease duration');
  await expect(block.locator('.why')).toContainText(/run [0-9a-f]{8}/);
  await expect(main.locator('.study-question')).toHaveText(revised);
  await expect(main.getByText('Starting point revised')).toBeVisible();
  await page.screenshot({ path: 'test-results/ui-lab/study-revised.png', fullPage: true });

  let asked = '';
  page.once('dialog', (d) => {
    asked = d.message();
    void d.accept();
  });
  await block.getByRole('button', { name: 'Revert' }).click();
  await expect(main.locator('.study-question')).toHaveText(original);
  expect(asked).toContain('original');
  await expect(main.locator('.starting-point')).toHaveCount(0);
});

test('a reopened study says so in its pane and its transcript', async ({ page }) => {
  const study = await newStudy(page.request, `Does IL-1β track amyloid? ${Date.now()}`);
  await page.goto(`/#/studies/${study.id}`);
  const meta = page.getByRole('complementary', { name: 'Study details' });
  await expect(meta.getByText('Reopened', { exact: true })).toHaveCount(0);
  await labRelease(page.request, study.id);
  const reopen = await page.request.post(`/api/cards/${study.id}/reopen`, {
    headers: { 'X-Workspace-ID': await labWorkspace(page.request) },
    data: { reason: 'a 2026 study contradicts E2', evidence: 'europepmc:PMC9911203' },
  });
  expect(reopen.status()).toBe(200);
  await page.reload();
  await expect(meta.locator('.reopened')).toHaveText(/^Once · /);
  await expect(
    page.getByRole('region', { name: 'Study' }).getByText('Reopened: a 2026 study contradicts E2'),
  ).toBeVisible();
});

test('a study on demo v1 offers Move to v2 once v2 is approved, and moving updates the pane', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  const headers = { 'X-Workspace-ID': f.workspace_id };
  labShip('demo', '1');
  try {
    expect(
      (await page.request.put('/api/me/pack', { headers, data: { name: 'demo' } })).status(),
    ).toBe(200);
    const created = await page.request.post('/api/cards', {
      headers,
      data: { question: `Does X raise Y? ${Date.now()}`, start_kind: 'question' },
    });
    expect(created.status()).toBe(201);
    const study = (await created.json()) as { id: string };
    await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
    await page.goto(`/#/studies/${study.id}`);
    const meta = page.getByRole('complementary', { name: 'Study details' });
    const team = meta.locator('.team');
    await expect(team).toContainText('demo Team v1');
    await expect(meta.getByText('Question', { exact: true })).toBeVisible();
    await expect(team.getByRole('button', { name: /Move to/ })).toHaveCount(0);

    labShip('demo', '2');
    expect(
      (await page.request.put('/api/me/pack', { headers, data: { name: 'demo' } })).status(),
    ).toBe(200);
    await page.reload();
    let asked = '';
    page.once('dialog', (d) => {
      asked = d.message();
      void d.accept();
    });
    await team.getByRole('button', { name: 'Move to v2' }).click();
    await expect(team).toContainText('demo Team v2');
    await expect(team.getByRole('button', { name: /Move to/ })).toHaveCount(0);
    expect(asked).toContain('v2');
  } finally {
    await labNoTeam(page.request);
    labUnship('demo');
  }
});
