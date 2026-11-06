import { readFileSync } from 'node:fs';
import { expect, it } from 'vitest';
import { readWorkbookArchive } from './xlsx-preview';

const fixture = (name: string) =>
  new Blob([readFileSync(`apps/dashboard/src/features/coresearch/viewers/fixtures/${name}`)]);
it('reads the relevant XML from an openpyxl workbook with multiple sheets', async () => {
  const result = await readWorkbookArchive(fixture('sample.xlsx'));
  expect(result?.workbook).toContain('First sheet');
  expect(result?.workbook).toContain('Second sheet');
  expect(result?.relationships).toContain('sheet1.xml');
  expect(result?.sheets['xl/worksheets/sheet1.xml']).toContain('Example');
  expect((await readWorkbookArchive(fixture('shared.xlsx')))?.sharedStrings).toContain(
    'shared text',
  );
});
it('rejects invalid or unrelated archives', async () => {
  expect(await readWorkbookArchive(new Blob(['not a zip']))).toBeUndefined();
  expect(await readWorkbookArchive(fixture('sample.docx'))).toBeUndefined();
  expect(await readWorkbookArchive(fixture('unsafe.docx'))).toBeUndefined();
  expect(await readWorkbookArchive(fixture('unsafe.xlsx'))).toBeUndefined();
  expect(await readWorkbookArchive(fixture('oversized.xlsx'))).toBeUndefined();
});
