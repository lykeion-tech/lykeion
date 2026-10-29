import { expect, test } from '@playwright/test';
import { cowriteFixture } from './cowrite-editor-fixture';
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
