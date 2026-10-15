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
  await main.getByRole('button', { name: 'Ran 2 commands' }).click();
  await expect(main.getByText('lyk card show')).toBeVisible();
  await expect(main.getByText('Read the card; the lab script stops here.')).toBeVisible();
  await expect(main.getByText('Escalated: waiting for a person')).toBeVisible();

  await main
    .getByRole('textbox', { name: 'Comment' })
    .fill('Prefer human islet data over mouse models.');
  await main.getByRole('button', { name: 'Send' }).click();
  await expect(main.locator('.bubble.mine', { hasText: 'Prefer human islet data' })).toBeVisible();
  await expect(main.getByRole('textbox', { name: 'Comment' })).toHaveValue('');

  const meta = page.getByRole('complementary', { name: 'Study details' });
  await expect(meta.getByText('Ana Ruiz')).toBeVisible();
  await expect(meta.getByText('Grounding')).toBeVisible();
  await meta.getByRole('button', { name: 'Change' }).click();
  await meta.getByRole('spinbutton', { name: 'Budget in USD' }).fill('3');
  await meta.getByRole('button', { name: 'Save budget' }).click();
  await expect(meta.getByText(/of \$3\.00/)).toBeVisible();

  await page.screenshot({
    path: '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-16-artifacts/study-comments-commands-metadata.png',
    fullPage: true,
  });
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

import { labFixture } from './lab';

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
  await expect(main.getByText('Attempt 2 message 501', { exact: true })).toBeVisible();
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
  await main.getByText('Attempt 2 message 501', { exact: true }).scrollIntoViewIfNeeded();
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
  await expect(main.getByText('Attempt 2 message 501', { exact: true })).toBeVisible();
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
