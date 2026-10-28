import { expect, test } from '@playwright/test';
import { installFilesFixture, openSavedFile, texts } from './coresearch-files-fixture';
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

for (const destination of ['global route', 'workspace']) {
  test(`dirty Files draft guards declined and accepted ${destination} exit`, async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    const fixture = await installFilesFixture(page, { head: 1 });
    await page.goto('/#/coresearch/p/selected');
    await openSavedFile(page, 'residuals.csv');
    const pane = page.getByTestId('artifact-pane'),
      draft = `exact ${destination} draft\n\n`;
    await pane.getByRole('button', { name: 'Edit content', exact: true }).click();
    await pane.getByRole('textbox', { name: 'File content' }).fill(draft);
    fixture.remoteHead(2);
    await expect(pane).toContainText('v1 (old)');
    const exit = async () => {
      if (destination === 'global route') {
        await page.getByRole('button', { name: 'Show navigation', exact: true }).click();
        await page.getByRole('link', { name: 'Coresearch', exact: true }).click();
      } else {
        if (!(await page.locator('.workspace-switch').isVisible()))
          await page.getByRole('button', { name: 'Show navigation', exact: true }).click();
        if (!(await page.getByRole('menu', { name: 'Workspace', exact: true }).isVisible()))
          await page.locator('.workspace-switch').click();
        await page.getByRole('menuitem', { name: /Second lab/ }).click();
      }
    };
    await exit();
    const dialog = page.getByRole('dialog', { name: 'Discard unsaved changes?', exact: true });
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Keep editing', exact: true }).click();
    await expect(page).toHaveURL(/#\/coresearch\/p\/selected$/);
    expect(await page.evaluate(() => localStorage.getItem('lykeion.workspace'))).toBe('lab');
    await expect(pane.getByRole('textbox', { name: 'File content' })).toHaveValue(draft);
    await expect(pane).toContainText('v1 (old)');
    if (destination === 'workspace') await page.locator('.workspace-switch').click();
    await pane.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(pane.getByRole('alert')).toContainText('version 1 is not the latest (2 is)');
    expect(fixture.writes[0]?.workspace).toBe('lab');
    expect(fixture.writes[0]?.body).toContain('name="base"\r\n\r\n1');
    expect(fixture.writes[0]?.body).toContain(draft);
    await exit();
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Discard changes', exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page).toHaveURL(destination === 'workspace' ? /#\/studies$/ : /#\/coresearch$/);
    expect(await page.evaluate(() => localStorage.getItem('lykeion.workspace'))).toBe(
      destination === 'workspace' ? 'second' : 'lab',
    );
    await expect(page.getByRole('textbox', { name: 'File content' })).toHaveCount(0);
    expect(errors).toEqual([]);
  });
}

test('local pane exits share the application discard dialog without native or repeated prompts', async ({
  page,
}) => {
  await installFilesFixture(page, { head: 1 });
  let nativePrompts = 0;
  page.on('dialog', async (dialog) => {
    nativePrompts++;
    await dialog.dismiss();
  });
  await page.goto('/#/coresearch/p/selected');
  await openSavedFile(page, 'residuals.csv');
  const pane = page.getByTestId('artifact-pane'),
    draft = 'local pane draft\n';
  await pane.getByRole('button', { name: 'Edit content', exact: true }).click();
  await pane.getByRole('textbox', { name: 'File content' }).fill(draft);
  const notebook = page.getByRole('tab', { name: 'Notebook', exact: true }),
    discard = page.getByRole('dialog', { name: 'Discard unsaved changes?', exact: true });
  await notebook.click();
  await expect(discard).toHaveCount(1);
  await discard.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(pane.getByRole('textbox')).toHaveValue(draft);
  await notebook.click();
  await expect(discard).toHaveCount(1);
  await discard.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await expect(page.getByRole('tabpanel', { name: 'Notebook', exact: true })).toBeVisible();
  await expect(discard).toHaveCount(0);
  await openSavedFile(page, 'residuals.csv');
  await expect(pane.getByRole('button', { name: 'Edit content', exact: true })).toBeVisible();
  await expect(pane.getByRole('textbox', { name: 'File content' })).toHaveCount(0);
  await page
    .locator('.task-rightpane')
    .getByRole('button', { name: 'Close panel', exact: true })
    .click();
  await expect(discard).toHaveCount(0);
  expect(nativePrompts).toBe(0);
});
