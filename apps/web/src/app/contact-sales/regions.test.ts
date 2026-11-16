import { expect, it } from 'vitest';
import { regions } from './regions';

it('uses stable country labels with no duplicate codes or names', () => {
  expect(regions.length).toBe(249);
  expect(new Set(regions.map(region => region.code)).size).toBe(249);
  expect(new Set(regions.map(region => region.name)).size).toBe(249);
  expect(regions.find(region => region.code === 'FK')?.name).toBe('Falkland Islands');
});
