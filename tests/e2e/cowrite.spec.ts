import { expect, test } from '@playwright/test';
import { cowriteFixture } from './cowrite-editor-fixture';
import { installAristotleFixture } from './aristotle-fixture';
function pdf() {
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>',
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ];
  const content = 'BT /F1 18 Tf 50 700 Td (A real Cowrite PDF) Tj ET';
  objects.push(`<< /Length ${content.length} >>\nstream\n${content}\nendstream`);
  let s = '%PDF-1.4\n';
  const offsets = [0];
  objects.forEach((o, i) => {
    offsets.push(Buffer.byteLength(s));
    s += `${i + 1} 0 obj\n${o}\nendobj\n`;
  });
  const start = Buffer.byteLength(s);
  s += `xref\n0 6\n0000000000 65535 f \n${offsets
    .slice(1)
    .map((n) => String(n).padStart(10, '0') + ' 00000 n \n')
    .join('')}trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n${start}\n%%EOF`;
  return Buffer.from(s);
}
test('Cowrite edits, keeps an agent hunk, compiles actual PDF bytes and opens Versions', async ({
  page,
}) => {
  const f = await cowriteFixture(page);
  let built = false;
  let buildSource = '';
  const build = {
    id: 'build-1',
    project_id: 'p',
    seq: 1,
    requested_by: 'me',
    status: 'ok',
    sources: [{ file_id: 'main', path: 'main.tex', n: 1, sha256: 'sha', size: 30 }],
    has_pdf: true,
    has_log: true,
    has_synctex: false,
    pages: 1,
    created_at: '2026-10-28T10:00:00Z',
  };
  await page.route('**/api/coresearch/projects/p/builds', async (r) => {
    if (r.request().method() === 'POST') {
      built = true;
      buildSource = f.docs.get('main')!.text;
      await r.fulfill({ json: build });
    } else await r.fulfill({ json: built ? [build] : [] });
  });
  await page.route('**/api/coresearch/projects/p/builds/1.pdf', (r) =>
    r.fulfill({ contentType: 'application/pdf', body: pdf() }),
  );
  await page.route('**/api/coresearch/projects/p/builds/1.log', (r) =>
    r.fulfill({ body: 'Output written on main.pdf (1 page).' }),
  );
  await page.route('**/api/coresearch/projects/p/versions', (r) =>
    r.fulfill({
      json: built
        ? [
            {
              id: 'v1',
              seq: 1,
              title: 'Build #1',
              by_user_id: 'me',
              created_at: '2026-10-28T10:00:00Z',
              files: [{ file_id: 'main', path: 'main.tex', n: 1 }],
            },
          ]
        : [],
    }),
  );
  await page.goto('/#/coresearch/p/chat');
  const editor = page.locator('.cm-content');
  await expect(editor).toBeVisible();
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.insertText('Typed before compile.');
  await page.locator('.cm-settle-keep').click();
  await expect
    .poll(() => f.writes.some((w) => w.path.endsWith('/changes/pending/keep')))
    .toBe(true);
  await page.getByRole('button', { name: 'Compile', exact: true }).click();
  await expect.poll(() => buildSource).toContain('Typed before compile.');
  await expect(page.locator('.pdf-canvas')).toBeVisible();
  await expect(page.locator('.pdf-text-layer')).toContainText('A real Cowrite PDF');
  await expect(page.getByRole('textbox', { name: 'Page, of 1' })).toHaveValue('01');
  await page.getByRole('button', { name: 'Tools', exact: true }).click();
  await page.getByRole('tab', { name: 'Versions', exact: true }).click();
  await expect(page.getByTestId('manuscript-version')).toContainText('v1');
  await expect(page.getByTestId('manuscript-version')).toContainText('Ada Lovelace');
  await page.getByRole('button', { name: 'Close panel' }).click();
  await expect(page.locator('.pdf-text-layer')).toContainText('A real Cowrite PDF');
});
test('Outline follows the actual cursor and completions read other paper files', async ({
  page,
}) => {
  const f = await cowriteFixture(page);
  f.docs.get('main')!.text = '\\section{First}\nText\n\\section{Second}\nMore text\n';
  f.docs.get('intro')!.text = '\\label{sec:evidence}\nEvidence';
  await page.goto('/#/coresearch/p/chat');
  const editor = page.locator('.cm-content');
  await expect(editor).toBeVisible();
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  await expect(page.getByRole('button', { name: 'Second', exact: true })).toHaveClass(/is-current/);
  await page.keyboard.insertText('\\cite{te');
  await page.keyboard.press('Control+Space');
  await expect(page.locator('.cm-tooltip-autocomplete')).toContainText('test');
});
test('Compile with no machine of the person online shows the no-machine state', async ({
  page,
}) => {
  await cowriteFixture(page);
  await page.route('**/api/coresearch/projects/p/builds', (r) =>
    r.request().method() === 'POST'
      ? r.fulfill({ status: 409, json: { error: 'no machine online', code: 'no_machine' } })
      : r.fulfill({ json: [] }),
  );
  await page.goto('/#/coresearch/p/chat');
  await page.getByRole('button', { name: 'Compile', exact: true }).click();
  await expect(page.getByText(/no machine online/)).toBeVisible();
  await expect(page.getByRole('button', { name: 'Compile', exact: true })).toBeEnabled();
});
test('the selection toolbar opens for every new selection made by double-click', async ({
  page,
}) => {
  await cowriteFixture(page);
  await page.goto('/#/coresearch/p/chat');
  const editor = page.locator('.cm-content');
  await expect(editor).toContainText('paragraph');
  for (const word of ['Introduction', 'shared', 'paragraph']) {
    const box = await editor.evaluate((root, word) => {
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      for (let node = walker.nextNode(); node; node = walker.nextNode()) {
        const at = node.textContent!.indexOf(word);
        if (at < 0) continue;
        const range = document.createRange();
        range.setStart(node, at);
        range.setEnd(node, at + word.length);
        const r = range.getBoundingClientRect();
        return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
      }
      throw new Error(`no ${word}`);
    }, word);
    await page.mouse.dblclick(box.x, box.y);
    await expect.poll(() => page.evaluate(() => window.getSelection()?.toString())).toBe(word);
    const bar = page.getByRole('toolbar', { name: 'Ask about the passage' });
    for (const name of ['Proofread', 'Comment', 'Edit with AI'])
      await expect(bar.getByRole('button', { name, exact: true })).toBeVisible();
  }
});
test('the session pill names the model by the label its runtime reported', async ({ page }) => {
  await cowriteFixture(page);
  let models = [{ value: 'sonnet', label: 'Sonnet 5.5' }];
  await page.route('**/api/runtimes', (r) =>
    r.fulfill({
      json: [{ id: 'own', owner_id: 'me', harness: 'claude', status: 'online', models }],
    }),
  );
  await page.route('**/api/coresearch/conversations', (r) =>
    r.fulfill({
      json: [
        {
          id: 'paper',
          owner_id: 'me',
          workspace_id: 'lab',
          project_id: 'p',
          chat_id: 'chat',
          title: 'Attention paper',
          snapshot_harness: 'claude',
          snapshot_model: 'sonnet',
        },
      ],
    }),
  );
  await page.goto('/#/coresearch/p/chat');
  await expect(page.getByRole('button', { name: 'Model: Sonnet 5.5', exact: true })).toBeVisible();
  models = [];
  await page.reload();
  await expect(page.getByRole('button', { name: 'Model: sonnet', exact: true })).toBeVisible();
});
test('the model menu stays inside a 900 px viewport', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await installAristotleFixture(page, true);
  const models = Array.from({ length: 13 }, (_, i) => ({
    value: `m${i}`,
    label: i ? `Model ${i}` : 'Default (recommended)',
    description: 'A model the CLI reported',
  }));
  await page.route('**/api/runtimes', (r) =>
    r.fulfill({
      json: [{ id: 'own', owner_id: 'me', harness: 'claude', status: 'online', models }],
    }),
  );
  await page.goto('/#/coresearch/new');
  await page
    .getByRole('button', {
      name: 'CLI and model: Claude Code · Default',
      exact: true,
    })
    .click();
  const menu = page.getByRole('dialog', { name: 'CLI and model', exact: true });
  await expect(menu).toBeVisible();
  const box = (await menu.boundingBox())!;
  expect(box.y).toBeGreaterThanOrEqual(0);
  expect(box.y + box.height).toBeLessThanOrEqual(900);
});
test('the drawer card summarises the pending proposal and reviews its hunks', async ({ page }) => {
  const f = await cowriteFixture(page);
  await page.goto('/#/coresearch/p/chat');
  const card = page.getByTestId('merge-changes');
  await expect(card).toContainText('1 file edited');
  await expect(page.getByTestId('merge-changes-file-main.tex')).toHaveText(
    'main.tex+2−21 pending · 1 superseded',
  );
  await card.getByRole('button', { name: 'Review', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Review changes' });
  await expect(dialog.getByTestId('merge-hunk-pending').getByTestId('merge-hunk-old')).toHaveText(
    'An',
  );
  await expect(dialog.getByTestId('merge-hunk-stale')).toContainText('Superseded');
  await dialog.getByTestId('merge-hunk-pending').getByRole('button', { name: 'Keep' }).click();
  await expect
    .poll(() => f.writes.some((w) => w.path.endsWith('/changes/pending/keep')))
    .toBe(true);
  await expect(dialog).toBeHidden();
  await expect(card).toBeHidden();
});
test('the session rail carries the rail buttons and the session title tab, and the navigation the workspace switcher', async ({
  page,
}) => {
  await cowriteFixture(page);
  await page.goto('/#/coresearch/p/chat');
  const rail = page.getByRole('complementary', { name: 'Project sidebar' });
  await expect(rail).toBeVisible();
  await expect(page.getByRole('tablist', { name: 'Workspace tabs' }).getByRole('tab')).toHaveText([
    'Paper',
  ]);
  await page.locator('.sidebar-brand:visible [aria-current=page]').click();
  const navigation = page.getByRole('complementary', { name: 'Workspace navigation' });
  await expect(navigation).toBeVisible();
  await expect(rail).toBeHidden();
  await expect(navigation.locator('.workspace-switch')).toContainText('Fixture lab');
  await navigation.locator('.workspace-switch').click();
  await expect(page.getByRole('menu', { name: 'Workspace' })).toBeVisible();
  await page.keyboard.press('Escape');
  await page.locator('.sidebar-brand:visible [aria-current=page]').click();
  await expect(rail.getByRole('tab', { name: 'Files' })).toBeVisible();
});
