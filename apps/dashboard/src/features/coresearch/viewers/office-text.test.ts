import { expect, it } from 'vitest';
import { paragraphText } from './office-text';

const ns = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main';
const element = (name: string, text = '', children: Element[] = []): Element =>
  ({
    nodeType: 1,
    namespaceURI: ns,
    localName: name,
    textContent: text,
    childNodes: children,
  }) as unknown as Element;
it('preserves tabs and line breaks in document order', () => {
  const paragraph = element('p', '', [
    element('r', '', [
      element('t', 'First'),
      element('tab'),
      element('t', 'Second'),
      element('br'),
      element('t', 'Third'),
      element('cr'),
    ]),
  ]);
  expect(paragraphText(paragraph, ns)).toBe('First\tSecond\nThird\n');
});
it('rejects nested paragraphs that could otherwise produce quadratic traversal', () => {
  expect(paragraphText(element('p', '', [element('p')]), ns)).toBeUndefined();
});
