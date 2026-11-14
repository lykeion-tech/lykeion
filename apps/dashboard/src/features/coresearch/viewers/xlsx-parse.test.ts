import { expect, it } from 'vitest';
import { cellCoordinate, worksheetTarget } from './xlsx-parse';
it('checks workbook relationship targets without following external paths', () => {
  expect(worksheetTarget('worksheets/sheet2.xml')).toBe('xl/worksheets/sheet2.xml');
  expect(worksheetTarget('/xl/worksheets/sheet2.xml')).toBe('xl/worksheets/sheet2.xml');
  expect(worksheetTarget('../other.xml')).toBeUndefined();
  expect(worksheetTarget('https://remote/sheet1.xml')).toBeUndefined();
});
it('parses bounded spreadsheet coordinates without accepting invalid references', () => {
  expect(cellCoordinate('AA3')).toEqual({ row: 2, column: 26 });
  expect(cellCoordinate('XFD1048576')).toEqual({ row: 1048575, column: 16383 });
  for (const invalid of ['A0', 'XFE1', 'A1048577', 'A1:A2', 'A1x'])
    expect(cellCoordinate(invalid)).toBeUndefined();
});
