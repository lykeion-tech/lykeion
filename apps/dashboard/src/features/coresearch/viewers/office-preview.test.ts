import { readFileSync } from 'node:fs';
import { expect, it } from 'vitest';
import { readOfficeArchive } from './office-preview';

const fixture = (name: string) =>
  new Blob([readFileSync(`apps/dashboard/src/features/coresearch/viewers/fixtures/${name}`)]);
it('reads the relevant XML from real Word and PowerPoint documents', async () => {
  const word = await readOfficeArchive(fixture('sample.docx'), 'docx');
  expect(word?.document).toContain('Example document');
  expect(word?.document).toContain('Cell text');
  const slides = await readOfficeArchive(fixture('sample.pptx'), 'pptx');
  expect(slides?.presentation).toContain('sldId');
  expect(slides?.relationships).toContain('slide1.xml');
  expect(slides?.slides?.['ppt/slides/slide1.xml']).toContain('First slide');
});
it('rejects broken or unsupported office archives', async () => {
  expect(await readOfficeArchive(new Blob(['not a zip']), 'docx')).toBeUndefined();
  expect(await readOfficeArchive(fixture('sample.docx'), 'pptx')).toBeUndefined();
  expect(await readOfficeArchive(fixture('unsafe.docx'), 'docx')).toBeUndefined();
  expect(await readOfficeArchive(fixture('oversized.docx'), 'docx')).toBeUndefined();
});
