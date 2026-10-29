import { expect, test } from '@playwright/test';
import { cowriteFixture } from './cowrite-editor-fixture';
test('real shared editor rebases, flushes file navigation, draws decisions and cursors', async ({
  page,
}) => {
  const f = await cowriteFixture(page);
  await page.goto('/#/coresearch/p/chat');
  const editor = page.locator('.cm-content');
  await expect(editor).toBeVisible();
  await expect(page.locator('.cm-settle-keep')).toHaveCount(1);
  await expect(page.locator('.cm-settle-undo')).toHaveCount(2);
  await page.locator('.cm-settle-keep').click();
  await expect
    .poll(() => f.writes.some((w) => w.path.endsWith('/changes/pending/keep')))
    .toBe(true);
  await expect(page.locator('.cm-ySelectionCaret')).toHaveCount(1);
  await page.locator('.cm-ySelectionCaret').hover();
  await expect(page.locator('.cm-ySelectionInfo')).toHaveText('Grace');
  f.forceConflict();
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  await editor.evaluate((el) => {
    const data = new DataTransfer();
    data.setData('text/plain', 'local 😀\r\n');
    el.dispatchEvent(
      new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }),
    );
  });
  await page.getByRole('button', { name: 'intro.tex', exact: true }).click();
  await expect(page).toHaveURL(/file=intro.tex/);
  await expect(editor).toContainText('Second file.');
  expect(f.conflicts).toBeGreaterThan(0);
  expect(f.docs.get('main')!.text).toContain('remote ');
  expect(f.docs.get('main')!.text).toContain('local 😀\r\n');
  await page.getByRole('button', { name: 'main.tex', exact: true }).click();
  await expect(editor).toContainText('local 😀');
  await page.getByRole('button', { name: 'Introduction', exact: true }).click();
  await expect(editor).toBeFocused();
  await page.getByRole('button', { name: 'refs.bib', exact: true }).click();
  await expect(page.locator('.ms-outline')).toHaveCount(0);
});
test('failed flush keeps pending document mounted and blocks leaving until recovered', async ({
  page,
}) => {
  const f = await cowriteFixture(page);
  await page.goto('/#/coresearch/p/chat');
  const editor = page.locator('.cm-content');
  await expect(editor).toBeVisible();
  f.setOffline(true);
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.insertText('unsaved');
  await page.getByRole('button', { name: 'intro.tex', exact: true }).click();
  await expect(page).toHaveURL(/\/p\/chat$/);
  await expect(editor).toContainText('unsaved');
  await expect(page.getByRole('alert').filter({ hasText: 'offline' })).toBeVisible();
  f.setOffline(false);
  await page.getByRole('button', { name: 'intro.tex', exact: true }).click();
  await expect(page).toHaveURL(/file=intro.tex/);
  expect(f.docs.get('main')!.text).toContain('unsaved');
});

