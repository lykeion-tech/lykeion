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
  await expect(groups.first()).not.toHaveAttribute('open');
  await expect(turn.locator('.run-activity[open]')).toHaveCount(0);
  await main.locator('.run-panel').evaluate((el) => (el.scrollTop = 0));
  await page.screenshot({ path: `${artifacts}/reading-light-1920.png` });
  await expect(groups.first().locator('.activity-title')).toHaveCount(0);
  const call = groups.first().locator('.activity-step[data-message-type="tool_use"]');
  await expect(call.getByText('Inspect the study record', { exact: true })).toBeVisible();
  await expect(call.locator('.tool-step')).toHaveAttribute('open', '');
  await page.screenshot({ path: `${artifacts}/compact-steps-light-1920.png` });
  await call.locator('.tool-step > summary').focus();
  await page.keyboard.press('Enter');
  await expect(call.locator('.tool-step')).not.toHaveAttribute('open');
  await page.keyboard.press('Enter');
  await expect(call.locator('.tool-step')).toHaveAttribute('open', '');
  await expect(call.getByText('lyk card show', { exact: true })).toBeVisible();
  await expect(call.locator('.activity-error')).toContainText('Error');
  await expect(
    groups
      .first()
      .getByText('Recorded lab reasoning remains collapsed by default.', { exact: true }),
  ).toBeHidden();
  await expect(groups.first().locator('.step-output[open]')).toHaveCount(0);
  await expect(groups.first().locator('.step-intent')).toHaveCSS('white-space', 'pre-wrap');
  await expect(groups.first().locator('.step-intent')).toHaveCSS('overflow-x', 'visible');
  await expect(groups.first().locator('.step-payload[open]')).toHaveCount(0);
  await main.locator('.run-panel').evaluate((el) => (el.scrollTop = 0));
  await page.screenshot({ path: `${artifacts}/activity-expanded-light-1920.png` });
  const output = call.locator('.step-output');
  await expect(output.locator('pre')).toBeHidden();
  await output.locator('summary').click();
  await expect(output.locator('pre')).toHaveText(
    'The scripted card read was declined. No evidence was created.',
  );
  await expect(output.locator('pre')).toHaveCSS('max-height', 'none');
  await output.locator('summary').click();
  await turn.getByRole('button', { name: 'Copy code', exact: true }).click();
  await expect(turn.locator('.copy-notice:not(:empty)')).toHaveText('Code copied');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(
    'long recorded value',
  );
  const payload = call.locator('.step-payload[data-message-type="tool_result"]');
  await expect(turn.locator('.step-sequence')).toHaveCount(0);
  await expect(turn.locator('.step-state').filter({ hasText: /recorded/i })).toHaveCount(0);
  await payload.locator('summary').click();
  await expect(payload.locator('pre')).toContainText('"tool_use_id": "t1"');
  await expect(payload.locator('pre')).toContainText('"is_error": true');
  await expect(payload.locator('pre')).toHaveCSS('max-height', 'none');
  await payload.locator('summary').click();
  await page.evaluate(() =>
    localStorage.setItem('lykeion.preferences', JSON.stringify({ theme: 'midnight' })),
  );
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-palette', 'midnight');
  await openAgentTranscript(page);
  await expect(turn.getByRole('heading', { name: 'Recorded observations' })).toBeVisible();
  await expect(turn.locator('.run-activity[open]')).toHaveCount(0);
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
  await openAgentTranscript(page);
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
  await turn
    .locator('.run-activity .activity-step[data-message-type="tool_result"] .step-output > summary')
    .click();
  await expect(turn.getByText('Recorded tool refusal', { exact: true })).toBeVisible();
  const tool = turn.locator('.run-activity .step-payload').first();
  await tool.locator('summary').click();
  await expect(tool.locator('pre')).toContainText('END OF FULL TOOL INPUT');
  await expect(turn.getByText('Full recorded history', { exact: true })).toHaveCount(0);
  await expect(turn.locator('.activity-step')).toHaveCount(3);
  await expect(turn.locator('.markdown-body').last()).toContainText('Attempt 2 message 501');
});
