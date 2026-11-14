import { expect, it } from 'vitest';
import { safeOfficeXml } from './office-xml-limits';

it('allows ordinary XML, quoted angle brackets, comments and CDATA', () => {
  expect(
    safeOfficeXml(
      '<?xml version="1.0"?><a attr=">">one<!-- <ignored> --><![CDATA[<data>]]><b/></a>',
    ),
  ).toBe(true);
});
it('rejects excessive nesting, nodes, size and unsafe declarations', () => {
  expect(safeOfficeXml('<a>'.repeat(65) + '</a>'.repeat(65))).toBe(false);
  expect(safeOfficeXml('<a>' + '<b/>'.repeat(10001) + '</a>')).toBe(false);
  expect(safeOfficeXml('<a>' + 'x'.repeat(1024 * 1024) + '</a>')).toBe(false);
  expect(safeOfficeXml('<!DOCTYPE a [<!ENTITY x "hi">]><a>&x;</a>')).toBe(false);
});
it('rejects incomplete markup without scanning beyond input', () => {
  expect(safeOfficeXml('<a x="unterminated>')).toBe(false);
  expect(safeOfficeXml('<!-- unterminated')).toBe(false);
  expect(safeOfficeXml('</a>')).toBe(false);
});