test('owner selection flush pins current UTF-16 range, and controlled composer clears height', async ({
  page,
}) => {
  const f = await cowriteFixture(page);
  await page.goto('/#/coresearch/p/chat');
  const editor = page.locator('.cm-content');
  await expect(editor).toBeVisible();
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.insertText('selection 😀');
  await page.keyboard.press('ControlOrMeta+A');
  await editor.dispatchEvent('mouseup');
  await expect(page.getByRole('button', { name: 'Proofread', exact: true })).toBeVisible();
  const passage = await editor.evaluate(() => {
    const r = window.getSelection()!.getRangeAt(0).getBoundingClientRect();
    return { left: r.left, width: r.width };
  });
  const toolbar = await page.locator('.ms-sel').boundingBox();
  expect(Math.abs(toolbar!.x + toolbar!.width / 2 - passage.left - passage.width / 2)).toBeLessThan(
    1,
  );
  await page.getByRole('button', { name: 'Proofread', exact: true }).click();
  await expect.poll(() => f.writes.filter((w) => w.path.endsWith('/chat/turns')).length).toBe(1);
  const sent = f.writes.find((w) => w.path.endsWith('/chat/turns'))!.body;
  expect(sent.selection).toEqual({
    file_id: 'main',
    version: f.docs.get('main')!.version,
    from: 0,
    to: f.docs.get('main')!.text.length,
  });
  expect(sent.body).toContain('selection 😀');
  const field = page.getByRole('textbox', { name: 'Message the agent' });
  await field.fill('long sentence\n'.repeat(20));
  await expect(field).toHaveCSS('height', '200px');
  await page.getByRole('button', { name: 'Send', exact: true }).click();
  await expect(field).toHaveValue('');
  await expect
    .poll(async () => field.evaluate((el) => el.getBoundingClientRect().height))
    .toBeLessThan(60);
});
test('member toolbar exposes comments and real member mention IDs, never AI commands', async ({
  page,
}) => {
  const f = await cowriteFixture(page, false);
  await page.goto('/#/coresearch/p/chat');
  const editor = page.locator('.cm-content');
  await expect(editor).toBeVisible();
  await editor.click();
  await page.keyboard.press('ControlOrMeta+A');
  await editor.dispatchEvent('mouseup');
  await expect(page.getByRole('button', { name: 'Proofread', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Edit with AI', exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Comment', exact: true }).first().click();
  const field = page.getByRole('textbox', { name: 'Comment on the passage' });
  await field.fill('@Ada');
  await field.press('Enter');
  await field.press('Enter');
  await expect.poll(() => f.writes.some((w) => w.path.endsWith('/docs/main/comments'))).toBe(true);
  expect(f.writes.find((w) => w.path.endsWith('/docs/main/comments'))!.body.mentions).toEqual([
    'me',
  ]);
  await expect(page.getByRole('button', { name: 'Members', exact: true })).toHaveCount(0);
});
test('cursor polling does not bypass the 300ms edit batch', async ({ page }) => {
  const f = await cowriteFixture(page);
  await page.goto('/#/coresearch/p/chat');
  const editor = page.locator('.cm-content');
  await expect(editor).toBeVisible();
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  const start = Date.now();
  await page.keyboard.insertText('batch');
  await expect.poll(() => f.writes.filter((w) => w.path.endsWith('/steps')).length).toBe(1);
  expect(f.writes.find((w) => w.path.endsWith('/steps'))!.at - start).toBeGreaterThanOrEqual(250);
});

test('failed selection acceptance retries the identical pinned selection and request', async ({
  page,
}) => {
  const f = await cowriteFixture(page);
  f.rejectTurns(true);
  await page.goto('/#/coresearch/p/chat');
  const editor = page.locator('.cm-content');
  await expect(editor).toBeVisible();
  await editor.click();
  await page.keyboard.press('ControlOrMeta+A');
  await editor.dispatchEvent('mouseup');
  await page.getByRole('button', { name: 'Proofread', exact: true }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'turn unavailable' })).toBeVisible();
  const first = f.writes.find((w) => w.path.endsWith('/chat/turns'))!.body;
  f.rejectTurns(false);
  await page.getByRole('button', { name: 'Send', exact: true }).click();
  await expect.poll(() => f.writes.filter((w) => w.path.endsWith('/chat/turns')).length).toBe(2);
  expect(f.writes.filter((w) => w.path.endsWith('/chat/turns'))[1]!.body).toEqual(first);
});
test('two actual EditorViews converge with concurrent Unicode edits and CRLF', async ({
  page,
  context,
}) => {
  const f = await cowriteFixture(page);
  const other = await context.newPage();
  await f.attach(other);
  await page.goto('/#/coresearch/p/chat');
  await other.goto('/#/coresearch/p/chat');
  const a = page.locator('.cm-content'),
    b = other.locator('.cm-content');
  await expect(a).toBeVisible();
  await expect(b).toBeVisible();
  await a.click();
  await page.keyboard.press('ControlOrMeta+End');
  await b.click();
  await other.keyboard.press('ControlOrMeta+End');
  await Promise.all([page.keyboard.insertText('alpha😀'), other.keyboard.insertText('beta🪐')]);
  await expect.poll(() => f.docs.get('main')!.text).toContain('alpha😀');
  await expect.poll(() => f.docs.get('main')!.text).toContain('beta🪐');
  await expect.poll(async () => (await a.innerText()) === (await b.innerText())).toBe(true);
  await expect(a).toContainText('alpha😀');
  await expect(b).toContainText('alpha😀');
  await expect(a).toContainText('beta🪐');
  await expect(b).toContainText('beta🪐');
  expect(f.docs.get('main')!.text).toContain('paragraph.\r\n');
  await other.close();
});
test('extension-changing rename preserves the dirty EditorView and live journal on reopen', async ({
  page,
}) => {
  const f = await cowriteFixture(page);
  await page.goto('/#/coresearch/p/chat?file=intro.tex');
  const editor = page.locator('.cm-content');
  await expect(editor).toContainText('Second file.');
  await editor.evaluate((el) => el.setAttribute('data-same-view', 'yes'));
  f.setOffline(true);
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.insertText(' retained');
  f.renameFile('intro', 'sections/evidence.py');
  await expect(page.getByRole('button', { name: 'evidence.py', exact: true })).toBeVisible();
  await expect(editor).toHaveAttribute('data-same-view', 'yes');
  await expect(editor).toContainText('retained');
  f.setOffline(false);
  await page.getByRole('button', { name: 'evidence.py', exact: true }).click();
  await expect(page).toHaveURL(/file=sections\/evidence.py/);
  await expect(editor).toHaveAttribute('data-same-view', 'yes');
  await page.getByRole('button', { name: 'main.tex', exact: true }).click();
  await page.getByRole('button', { name: 'evidence.py', exact: true }).click();
  await expect(editor).toContainText('retained');
  await page.reload();
  await expect(editor).toContainText('retained');
  expect(f.docs.get('intro')!.text).toContain('retained');
});
test('long controlled draft restored from Settings autosizes on mount; drawer bounds persist', async ({
  page,
}) => {
  await cowriteFixture(page);
  await page.goto('/#/coresearch/p/chat');
  const field = page.getByRole('textbox', { name: 'Message the agent' });
  const long = 'restored sentence\n'.repeat(20);
  await field.fill(long);
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Skills', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Manage skills', exact: true }).click();
  await expect(page).toHaveURL(/settings\?tab=skills/);
  await page.goBack();
  await expect(field).toHaveValue(long);
  await expect(field).toHaveCSS('height', '200px');
  const grip = page.getByRole('separator', { name: 'Resize the conversation' });
  await grip.focus();
  await grip.press('End');
  expect(await page.evaluate(() => localStorage.getItem('lykeion:home-chat:p'))).toBe('70');
  await grip.press('Home');
  expect(await page.evaluate(() => localStorage.getItem('lykeion:home-chat:p'))).toBe('25');
  await page.reload();
  await expect(page.locator('.home-chat')).toHaveCSS('--home-chat-share', '25%');
});
test('a remote deletion disables the retained view on refetch', async ({ page }) => {
  const f = await cowriteFixture(page);
  await page.goto('/#/coresearch/p/chat?file=intro.tex');
  const editor = page.locator('.cm-content');
  await expect(editor).toContainText('Second file.');
  f.hideFile('intro');
  await expect(
    page.getByRole('alert').filter({ hasText: 'This file is no longer available.' }),
  ).toBeVisible();
  await expect(editor).toHaveAttribute('contenteditable', 'false');
  await expect(editor).toContainText('Second file.');
});

