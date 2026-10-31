import { expect, test } from '@playwright/test';
import { newStudy, labFixture, openAgentTranscript } from './lab';
const artifacts = 'test-results/l2-transcript';

test('real scripted CLI writing reads naturally and expanded activity stays compact with full recorded data', async ({
  page,
  context,
}) => {
  const external: string[] = [];
  page.on('request', (request) => {
    if (request.url().startsWith('https://example.com')) external.push(request.url());
  });
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.addInitScript(() => {
    if (!localStorage.getItem('lykeion.preferences'))
      localStorage.setItem('lykeion.preferences', JSON.stringify({ theme: 'ash' }));
  });
  await page.setViewportSize({ width: 1920, height: 1080 });
  const study = await newStudy(page.request, `Readable transcript regression ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  const main = page.getByRole('region', { name: 'Study', exact: true });
  await openAgentTranscript(page);
  const turn = main.locator('.agent-turn').first();
  await expect(
    turn.getByRole('heading', { name: 'Recorded observations', exact: true }),
  ).toBeVisible({ timeout: 30000 });
  await expect(
    turn.getByRole('heading', { name: 'What the tool recorded', exact: true }),
  ).toBeVisible();
  await expect(
    turn.getByText('Read the card; the lab script stops here.', { exact: true }),
  ).toBeVisible();
  await expect(turn.locator('.markdown-body strong')).toHaveText('recorded evidence');
  await expect(turn.locator('img,script,iframe')).toHaveCount(0);
  expect(await page.evaluate(() => 'transcriptUnsafe' in window)).toBe(false);
  expect(external).toEqual([]);
  const fontLoaded = await page.evaluate(async () => {
    await document.fonts.ready;
    return [...document.fonts].some(
      (font) => font.family.includes('Inter Variable') && font.status === 'loaded',
    );
  });
  expect(fontLoaded).toBe(true);
  const groups = turn.locator('.run-activity');
  await expect(groups).toHaveCount(2);
  const call = groups.first().locator('.activity-step[data-message-type="tool_use"]');
  await expect(call.getByText('Inspect the study record', { exact: true })).toBeVisible();
  await expect(call.locator('.tool-step')).toHaveAttribute('open', '');
  await expect(call.locator('.step-output')).toHaveText(
    'The scripted card read was declined. No evidence was created.',
  );
  await expect(turn.getByText('Full recorded message', { exact: false })).toHaveCount(0);
  await call.locator('.step-heading').click();
  await expect(call.locator('.tool-step')).not.toHaveAttribute('open');
  await call.locator('.step-heading').click();
  await expect(call.locator('.step-code-source')).toContainText('lyk card show');
  await call.getByRole('button', { name: 'Copy tool code' }).click();
  await expect(call.getByRole('button', { name: 'Copy tool code' })).toHaveText('Copied');
  await expect(call.locator('.step-copy .lucide-check')).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain('lyk card show');
  const assignment = turn.locator('.turn-assignment');
  await assignment.hover();
  await assignment.getByRole('button', { name: 'Copy message' }).click();
  await expect(assignment.getByRole('button', { name: 'Copy message' })).toHaveText('Copied');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(
    'Readable transcript regression',
  );
  const proseCode = turn.getByRole('button', { name: 'Copy code', exact: true });
  await proseCode.click();
  await expect(proseCode).toHaveText('Copied');
  await expect(proseCode).toHaveText('Copy', { timeout: 4000 });
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(
    'long recorded value',
  );
  await page.evaluate(() =>
    localStorage.setItem('lykeion.preferences', JSON.stringify({ theme: 'midnight' })),
  );
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-palette', 'midnight');
  await openAgentTranscript(page);
  await expect(turn.getByRole('heading', { name: 'Recorded observations' })).toBeVisible();
  await expect(turn.locator('.run-activity')).toHaveCount(2);
  await expect(turn.getByText('Inspect the study record', { exact: true })).toBeVisible();
  await main.locator('.run-panel').evaluate((el) => (el.scrollTop = 0));
  await page.screenshot({ path: `${artifacts}/reading-dark-1920.png` });
  await page.setViewportSize({ width: 800, height: 982 });
  await expect(turn.getByRole('heading', { name: 'Recorded observations' })).toBeVisible();
  expect(await main.evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(0);
  const code = turn.locator('.markdown-code pre');
  expect(await code.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
  await page.screenshot({ path: `${artifacts}/reading-dark-800.png` });
  await expect(turn.getByText('Full recorded history', { exact: true })).toHaveCount(0);
  await page.route('**/RecordedMarkdown.vue*', (route) => route.abort('failed'));
  await page.reload();
  await expect(page.locator('.agent-card').first()).toBeVisible();
  const closeOverlay = page.getByRole('button', { name: 'Close pane', exact: true });
  if (await closeOverlay.isVisible()) await closeOverlay.click();
  await openAgentTranscript(page);
  await expect(turn.locator('.formatting-notice')).toHaveRole('status');
  await expect(turn.locator('.formatting-notice')).toHaveText(
    'Markdown formatting is unavailable; showing recorded text.',
  );
  await expect(turn.locator('.turn-assignment .recorded-text-fallback')).toContainText(
    'Readable transcript regression',
  );
  await expect(turn.locator('.turn-timeline .recorded-text-fallback').first()).toContainText(
    '## Recorded observations',
  );
  await expect(turn.locator('.turn-timeline .recorded-text-fallback').last()).toContainText(
    'Read the card; the lab script stops here.',
  );
  await expect(turn.locator('script,img,iframe')).toHaveCount(0);
  await page.unroute('**/RecordedMarkdown.vue*');
});

test('current attempt pagination retains every message and compact raw payload access', async ({
  page,
}) => {
  const fixture = await labFixture(page.request);
  await page.addInitScript(
    (ws) => localStorage.setItem('lykeion.workspace', ws),
    fixture.workspace_id,
  );
  const urls: string[] = [];
  page.on('request', (request) => {
    if (request.url().includes(`/api/agent-runs/${fixture.run_id}/messages`))
      urls.push(request.url());
  });
  await page.goto(`/#/studies/${fixture.card_id}`);
  await openAgentTranscript(page);
  const turn = page.locator('.agent-turn').first();
  await expect(turn.locator('.markdown-body').last()).toContainText('Attempt 2 message 501');
  await expect(turn.locator('.markdown-body')).toHaveCount(498);
  await expect(turn.locator('.markdown-body').nth(1)).toHaveText('Attempt 2 message 005');
  await expect(turn.locator('.markdown-body').nth(2)).toHaveText('Attempt 2 message 006');
  await page.setViewportSize({ width: 1920, height: 1080 });
  await turn.locator('.markdown-body').first().scrollIntoViewIfNeeded();
  await page.screenshot({
    path: `${artifacts}/adjacent-recorded-text-1920.png`,
  });

  expect(urls.some((url) => url.includes('attempt=2&after=500'))).toBe(true);
  expect(urls.every((url) => url.includes('attempt=2'))).toBe(true);
  const failed = turn.locator('.run-activity .activity-step[data-message-type="tool_result"]');
  await expect(failed.locator('.step-output')).toContainText('Recorded tool refusal');
  const tool = turn.locator('.activity-step[data-message-type="tool_use"]').first();
  if (!(await tool.locator('.tool-step').evaluate((el) => (el as HTMLDetailsElement).open)))
    await tool.locator('.step-heading').click();
  await expect(tool.locator('.step-output')).toContainText('END OF FULL TOOL INPUT');
  await expect(turn.getByText('Full recorded message', { exact: false })).toHaveCount(0);
  // The status message is not shown, so the tool call and its result are the two steps.
  await expect(turn.locator('.activity-step')).toHaveCount(2);
  await expect(turn.locator('.markdown-body').last()).toContainText('Attempt 2 message 501');
});

test('notebook cells match the L2 tool-step geometry and retain code and output', async ({
  page,
  context,
}) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.addInitScript(
    () =>
      !localStorage.getItem('lykeion.preferences') &&
      localStorage.setItem('lykeion.preferences', JSON.stringify({ theme: 'ash' })),
  );
  await page.setViewportSize({ width: 1920, height: 1080 });
  const study = await newStudy(page.request, `Notebook cell transcript fixture ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  await openAgentTranscript(page);
  const turn = page.locator('.agent-turn').first();
  const cells = turn.locator('.notebook-cell');
  await expect(cells).toHaveCount(2, { timeout: 30000 });
  await expect(cells.first().locator('.cell-count')).toHaveText('In [3]');
  await expect(cells.nth(1).locator('.cell-count')).toHaveText('In [ ]');
  await expect(cells.first().locator('.step-code-source')).toContainText(
    'print("transcript UI check")',
  );
  await expect(cells.first().locator('.line-number')).toHaveText(['1', '2']);
  await expect(cells.first().locator('.step-code-source')).toHaveClass(/highlighted/);
  await expect(cells.first().locator('.step-code-source span[style*="color"]')).not.toHaveCount(0);
  await expect(turn.locator('.step-label').first()).toHaveCSS('font-size', '13px');
  const first = turn
    .locator('.activity-step')
    .filter({ has: page.locator('.notebook-cell') })
    .first();
  if (!(await first.locator('.tool-step').evaluate((el) => (el as HTMLDetailsElement).open)))
    await first.locator('.step-heading').click();
  await cells.first().getByRole('button', { name: 'Copy tool code' }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    'print("transcript UI check")\nprint(42)',
  );
  await expect(first.locator('.step-output')).toHaveText('transcript UI check\n42');
  await expect(first.locator('.step-payload')).toHaveCount(0);
  await page.locator('.run-panel').evaluate((el) => (el.scrollTop = 0));
  await page.screenshot({ path: `${artifacts}/notebook-steps-light-1920.png` });
  await page.setViewportSize({ width: 800, height: 982 });
  expect(
    await page.locator('.run-panel').evaluate((el) => el.scrollWidth - el.clientWidth),
  ).toBeLessThanOrEqual(0);
  await page.screenshot({ path: `${artifacts}/notebook-steps-light-800.png` });
  await page.evaluate(() =>
    localStorage.setItem('lykeion.preferences', JSON.stringify({ theme: 'midnight' })),
  );
  await page.reload();
  await expect(page.locator('.agent-card').first()).toBeVisible();
  const closePane = page.getByRole('button', { name: 'Close pane', exact: true });
  if (await closePane.isVisible()) await closePane.click();
  await openAgentTranscript(page);
  await expect(page.locator('html')).toHaveAttribute('data-palette', 'midnight');
  await expect(cells).toHaveCount(2);
  await expect(cells.first().locator('.step-code-source')).toHaveClass(/highlighted/);
  await page.locator('.run-panel').evaluate((el) => (el.scrollTop = 0));
  await page.screenshot({ path: `${artifacts}/notebook-steps-dark-800.png` });
});

test('study run prose uses the shared readable Markdown presentation', async ({ page }) => {
  const study = await newStudy(page.request, `Markdown transcript ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  await openAgentTranscript(page);
  const turn = page.locator('.agent-turn').first();
  const prose = turn
    .locator('.lyk-reading .markdown-body')
    .filter({ hasText: 'Recorded observations' });
  await expect(prose.getByRole('heading', { name: 'Recorded observations' })).toBeVisible();
  await expect(prose.locator('strong')).toContainText('recorded evidence');
  await expect(prose).toHaveCSS('line-height', '22.75px');
  await expect(turn.locator('script,iframe,img')).toHaveCount(0);
  await expect(turn.getByRole('button', { name: 'Copy code', exact: true })).toBeVisible();
  await page.setViewportSize({ width: 800, height: 982 });
  expect(await prose.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
});
