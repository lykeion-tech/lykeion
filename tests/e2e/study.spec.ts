import { expect, test } from '@playwright/test';
import { newStudy, openAgentTranscript } from './lab';

test('the study opens with its description and identifiable runs, keeping the question in its assignment', async ({
  page,
}) => {
  const question = `Microglia pruning after sleep loss ${Date.now()}?`;
  const study = await newStudy(page.request, question, {
    title: 'Microglia pruning after sleep loss',
  });
  // Cards read the runs listing; only an open transcript reads a run's messages.
  const messageReads: string[] = [];
  page.on('request', (request) => {
    if (/\/api\/agent-runs\/[^/]+\/messages/.test(request.url())) messageReads.push(request.url());
  });
  await page.goto(`/#/studies/${study.id}`);
  const main = page.getByRole('region', { name: 'Study' });
  await expect(
    main.getByRole('heading', { name: 'Microglia pruning after sleep loss' }),
  ).toBeVisible();
  await expect(main.locator('.study-question')).toHaveCount(0);
  await expect(main.locator('.transcript-heading')).toHaveCount(0);
  await expect(main.locator('.event-line').filter({ hasText: /^Created/ })).toHaveCount(0);
  await expect(main.getByRole('region', { name: 'Study description' })).toHaveText(question);

  // The lab's leader arrives live as its Team agent, previewing its latest text.
  const card = main.locator('.agent-card').first();
  await expect(card.getByRole('heading', { name: 'pi · ui-demo' })).toBeVisible({
    timeout: 30_000,
  });
  await expect(card.getByRole('button', { name: /Run 1$/ })).toBeVisible();
  await expect(card.getByRole('heading')).not.toHaveClass(/truncate/);
  await expect(card.locator('.preview')).toContainText('Read the card; the lab script stops here.');
  expect(messageReads).toEqual([]);
  const transcript = await openAgentTranscript(page);
  await expect(transcript.getByRole('heading', { name: 'pi · ui-demo', level: 2 })).toBeVisible();
  // The transcript opens with what the run was asked, not its prompt's framing.
  await expect(transcript.locator('.turn-assignment')).toContainText(question);
  await expect(transcript.locator('.turn-assignment')).not.toContainText('You are the leader');
  await transcript.getByRole('tab', { name: 'Environment' }).click();
  await expect(transcript.getByText('Claude Code', { exact: true })).toBeVisible();
  await expect(transcript.locator('.environment').getByText(/^lab-mac/)).toBeVisible();
  // The lab daemon reports its own machine's platform.
  await expect(transcript.locator('.environment li', { hasText: /Round\b/ })).toHaveCount(0);
  await expect(transcript.locator('.environment li', { hasText: /Attempt\b/ })).toHaveCount(0);
  const platform = transcript.locator('.environment li', { hasText: 'Platform' });
  await expect(platform.locator('code')).toHaveText(/^(linux|darwin|windows) \/ \w+$/);
  for (const value of await transcript.locator('.environment code').allTextContents())
    expect(value.trim()).not.toBe('');
  // The daemon keeps each call's id, so its result pairs with it; nothing
  // in the run carried code.
  await transcript.getByRole('tab', { name: 'Code' }).click();
  await expect(transcript.getByText('No code was recorded in this run.')).toBeVisible();
  await transcript.getByRole('tab', { name: 'Execution Log' }).click();
  const cells = transcript.locator('.cells > li');
  await expect(cells).toHaveCount(2);
  await expect(cells.first()).toHaveClass(/cell-error/);
  await expect(cells.first().locator('.source-line')).toHaveText(['1lyk card show']);
  await cells.first().locator('.cell-output summary').click();
  await expect(cells.first().locator('.cell-output pre')).toHaveText(
    'The scripted card read was declined. No evidence was created.',
  );
  await expect(cells.nth(1)).not.toHaveClass(/cell-error/);
  await transcript.getByRole('tab', { name: 'Transcript' }).click();
  await expect(transcript.getByText('lyk card show', { exact: true }).first()).toBeVisible();
  // A leader that stops without finishing is nudged once, so the scripted run
  // has a second turn that says the same.
  await expect(
    transcript.getByText('Read the card; the lab script stops here.').first(),
  ).toBeVisible();
  await transcript.getByRole('button', { name: 'Close transcript' }).click();
  await expect(transcript).toBeHidden();
  await expect(main.locator('.agent-card')).toHaveCount(2);

  await main
    .getByRole('textbox', { name: 'Comment' })
    .fill('Prefer human islet data over mouse models.');
  await main.getByRole('button', { name: 'Send' }).click();
  await expect(main.locator('.bubble.mine', { hasText: 'Prefer human islet data' })).toBeVisible();
  await expect(main.getByRole('textbox', { name: 'Comment' })).toHaveValue('');

  const meta = page.getByRole('complementary', { name: 'Right pane' });
  const surface = async (name: string) => {
    const back = meta.getByRole('button', { name: 'Back to surfaces', exact: true });
    if (await back.isVisible()) await back.click();
    await meta.getByRole('button', { name: name, exact: true }).click();
  };
  await surface('Details');
  await expect(meta.locator('.metadata-owner')).toContainText('Ana Ruiz');
  await surface('Properties');
  await expect(meta.getByText('Grounding')).toBeVisible();

  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-16-artifacts/study-comments-commands-metadata.png',
    fullPage: true,
  });
  await page.setViewportSize({ width: 1920, height: 1080 });
  await expect(meta.getByText('Stage', { exact: true })).toBeVisible();
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-study-inspector-refinements/task-2-artifacts/recorded-spend-inspector.png',
  });
  await surface('Timeline');
  const runDialog = meta.locator('.execution-log');
  await expect(runDialog.locator('.run-metrics')).toContainText('$0.01');
  await expect(runDialog.locator('.timeline-chart .cost-stroke')).not.toHaveAttribute(
    'd',
    'M0,100 L1000,100',
  );
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-study-inspector-refinements/task-2-artifacts/recorded-spend-timeline.png',
  });
  await page.getByRole('button', { name: 'Close pane' }).click();
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
  await expect(page.locator('.agent-card h3', { hasText: 'pi · ui-demo' }).first()).toBeVisible({
    timeout: 30_000,
  });
  await labRecords(page.request, study.id, 40);
  await page.reload();
  await expect(page.locator('.agent-card h3', { hasText: 'pi · ui-demo' }).first()).toBeVisible();
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

