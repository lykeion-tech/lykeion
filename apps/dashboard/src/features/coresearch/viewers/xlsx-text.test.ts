import { expect, it } from 'vitest';
import { boundedSpreadsheetText, workbookCell } from './xlsx-text';
const ns = 'http://schemas.openxmlformats.org/spreadsheetml/2006/main';
const element = (name: string, text = '', children: Element[] = []): Element =>
  ({
    nodeType: 1,
    namespaceURI: ns,
    localName: name,
    textContent: text,
    childNodes: children,
    childElementCount: children.length,
  }) as unknown as Element;
it('bounds rich string text, ignores phonetic runs and rejects nested text elements', () => {
  expect(
    boundedSpreadsheetText(
      element('si', '', [
        element('t', 'Word'),
        element('rPh', '', [element('t', 'phonetic')]),
        element('t', ' end'),
      ]),
    ),
  ).toEqual({ text: 'Word end', truncated: false });
  expect(
    boundedSpreadsheetText(element('si', '', [element('t', 'ignored', [element('t', 'nested')])])),
  ).toBeUndefined();
  expect(
    boundedSpreadsheetText(
      element('si', '', [element('t', 'a'.repeat(1999)), element('t', 'xyz')]),
    ),
  ).toEqual({ text: `${'a'.repeat(1999)}x…`, truncated: true });
});
it('handles absent cached values before shared and boolean type validation', () => {
  expect(workbookCell('b', false, '', [], true)).toEqual({
    text: '',
    formula: true,
    missing: true,
  });
  expect(workbookCell('s', false, '', [], true)).toEqual({
    text: '',
    formula: true,
    missing: true,
  });
  expect(workbookCell('str', true, '', [], true)).toEqual({
    text: '',
    formula: true,
    missing: false,
  });
  expect(workbookCell('str', false, '', [], true)).toEqual({
    text: '',
    formula: true,
    missing: true,
  });
});
