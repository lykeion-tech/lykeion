import { expect, test, type Page } from '@playwright/test';
import { installFilesFixture } from './coresearch-files-fixture';

async function fixture(page: Page) {
  await installFilesFixture(page);
  let draft = {
    title: '',
    context: '',
    deadline: null as string | null,
    card_id: null as string | null,
  };
  let archived = false;
  let marker: string | null = null;
  let messages: unknown[] = [];
  const writes: { path: string; method: string; body: any }[] = [];
  await page.route('**/api/**', async (route) => {
    const request = route.request(),
      url = new URL(request.url()),
      path = url.pathname;
    if (!path.startsWith('/api/lab/') && !path.startsWith('/api/decisions'))
      return route.fallback();
    const method = request.method(),
      body = request.postData() ? request.postDataJSON() : undefined;
    if (method !== 'GET') writes.push({ path, method, body });
    let data: unknown = [];
    if (path === '/api/decisions/count') data = { open: 0, due_soon: 0 };
    else if (path === '/api/lab/chats' && method === 'POST') data = { chat: { id: 'd1' } };
    else if (path === '/api/lab/chats')
      data = { chats: archived ? [] : [{ id: 'd1', title: draft.title }], next_cursor: '' };
    else if (path.endsWith('/draft')) {
      if (method === 'PUT') {
        const { applied_message_id, ...fields } = body;
        draft = fields;
        if (applied_message_id !== undefined) marker = applied_message_id;
      }
      data = {
        chat_id: 'd1',
        purpose: 'manual_decision_draft',
        draft,
        applied_message_id: marker,
        archived_at: archived ? '2026-11-04T00:00:00Z' : null,
      };
    } else if (path.endsWith('/messages')) data = { messages, pending: false, next_cursor: '' };
    else if (path.endsWith('/questions'))
      return route.fulfill({
        status: 409,
        json: { code: 'no_machine', error: 'Your machine is offline' },
      });
    else if (path.endsWith('/archive')) {
      archived = true;
      return route.fulfill({ status: 204 });
    } else if (path === '/api/decisions/manual') data = { id: 'note1' };
    return route.fulfill({ json: data });
  });
  return Object.assign(writes, {
    completeWhileAway() {
      messages = [
        {
          id: 'question-away',
          agent_run_id: 'run-away',
          kind: 'question',
          body: 'Draft context',
          current_draft: { ...draft },
          statements: [],
          evidence: [],
        },
        {
          id: '22222222-2222-2222-2222-222222222222',
          agent_run_id: 'run-away',
          kind: 'answer',
          body: '',
          proposal: {
            message: 'Completion while away',
            draft: { ...draft, context: 'AI context' },
          },
          statements: [],
          evidence: [],
        },
      ];
    },
  });
}

test('AI builder machine and model selectors stay compact', async ({ page }) => {
  await fixture(page);
  await page.goto('/#/decisions/new');
  await page.getByRole('button', { name: /Create with AI/ }).click();
  const setup = page.getByRole('dialog', { name: 'Choose a machine' });
  for (const name of ['Machine and CLI', 'Builder model']) {
    const select = setup.getByRole('combobox', { name });
    await expect(select).toBeVisible();
    await expect.poll(async () => (await select.boundingBox())?.width ?? 0).toBeLessThanOrEqual(145);
    await select.click();
    const menu = page.getByRole('listbox', { name });
    await expect(menu).toBeVisible();
    await expect.poll(async () => (await menu.boundingBox())?.width ?? 0).toBeLessThanOrEqual(280);
    await page.keyboard.press('Escape');
    await expect(menu).toHaveCount(0);
    await expect(select).toBeFocused();
  }
  const machine = setup.getByRole('combobox', { name: 'Machine and CLI' });
  const model = setup.getByRole('combobox', { name: 'Builder model' });
  await machine.click();
  await page.keyboard.press('Tab');
  await expect(model).toBeFocused();
  await model.click();
  await page.keyboard.press('Shift+Tab');
  await expect(machine).toBeFocused();
  const cancel = setup.getByRole('button', { name: 'Cancel' });
  await machine.evaluate((el) => el.setAttribute('tabindex', '3'));
  await model.evaluate((el) => el.setAttribute('tabindex', '2'));
  await cancel.evaluate((el) => el.setAttribute('tabindex', '4'));
  await machine.focus();
  await page.keyboard.press('Enter');
  await page.keyboard.press('ArrowDown');
  await expect(machine).toBeFocused();
  await expect(machine).toHaveAttribute('aria-activedescendant', /.+/);
  await page.keyboard.press('Tab');
  await expect(cancel).toBeFocused();
  for (const control of [machine, model, cancel]) await control.evaluate((el) => el.removeAttribute('tabindex'));
  await page.setViewportSize({ width: 900, height: 240 });
  await model.click();
  const popup = page.getByRole('listbox', { name: 'Builder model' });
  const box = await popup.boundingBox();
  expect(box).not.toBeNull();
  expect(box!.y).toBeGreaterThanOrEqual(0);
  expect(box!.y + box!.height).toBeLessThanOrEqual(240);
  await page.keyboard.press('Escape');
});

