import { expect, test } from './test';
import { installFilesFixture, openFilePage, openSavedFile } from './coresearch-files-fixture';
test('active chat turns display the ring spinner beside Working', async ({ page }) => {
  await installFilesFixture(page);
  await page.route('**/api/coresearch/projects/p/chats/selected/messages', async (route) => {
    await route.fulfill({
      json: [
        {
          id: 'turn',
          chat_id: 'selected',
          kind: 'turn',
          body: 'Inspect data',
          agent_run_id: 'different',
          status: 'running',
        },
      ],
    });
  });
  await page.goto('/#/coresearch/p/selected');
  const status = page.getByRole('status').filter({ hasText: 'Working' });
  await expect(status).toBeVisible();
  await expect(status.locator('.run-status-ring-right')).toBeVisible();
  await expect(status.locator('.run-status-ring-left')).toBeVisible();
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await expect(status.locator('.run-status-ring-right')).toHaveCSS('animation-name', 'none');
});

test('chat transcript uses the Study step timeline, including errors and expandable output', async ({
  page,
}) => {
  await installFilesFixture(page);
  await page.route(
    '**/api/coresearch/projects/p/chats/selected/turns/different/steps',
    async (route) => {
      await route.fulfill({
        json: [
          { attempt: 1, seq: 0, type: 'text', tool: '', content: '  ' },
          { attempt: 1, seq: 0.5, type: 'log', tool: '', content: 'hidden log' },
          {
            attempt: 1,
            seq: 1,
            type: 'tool_use',
            tool: 'Bash',
            content: { type: 'tool_use', id: 'one', input: { command: 'echo first' } },
          },
          {
            attempt: 1,
            seq: 2,
            type: 'tool_result',
            tool: '',
            content: { tool_use_id: 'one', content: 'first result', is_error: true },
          },
          {
            attempt: 1,
            seq: 3,
            type: 'tool_use',
            tool: 'Bash',
            content: { type: 'tool_use', id: 'two', input: { command: 'echo second' } },
          },
          {
            attempt: 1,
            seq: 4,
            type: 'tool_result',
            tool: '',
            content: { tool_use_id: 'two', content: 'second result' },
          },
        ],
      });
    },
  );
  await page.goto('/#/coresearch/p/selected');
  const timeline = page.getByRole('list', { name: 'Transcript' });
  await expect(timeline.locator(':scope > li.turn-item')).toHaveCount(1);
  await expect(timeline).toHaveCSS('list-style-type', 'none');
  const group = timeline.locator('details[data-activity-group]');
  await expect(group).toBeVisible();
  await expect(group).toHaveAttribute('open', '');
  await expect(group.locator('summary').first()).toContainText('2 steps');
  await expect(group.locator('summary').first()).toContainText('1 error');
  await expect(group.getByText('first result')).toBeVisible();
  const second = group.locator('details.tool-step').nth(1);
  await second.locator('summary').click();
  await expect(second.getByText('second result')).toBeVisible();
  await group.locator('summary').first().click();
  await expect(group.locator('ol')).toBeHidden();
});

test('cross-chat transcript renders exact producing turn, SVG remains inert and members cannot rerun', async ({
  page,
}) => {
  const fixture = await installFilesFixture(page, { user: 'member' });
  await page.goto('/#/coresearch/p/selected');
  await openSavedFile(page, 'notes.txt');
  const pane = page.getByTestId('artifact-pane');
  await pane.getByRole('tab', { name: 'Transcript', exact: true }).click();
  await expect(pane).toContainText('Write the provenance note');
  await expect(pane).toContainText('Saved the exact note.');
  await expect(pane).toContainText('The producing chat tool transcript.');
  await expect(pane).not.toContainText('Unrelated selected chat');
  expect(fixture.reads).toContain(
    '/api/coresearch/projects/p/chats/producer/turns/producing-turn/steps',
  );
  expect(fixture.reads.some((p) => p.includes('/agent-runs/'))).toBe(false);
  await openSavedFile(page, 'figure.svg');
  const img = pane.locator('img');
  await expect(img).toHaveAttribute('src', /^blob:/);
  await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBe(40);
  await expect(pane.locator('svg')).toHaveCount(0);
  expect(await page.evaluate(() => Reflect.get(window, 'svgExecuted'))).toBeUndefined();
  await openSavedFile(page, 'unknown.bin');
  await expect(pane.getByRole('link', { name: 'Download' })).toHaveAttribute('href', /^blob:/);
  await expect(page.getByRole('button', { name: 'New run' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Rerun R1' })).toHaveCount(0);
});

test('version diff uses visible highlighted exact saved bytes and comparison picker', async ({
  page,
}) => {
  await installFilesFixture(page);
  await page.goto('/#/coresearch/p/selected');
  await openSavedFile(page, 'analysis.py');
  const pane = page.getByTestId('artifact-pane');
  await pane.getByRole('button', { name: 'Show changes' }).click();
  const diff = page.getByTestId('artifact-diff');
  await expect(diff.locator('code')).toContainText('-print("original script v1")');
  await expect(diff.locator('code')).toContainText('+print("new script head")');
  await expect(diff.locator('pre[style]')).toHaveCSS('background-color', 'rgb(36, 41, 46)');
  await pane.getByRole('button', { name: 'Comparing to v2' }).click();
  await pane.getByRole('option', { name: 'Version 1', exact: true }).click();
  await expect(pane.getByRole('button', { name: 'Comparing to v1' })).toBeVisible();
  await expect(diff.locator('code')).toContainText('-print("original script v1")');
  await pane.getByRole('button', { name: 'Hide changes' }).click();
  await expect(pane.getByTestId('artifact-diff')).toHaveCount(0);
});

test('a saved file page only views: no edit action, versions and download stay', async ({
  page,
}) => {
  const fixture = await installFilesFixture(page, { head: 2 });
  await openFilePage(page, 'f');
  const pane = page.getByTestId('artifact-pane');
  await expect(pane.locator('tbody')).toContainText('0.02');
  await expect(pane.getByRole('button', { name: 'Download' })).toBeVisible();
  await expect(pane.getByRole('button', { name: 'Previous version' })).toBeEnabled();
  await expect(pane.getByRole('button', { name: 'Edit content' })).toHaveCount(0);
  await expect(pane.getByRole('textbox')).toHaveCount(0);
  expect(fixture.writes).toEqual([]);
});
