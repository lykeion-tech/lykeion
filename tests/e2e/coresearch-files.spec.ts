import { expect, test } from '@playwright/test';
import { installFilesFixture, openSavedFile, texts } from './coresearch-files-fixture';
test('run outputs open original v1 through parent selection with exact script and run provenance', async ({
  page,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  const fixture = await installFilesFixture(page);
  await page.goto('/#/coresearch/p/selected');
  await page.getByRole('button', { name: 'Runs', exact: true }).click();
  const runs = page.getByRole('tabpanel', { name: 'Runs' });
  await expect(runs.getByText('matches original', { exact: false })).toHaveCount(1);
  await expect(runs.getByRole('button', { name: 'New run' })).toHaveCount(0);
  await expect(runs.getByRole('button', { name: 'Rerun R1' })).toBeVisible();
  await page.getByRole('button', { name: 'Outputs R1' }).click();
  await page.getByRole('button', { name: 'residuals.csv v1', exact: true }).click();
  const pane = page.getByTestId('artifact-pane');
  await expect(pane).toContainText('v1 (old)');
  await expect(pane.locator('tbody')).toContainText('0.02');
  await expect(pane.locator('tbody')).not.toContainText('9.99');
  await pane.getByRole('tab', { name: 'Code', exact: true }).click();
  await expect(pane).toContainText('analysis.py v1');
  await expect(pane.locator('code')).toContainText('original script v1');
  await pane.getByRole('tab', { name: 'Environment', exact: true }).click();
  await expect(pane).toContainText('abcdef0123456789');
  await expect(pane).toContainText('3.12.8');
  await pane.getByRole('tab', { name: 'Execution Log', exact: true }).click();
  await expect(pane).toContainText('Baseline stable');
  expect(fixture.reads).toContain('/api/coresearch/projects/p/runs/R1');
  expect(fixture.reads).toContain('/api/coresearch/projects/p/files/script/versions/1/content');
  expect(fixture.reads).not.toContain('/api/coresearch/projects/p/runs/rid');
  await pane.getByRole('button', { name: 'Restore this version' }).click();
  await expect(pane).toContainText('v4');
  await pane.getByRole('tab', { name: 'Code', exact: true }).click();
  await expect(pane).toContainText('Restored from v1.');
  await expect(pane).toContainText('Saved by a member.');
  await expect(pane).toContainText('No recorded execution provenance');
  expect(JSON.parse(fixture.writes[0]!.body)).toEqual({ n: 1, base: 3 });
  await pane.getByRole('tab', { name: 'Preview', exact: true }).click();
  await expect(pane.locator('tbody')).toContainText('0.02');
  await expect(pane.locator('tbody')).not.toContainText('9.99');
  expect(errors).toEqual([]);
});
test('typing before remote head update preserves draft and base through stale and force saves; intentional reload replaces edits', async ({
  page,
}) => {
  const fixture = await installFilesFixture(page, { head: 1 });
  await page.goto('/#/coresearch/p/selected');
  await openSavedFile(page, 'residuals.csv');
  const pane = page.getByTestId('artifact-pane');
  await pane.getByRole('button', { name: 'Edit content', exact: true }).click();
  const draft = 'time,residual\n1,7.125\n\n';
  await pane.getByRole('textbox', { name: 'File content' }).fill(draft);
  fixture.remoteHead(2);
  await expect(pane).toContainText('v1 (old)');
  await expect(pane.getByRole('textbox')).toHaveValue(draft);
  await pane.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(pane.getByRole('alert')).toContainText('version 1 is not the latest (2 is)');
  await pane.getByRole('button', { name: 'Save anyway' }).click();
  await expect.poll(() => fixture.writes.length).toBe(2);
  expect(fixture.writes[0]?.body).toContain('name="base"\r\n\r\n1');
  expect(fixture.writes[1]?.body).toContain('name="force"\r\n\r\ntrue');
  expect(fixture.writes[1]?.body).toContain(draft);
  expect(fixture.writes.every((w) => w.workspace === 'lab')).toBe(true);
  await expect(pane.locator('tbody')).toContainText('7.125');
  await expect(pane.getByRole('button', { name: 'Edit content' })).toBeVisible();
  await pane.getByRole('button', { name: 'Edit content' }).click();
  await pane.getByRole('textbox').fill('discard me');
  fixture.remoteHead(4);
  await expect(pane).toContainText('v3 (old)');
  await pane.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(pane.getByRole('alert')).toContainText('version 3 is not the latest (4 is)');
  await pane.getByRole('button', { name: 'Reload' }).click();
  await expect(pane.getByRole('textbox')).toHaveCount(0);
  await expect(pane).toContainText('v4');
  expect(texts[0]).toContain('0.02');
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
  await page.getByRole('tab', { name: 'Runs', exact: true }).click();
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