test('blank creation is keyboard accessible, survives reload and archives only after Create', async ({
  page,
}) => {
  const writes = await fixture(page);
  await page.goto('/#/decisions/new');
  await expect(page.locator('main > header.page-header')).toHaveCount(0);
  await page.getByRole('button', { name: /Start blank/ }).focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('textbox', { name: 'Title', exact: true })).toBeFocused();
  await page
    .getByRole('textbox', { name: 'Title', exact: true })
    .fill('Review the replication protocol');
  await page
    .getByRole('textbox', { name: 'Context', exact: true })
    .fill('Check the assay controls before the next run.');
  await page.getByLabel('Deadline', { exact: true }).fill('2099-12-31');
  await expect(page.getByRole('status').filter({ hasText: 'Draft saved' })).toBeVisible();
  await page.reload();
  await expect(page.getByRole('textbox', { name: 'Title', exact: true })).toHaveValue(
    'Review the replication protocol',
  );
  expect(writes.some((w) => w.path === '/api/decisions/manual')).toBe(false);
  await page.screenshot({ path: '/private/tmp/manual-decisions-form.png', fullPage: true });
  await page.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(page).toHaveURL(/decisions\/note1/);
  expect(writes.filter((w) => w.path === '/api/decisions/manual')).toHaveLength(1);
  expect(writes.some((w) => w.path.endsWith('/archive'))).toBe(true);
  expect(writes.some((w) => w.method === 'DELETE')).toBe(false);
});

test('AI draft uses the existing window, keeps offline edits and remains usable on a narrow screen', async ({
  page,
}) => {
  const writes = await fixture(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/#/decisions/new');
  await page.getByRole('button', { name: 'Toggle left sidebar', exact: true }).click();
  await expect(page.getByRole('button', { name: /Create with AI/ })).toBeVisible();
  await page.screenshot({ path: '/private/tmp/manual-decisions-start-mobile.png', fullPage: true });
  await page.getByRole('button', { name: /Create with AI/ }).click();
  await expect(page.getByRole('textbox', { name: 'Question', exact: true })).toBeFocused();
  await page
    .getByRole('textbox', { name: 'Question', exact: true })
    .fill('Help me plan a replication note');
  await page.getByRole('button', { name: 'Ask', exact: true }).click();
  await expect(
    page.getByRole('alert').filter({ hasText: 'Your machine is offline' }),
  ).toBeVisible();
  await expect(page.getByRole('textbox', { name: 'Question', exact: true })).toHaveValue(
    'Help me plan a replication note',
  );
  expect(writes.some((w) => w.path === '/api/decisions/manual')).toBe(false);
  await page.screenshot({ path: '/private/tmp/manual-decisions-ai-mobile.png', fullPage: true });
  await page.getByRole('button', { name: 'Minimize', exact: true }).click();
  await page.getByRole('textbox', { name: 'Title', exact: true }).fill('My note');
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth,
  );
  expect(overflow).toBe(false);
});

test('Agent starting screen keeps its original choices and disabled AI behavior', async ({
  page,
}) => {
  await fixture(page);
  await page.goto('/#/agents/new');
  await expect(page.getByRole('heading', { name: 'Create an agent' })).toBeVisible();
  await expect(page.getByRole('button', { name: /Start blank/ })).toBeEnabled();
  await expect(page.getByRole('button', { name: /Build with AI/ })).toBeDisabled();
  await page.screenshot({
    path: '/private/tmp/manual-decisions-agent-unchanged.png',
    fullPage: true,
  });
});

test('recovers an away completion once and preserves a saved human reversion after reload', async ({
  page,
}) => {
  const writes = await fixture(page);
  await page.goto('/#/decisions/new?draft=d1');
  await page.getByRole('textbox', { name: 'Title', exact: true }).fill('Original title');
  await expect(page.getByRole('status').filter({ hasText: 'Draft saved' })).toBeVisible();
  await page.getByRole('link', { name: 'Back to decisions', exact: true }).click();
  await expect(page).toHaveURL(/#\/decisions$/);
  writes.completeWhileAway();
  await page.goto('/#/decisions/new?draft=d1&mode=ai');
  await expect(page.getByRole('textbox', { name: 'Context', exact: true })).toHaveValue(
    'AI context',
  );
  await expect(page.getByRole('status').filter({ hasText: 'Draft saved' })).toBeVisible();
  expect(
    writes.some(
      (w) =>
        w.method === 'PUT' &&
        w.body.context === 'AI context' &&
        w.body.applied_message_id === '22222222-2222-2222-2222-222222222222',
    ),
  ).toBe(true);
  await page.getByRole('button', { name: 'Minimize', exact: true }).click();
  await page.getByRole('textbox', { name: 'Context', exact: true }).fill('');
  await expect(page.getByRole('status').filter({ hasText: 'Draft saved' })).toBeVisible();
  await page.reload();
  await expect(page.getByText('Completion while away', { exact: false })).toBeVisible();
  await expect(page.getByRole('textbox', { name: 'Context', exact: true })).toHaveValue('');
  expect(writes.some((w) => w.path === '/api/decisions/manual')).toBe(false);
});