test('the current attempt loads beyond 500 messages', async ({ page }) => {
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
  const main = page.getByRole('region', { name: 'Study', exact: true });
  await openAgentTranscript(page);
  await expect(main.locator('.markdown-body').last()).toContainText('Attempt 2 message 501');
  await expect(main.getByText(/Attempt 1 message/)).toHaveCount(0);
  expect(messageRequests.some((u) => u.includes('attempt=2&after=500'))).toBe(true);
  expect(messageRequests.every((u) => u.includes('attempt=2'))).toBe(true);
  const overflow = await main.evaluate((e) => e.scrollWidth - e.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
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
  const card = await page.request.post(`${LAB}/lab/idle-study`, {
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
  const transcript = await openAgentTranscript(page);
  await expect(main.locator('.markdown-body').last()).toContainText('Attempt 2 message 501');
  await transcript.getByRole('button', { name: 'Close transcript' }).click();
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
    .getByRole('region', { name: 'Grounding' })
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
  async function create(title: string) {
    const response = await page.request.post(`${LAB}/lab/idle-study`, {
      headers: { 'X-Workspace-ID': ws },
      data: { title, question: `${title} ${Date.now()}?` },
    });
    expect(response.status()).toBe(201);
    return (await response.json()).id as string;
  }
  const b = await create('Lifecycle B');
  const a = await create('Lifecycle A');
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

// The owner deletes a study from its card's menu on the board.
async function deleteFromBoard(page: import('@playwright/test').Page, id: string) {
  await page.locator(`.study-card:has([data-card="${id}"]) .study-card-actions`).click();
  await page.getByRole('menuitem', { name: 'Delete', exact: true }).click();
}

// Opens one of the study's right-pane surfaces from the chooser.
async function openSurface(page: import('@playwright/test').Page, name: string) {
  const pane = page.getByRole('complementary', { name: 'Right pane', exact: true });
  await pane.getByRole('button', { name, exact: true }).click();
  return pane;
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

test('study navigation resets the comment draft', async ({ page }) => {
  const { a, b } = await linkedStudies(page);
  const main = page.getByRole('region', { name: 'Study' });
  await main.getByRole('textbox', { name: 'Comment' }).fill('Draft for A');
  await studyHash(page, b, 'Lifecycle B');
  await expect(main.getByRole('heading', { name: 'Lifecycle B' })).toBeVisible();
  await expect(main.getByRole('textbox', { name: 'Comment' })).toHaveValue('');
  await studyHash(page, a, 'Lifecycle A');
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
  await page.goto('/#/studies');
  const held = await holdStudyResponse(page, `/api/cards/${a}`, 'DELETE');
  try {
    page.once('dialog', (dialog) => dialog.accept());
    await deleteFromBoard(page, a);
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

for (const operation of ['comment', 'delete'] as const) {
  test(`late real ${operation} refusals cannot affect a new study scope`, async ({ page }) => {
    const { a, b, ws } = await linkedStudies(page);
    const path = `/api/cards/${a}${operation === 'delete' ? '' : '/comments'}`;
    const held = await holdStudyResponse(
      page,
      path,
      operation === 'delete' ? 'DELETE' : 'POST',
      async () =>
        expect(
          (
            await page.request.delete(`/api/cards/${a}`, { headers: { 'X-Workspace-ID': ws } })
          ).status(),
        ).toBe(204),
    );
    try {
      const main = page.getByRole('region', { name: 'Study' });
      if (operation === 'comment') {
        await main
          .getByRole('textbox', { name: 'Comment' })
          .fill('Comment refused after concurrent deletion');
        await main.getByRole('button', { name: 'Send' }).click();
      } else {
        await page.goto('/#/studies');
        page.once('dialog', (dialog) => dialog.accept());
        await deleteFromBoard(page, a);
      }
      expect(await held.accepted).toBe(404);
      expect(held.error).not.toBe('');
      expect(held.intent.workspace).toBe(ws);
      if (operation === 'delete') {
        await expect(main).toHaveCount(0);
        await page
          .getByRole('region', { name: 'Grounding' })
          .getByRole('button', { name: /Lifecycle B/ })
          .click();
      } else await studyHash(page, b, 'Lifecycle B');
      await expect(main.getByRole('heading', { name: 'Lifecycle B' })).toBeVisible();
      await main.getByRole('textbox', { name: 'Comment' }).fill('Keep the new scope draft');
      await held.finish();
      await expect(main.getByRole('textbox', { name: 'Comment' })).toHaveValue(
        'Keep the new scope draft',
      );
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
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
  const pages: string[] = [];
  page.on('request', (r) => {
    if (r.url().includes(`/api/cards/${f.card_id}/events`)) pages.push(r.url());
  });
  await page.goto(`/#/studies/${f.card_id}`);
  await expect(page.getByRole('region', { name: 'Study', exact: true })).toBeVisible();
  await expect.poll(() => pages.some((url) => /after=[1-9]/.test(url))).toBe(true);
  const origin = await page.evaluate(() => performance.timeOrigin);
  expect(
    (
      await page.request.post(`/api/cards/${f.card_id}/stop`, { headers, data: { reason: 'done' } })
    ).status(),
  ).toBe(200);
  await expect(page.locator('.event-line', { hasText: 'Stopped' })).toHaveCount(0);
  expect(await page.evaluate(() => performance.timeOrigin)).toBe(origin);
});

test('expanded run preserves every ordered recorded message and full tool content', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
  await page.goto(`/#/studies/${f.card_id}`);
  const dialog = await openAgentTranscript(page);
  const markdown = dialog.locator('.markdown-body');
  await expect(markdown.last()).toContainText('Attempt 2 message 501');
  await expect(markdown.first()).toContainText('Attempt 2 message 001');
  await expect(dialog.getByText('Full recorded history', { exact: true })).toHaveCount(0);
  // The status message is not shown, so the tool call and its result are the two steps.
  const messages = dialog.locator('.activity-step');
  await expect(messages).toHaveCount(2);
  await expect(markdown).toHaveCount(498);
  const tool = messages.nth(0);
  if (!(await tool.locator('.tool-step').evaluate((el) => (el as HTMLDetailsElement).open)))
    await tool.locator('.step-heading').click();
  await expect(tool.locator('.step-output')).toContainText('END OF FULL TOOL INPUT');
  await expect(messages.nth(1)).toContainText('Recorded tool refusal');
  await messages.nth(0).evaluate((el) => el.scrollIntoView({ block: 'start' }));
  await page.screenshot({
    path: 'test-results/l2-transcript/expanded-run.png',
    fullPage: true,
  });
  expect(
    await page.locator('.run-panel').evaluate((el) => el.scrollWidth - el.clientWidth),
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
  await page
    .locator('.study-read-error')
    .first()
    .evaluate((el) => el.scrollIntoView({ block: 'start' }));
  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/final-fix-artifacts/study-read-errors.png',
    fullPage: true,
  });
  let transcript = await openAgentTranscript(page);
  await expect(page.locator('.run-read-error')).toBeVisible();
  refuse = false;
  await page.getByRole('button', { name: 'Retry run messages', exact: true }).click();
  await expect(page.locator('.markdown-body').last()).toContainText('Attempt 2 message 501');
  await transcript.getByRole('button', { name: 'Close transcript' }).click();
  await page.getByRole('button', { name: 'Retry Comments', exact: true }).click();
  await expect(page.locator('.bubble', { hasText: comment })).toBeVisible();
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
  await expect(page.locator('.bubble', { hasText: comment })).toBeVisible();
  transcript = await openAgentTranscript(page);
  await expect(page.locator('.run-read-error')).toBeVisible();
  await expect(page.locator('.markdown-body').last()).toContainText('Attempt 2 message 501');
  refuse = false;
  await page.getByRole('button', { name: 'Retry run messages', exact: true }).click();
  await transcript.getByRole('button', { name: 'Close transcript' }).click();
  await page.getByRole('button', { name: 'Retry Comments', exact: true }).click();
  await expect(page.locator('.study-read-error')).toHaveCount(0);
  await expect(page.locator('.run-read-error')).toHaveCount(0);
});

test('a real study deletion refusal remains inline on the board', async ({ page }) => {
  const { a, ws } = await linkedStudies(page);
  await page.route(`**/api/cards/${a}`, async (route) => {
    if (route.request().method() !== 'DELETE') return route.continue();
    expect(
      (
        await page.request.delete(`/api/cards/${a}`, { headers: { 'X-Workspace-ID': ws } })
      ).status(),
    ).toBe(204);
    const response = await route.fetch();
    expect(response.status()).toBe(404);
    await route.fulfill({ response });
  });
  await page.goto('/#/studies');
  page.once('dialog', (dialog) => dialog.accept());
  await deleteFromBoard(page, a);
  await expect(page.locator('.board-action-error')).toHaveText('not found');
});

import { labFlow, labTeam, labWorkspace } from './lab';

const shots = 'test-results/teams-scoring';
const core = [
  'Does the work cite where its facts came from?',
  'Does it answer what the researcher asked?',
  "Does it say what it couldn't do?",
];
const score = (i: number, level: number) => ({
  kind: 'score',
  key: `s${i}`,
  body: { criterion: core[i], source: 'core', level, probabilities: [0, 0, 0, 0, 1] },
});

async function unassigned(page: import('@playwright/test').Page) {
  return newStudy(page.request, `Does sleep loss prune microglia? ${Date.now()}`, {
    assignee: null,
  });
}

test('a blocked study is redone from the composer', async ({ page }) => {
  const study = await unassigned(page);
  await labFlow(page.request, study.id, {
    column: 'grounding',
    state: 'blocked',
    block_kind: 'low_score',
    redos: 1,
    records: [
      score(0, 1),
      score(1, 2),
      score(2, 2),
      { kind: 'outcome', key: 'o1', body: { verdict: 'low', attempt: 2 } },
    ],
  });
  await page.goto(`/#/studies/${study.id}`);
  const note = 'Scored low twice. Tell the Team what to fix to redo this column.';
  await expect(page.getByText(note)).toBeVisible();
  await page.getByRole('button', { name: 'Action', exact: true }).click();
  await expect(page.getByRole('menuitem')).toHaveText([/Comment/]);
  await page.keyboard.press('Escape');
  await page.screenshot({ path: `${shots}/blocked-composer.png`, fullPage: true });
  await page.getByRole('textbox', { name: 'Comment' }).fill('Cite the primary papers.');
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByText(note)).toHaveCount(0);
});

test('a Team study shows its Team as the assignee', async ({ page }) => {
  const workspaceId = await labWorkspace(page.request);
  const team = await labTeam(page.request, workspaceId);
  const study = await newStudy(page.request, `Who works this? ${Date.now()}`, {
    assignee: { type: 'team', teams: { hypotheses: team } },
  });
  await page.goto(`/#/studies/${study.id}`);
  await openSurface(page, 'Properties');
  const row = page.locator('.property-row', { hasText: 'Assignee' });
  await expect(row).toContainText('Team:');
  await expect(row).not.toContainText('Unassigned');
});

test('a finished single-stage study stays in its column, done', async ({ page }) => {
  const study = await unassigned(page);
  await labFlow(page.request, study.id, {
    column: 'grounding',
    state: 'done',
    records: [
      score(0, 5),
      score(1, 5),
      score(2, 4),
      { kind: 'outcome', key: 'o1', body: { verdict: 'high', attempt: 1 } },
    ],
  });
  await page.goto('/#/studies');
  const card = page.getByRole('region', { name: 'Grounding' }).locator(`[data-card="${study.id}"]`);
  await expect(card).toBeVisible();
  await expect(card.locator('.chip-done')).toHaveText('Done');
  await expect(
    page.getByRole('region', { name: 'Done' }).locator(`[data-card="${study.id}"]`),
  ).toHaveCount(0);
  await page.screenshot({ path: `${shots}/done-on-board.png`, fullPage: true });
});

test('the study shows its description without evidence in details', async ({ page }) => {
  const study = await unassigned(page);
  await labFlow(page.request, study.id, {
    column: 'grounding',
    state: 'working',
    records: [
      {
        kind: 'summary',
        key: 's1',
        body: { text: 'The protein lookup is recorded.\nFull details remain available here.' },
      },
      {
        kind: 'audit',
        key: 'a1',
        body: {
          text: 'One quote needs correction.',
          findings: ['The organism quote omits a field.'],
        },
      },
    ],
  });
  await page.goto(`/#/studies/${study.id}`);
  const description = page.getByRole('region', { name: 'Study description' });
  await expect(description).toContainText('Does sleep loss prune microglia?');
  await expect(description).not.toContainText('Audit recorded');
  await expect(description).not.toContainText('The protein lookup is recorded.');
  const pane = page.getByRole('complementary', { name: 'Right pane' });
  await pane.getByRole('button', { name: 'Properties', exact: true }).click();
  await expect(pane.getByLabel('Machine', { exact: true })).toHaveCount(0);
  await expect(pane).toContainText('Runs on:');
  await pane.getByRole('button', { name: 'Back to surfaces' }).click();
  await pane.getByRole('button', { name: 'Details', exact: true }).click();
  await expect(pane.getByText('View evidence and activity')).toHaveCount(0);
  await expect(pane.getByRole('region', { name: 'Audit' })).toHaveCount(0);
});

test('Properties stays the same width on the right and resizes inward', async ({ page }) => {
  const study = await unassigned(page);
  await page.goto(`/#/studies/${study.id}`);
  const area = page.getByRole('region', { name: 'Study', exact: true });
  const properties = area.getByRole('complementary', { name: 'Right pane' });
  await properties.getByRole('button', { name: 'Properties', exact: true }).click();
  const splitter = area.getByRole('separator', { name: 'Resize right pane' });
  await expect(properties).toHaveCSS('width', '240px');
  const pane = (await properties.boundingBox())!;
  const content = (await area.locator('.task-main').boundingBox())!;
  const container = (await area.boundingBox())!;
  expect(pane.x).toBeGreaterThan(content.x);
  expect(pane.x + pane.width).toBeCloseTo(container.x + container.width, 0);
  expect(pane.x - content.x - content.width).toBeCloseTo(0, 0);
  await splitter.focus();
  await page.keyboard.press('ArrowLeft');
  await expect(properties).toHaveCSS('width', '250px');
  await page.keyboard.press('ArrowRight');
  await expect(properties).toHaveCSS('width', '240px');
  const handle = (await splitter.boundingBox())!;
  await page.mouse.move(handle.x + handle.width / 2, handle.y + 200);
  await page.mouse.down();
  await page.mouse.move(handle.x - 40 + handle.width / 2, handle.y + 200);
  await page.mouse.up();
  await expect(properties).toHaveCSS('width', '280px');
});

test('study content and composer share a wider measure and only the corner button opens a run', async ({
  page,
}) => {
  await page.setViewportSize({ width: 2071, height: 960 });
  const study = await newStudy(page.request, `Study layout alignment ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  const main = page.getByRole('region', { name: 'Study', exact: true });
  const cards = main.locator('.agent-card');
  await expect(cards).toHaveCount(2);
  const content = main.locator('.study-content');
  await expect(content).toHaveCSS('max-width', '860px');
  const bounds = await main.locator('.agent-cards').boundingBox();
  const composer = await main.locator('.study-comment').boundingBox();
  expect(bounds).toBeTruthy();
  expect(composer).toBeTruthy();
  expect(composer!.x).toBeCloseTo(bounds!.x, 0);
  expect(composer!.width).toBeCloseTo(bounds!.width, 0);
  await cards.first().locator('.preview').click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await cards
    .first()
    .getByRole('button', { name: /Open transcript/ })
    .click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.getByRole('button', { name: 'Close transcript' }).click();
  await page.screenshot({ path: 'test-results/l2-transcript/study-width-composer-alignment.png' });
  await page.setViewportSize({ width: 800, height: 982 });
  const narrowCards = await main.locator('.agent-cards').boundingBox();
  const narrowComposer = await main.locator('.study-comment').boundingBox();
  expect(narrowComposer!.x).toBeCloseTo(narrowCards!.x, 0);
  expect(narrowComposer!.width).toBeCloseTo(narrowCards!.width, 0);
});

test('a study one agent works names the agent without an auditor chip', async ({ page }) => {
  const headers = { 'X-Workspace-ID': await labWorkspace(page.request) };
  const name = `Lone agent ${Date.now()}`;
  const made = await page.request.post('/api/agents', {
    headers,
    data: { name, stage: 'grounding', harness: 'claude' },
  });
  expect(made.status()).toBe(201);
  const agent = (await made.json()) as { id: string };
  const alone = await newStudy(page.request, `Worked alone ${Date.now()}?`, {
    assignee: { type: 'agent', id: agent.id },
    scope: { from: 'grounding', to: 'grounding' },
  });
  await page.goto(`/#/studies/${alone.id}`);
  await openSurface(page, 'Properties');
  const assignee = page.locator('.property-row', { hasText: 'Assignee' });
  await expect(assignee).toContainText(name);
  await expect(assignee.getByText('No auditor', { exact: true })).toHaveCount(0);

  const teamed = await newStudy(page.request, `Worked by a Team ${Date.now()}?`);
  await page.goto(`/#/studies/${teamed.id}`);
  await openSurface(page, 'Properties');
  await expect(page.locator('.property-row', { hasText: 'Assignee' })).toContainText('Team');
  await expect(page.getByText('No auditor', { exact: true })).toHaveCount(0);
});