for (const person of ['me', 'member'])
  test(`expired dirty editor recovers through SignIn as ${person} without crossing identity`, async ({
    page,
  }) => {
    const f = await cowriteFixture(page);
    await page.goto('/#/coresearch/p/chat');
    const editor = page.locator('.cm-content');
    await expect(editor).toBeVisible();
    f.setOffline(true);
    await editor.click();
    await page.keyboard.press('ControlOrMeta+End');
    await page.keyboard.insertText('PRIVATE PENDING');
    f.expire();
    await expect(page).toHaveURL(/#\/signin$/);
    await expect(page.getByRole('heading', { name: 'Sign in', exact: true })).toBeVisible();
    async function signIn(who: string) {
      await page.getByRole('textbox', { name: 'Email', exact: true }).fill(who + '@example.com');
      await page.getByRole('button', { name: 'Continue with email', exact: true }).click();
      await page.getByRole('textbox', { name: 'Digit 1', exact: true }).fill('123456');
      await expect(page).not.toHaveURL(/signin/);
      await expect
        .poll(() => page.evaluate(() => localStorage.getItem('lykeion.workspace')))
        .toBe('lab');
      await page.evaluate(() => (location.hash = '/coresearch/p/chat'));
      await expect(editor).toBeVisible();
    }
    await signIn(person);
    if (person === 'member') {
      await expect(editor).not.toContainText('PRIVATE PENDING');
      expect(f.docs.get('main')!.text).not.toContain('PRIVATE PENDING');
      expect(
        f.writes.filter((w) => w.person === 'member' && w.path.endsWith('/steps')),
      ).toHaveLength(0);
      f.expire();
      await expect(page).toHaveURL(/signin/);
      await signIn('me');
    }
    await expect(editor).toContainText('PRIVATE PENDING');
    await expect.poll(() => f.docs.get('main')!.text).toContain('PRIVATE PENDING');
    expect(
      f.writes
        .filter(
          (w) => w.path.endsWith('/steps') && JSON.stringify(w.body).includes('PRIVATE PENDING'),
        )
        .every((w) => w.person === 'me'),
    ).toBe(true);
  });
test('Compute shows only the caller kernel when another member owns the selected chat', async ({
  page,
}) => {
  await cowriteFixture(page, false);
  await page.route('**/projects/p/chats', (r) =>
    r.fulfill({
      json: [
        {
          id: 'chat',
          owner_id: 'me',
          mode: 'cowrite',
          cli: 'claude',
          kernel_state: 'OWNER-PRIVATE-STATE',
          kernel_seen_at: '2026-09-01T01:02:03Z',
        },
        {
          id: 'member-chat',
          owner_id: 'member',
          mode: 'cowrite',
          cli: 'claude',
          kernel_state: 'CALLER-STATE',
          kernel_seen_at: '2026-09-02T04:05:06Z',
        },
      ],
    }),
  );
  await page.route('**/projects/p/compute', (r) =>
    r.fulfill({
      json: {
        kernels: [
          {
            chat_id: 'member-chat',
            kernel: 'python3',
            state: 'busy',
            runtime_id: 'r',
            device_name: 'CALLER-MACHINE',
            samples: [{ t: '2026-09-02T04:05:06Z', rss: 1048576, cpu: 50 }],
          },
        ],
        machines: [
          {
            runtime_id: 'r',
            device_name: 'CALLER-MACHINE',
            metrics: {
              mem_used: 1,
              mem_total: 2,
              cpu_used: 50,
              cores: 4,
              disk_free: 1,
              disk_total: 2,
            },
          },
        ],
      },
    }),
  );
  await page.goto('/#/coresearch/p/chat');
  await page.getByRole('button', { name: 'Compute', exact: true }).click();
  const pane = page.getByRole('tabpanel', { name: 'Compute', exact: true });
  await expect(pane).toContainText('CALLER-MACHINE');
  await expect(pane).not.toContainText('OWNER-PRIVATE-STATE');
  await expect(pane).not.toContainText('2026-09-01T01:02:03Z');
});
test('a different user without a workspace can finish sign-in and manually return to the draft owner', async ({
  page,
}) => {
  const f = await cowriteFixture(page);
  let withoutWorkspace = false;
  await page.route('**/api/workspaces', (r) =>
    withoutWorkspace ? r.fulfill({ json: [] }) : r.fallback(),
  );
  await page.goto('/#/coresearch/p/chat');
  const editor = page.locator('.cm-content');
  await expect(editor).toBeVisible();
  f.setOffline(true);
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.insertText('PRIVATE LAB-LESS DRAFT');
  f.expire();
  await expect(page).toHaveURL(/signin/);
  async function verify(who: string) {
    await page.getByRole('textbox', { name: 'Email', exact: true }).fill(who + '@example.com');
    await page.getByRole('button', { name: 'Continue with email', exact: true }).click();
    await page.getByRole('textbox', { name: 'Digit 1', exact: true }).fill('123456');
  }
  withoutWorkspace = true;
  await verify('member');
  await expect(page).toHaveURL(/onboarding\?step=workspace/);
  await expect(page.locator('body')).not.toContainText('PRIVATE LAB-LESS DRAFT');
  expect(f.writes.filter((w) => w.person === 'member' && w.path.endsWith('/steps'))).toHaveLength(
    0,
  );
  // No new 401: this verified SignIn must restore scope after the deliberate reset too.
  await page.evaluate(() => {
    location.hash = '/signin';
  });
  await expect(page.getByRole('heading', { name: 'Sign in', exact: true })).toBeVisible();
  withoutWorkspace = false;
  await verify('me');
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem('lykeion.workspace')))
    .toBe('lab');
  await page.evaluate(() => {
    location.hash = '/coresearch/p/chat';
  });
  await expect(editor).toContainText('PRIVATE LAB-LESS DRAFT');
  await expect.poll(() => f.docs.get('main')!.text).toContain('PRIVATE LAB-LESS DRAFT');
});
