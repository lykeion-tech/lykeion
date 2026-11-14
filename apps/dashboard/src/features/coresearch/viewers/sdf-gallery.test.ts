import { readFileSync } from 'node:fs';
import { expect, it } from 'vitest';
import { sdfGallery } from './sdf-gallery';

it('keeps separately generated molecules in file order with their own geometry', () => {
  // Synthetic SDF produced from ethanol and cyclohexane with RDKit 2026.9.2.
  const text = readFileSync(
    'apps/dashboard/src/features/coresearch/viewers/fixtures/synthetic-two-molecules.sdf',
    'utf8',
  );
  const result = sdfGallery(text, false);
  expect(result?.total).toBe(2);
  expect(result?.items.map((item) => [item.name, item.input.atoms])).toEqual([
    ['Synthetic ethanol', 3],
    ['Synthetic cyclohexane', 6],
  ]);
  expect(result?.items[0]?.input.text).not.toContain('Synthetic cyclohexane');
});
it('bounds selection, labels omitted records, and rejects incomplete or malformed data', () => {
  const source = readFileSync(
    'apps/dashboard/src/features/coresearch/viewers/fixtures/synthetic-two-molecules.sdf',
    'utf8',
  );
  const result = sdfGallery(source.repeat(9), false);
  expect(result?.total).toBe(18);
  expect(result?.items).toHaveLength(16);
  expect(sdfGallery(source, true)).toBeUndefined();
  expect(sdfGallery(source.slice(0, -6), false)).toBeUndefined();
  expect(sdfGallery(source.replace('V2000', 'V3000'), false)).toBeUndefined();
  expect(sdfGallery(source + '\0', false)).toBeUndefined();
  expect(sdfGallery(source.repeat(129), false)).toBeUndefined();
  expect(sdfGallery(source.repeat(9) + source.replace('V2000', 'V3000'), false)).toBeUndefined();
});
