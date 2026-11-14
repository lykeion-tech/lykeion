import type { OfficeParts } from './office-preview';
import { orderSlides } from './office-order';
import { safeOfficeXml } from './office-xml-limits';
const WORD = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main';
const DRAWING = 'http://schemas.openxmlformats.org/drawingml/2006/main';
const PRESENTATION = 'http://schemas.openxmlformats.org/presentationml/2006/main';
const REL = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships';
const PACKAGE_REL = 'http://schemas.openxmlformats.org/package/2006/relationships';
function parse(xml: string): Document | undefined {
  if (!safeOfficeXml(xml)) return;
  const doc = new DOMParser().parseFromString(xml, 'application/xml');
  return doc.getElementsByTagName('parsererror').length ? undefined : doc;
}
export function paragraphText(paragraph: Element, namespace: string): string | undefined {
  const pending: Node[] = Array.from(paragraph.childNodes).reverse();
  const result: string[] = [];
  while (pending.length) {
    const node = pending.pop()!;
    if (node.nodeType !== 1) continue;
    const element = node as Element;
    if (element.namespaceURI === namespace) {
      if (element.localName === 'p') return;
      if (element.localName === 't') {
        if (element.childElementCount > 0) return;
        result.push(element.textContent ?? '');
        continue;
      }
      if (element.localName === 'tab') {
        result.push('\t');
        continue;
      }
      if (element.localName === 'br' || element.localName === 'cr') {
        result.push('\n');
        continue;
      }
    }
    for (let i = element.childNodes.length - 1; i >= 0; i--) pending.push(element.childNodes[i]!);
  }
  return result.join('');
}
function paragraphs(doc: Document, namespace: string): string[] | undefined {
  const nodes = doc.getElementsByTagNameNS(namespace, 'p');
  if (nodes.length > 5000) return;
  const result: string[] = [];
  let length = 0;
  for (const node of nodes) {
    const text = paragraphText(node, namespace);
    if (text === undefined) return;
    length += text.length;
    if (length > 200_000) return;
    if (text.trim()) result.push(text);
  }
  return result;
}
export function officeText(parts: OfficeParts, format: 'docx' | 'pptx'): string[][] | undefined {
  try {
    if (format === 'docx') {
      if (!parts.document) return;
      const doc = parse(parts.document);
      if (
        !doc ||
        doc.documentElement.namespaceURI !== WORD ||
        doc.documentElement.localName !== 'document'
      )
        return;
      const text = paragraphs(doc, WORD);
      return text ? [text] : undefined;
    }
    if (!parts.presentation || !parts.relationships || !parts.slides) return;
    const pres = parse(parts.presentation),
      rels = parse(parts.relationships);
    if (
      !pres ||
      !rels ||
      pres.documentElement.namespaceURI !== PRESENTATION ||
      rels.documentElement.namespaceURI !== PACKAGE_REL
    )
      return;
    const ids = Array.from(
      pres.getElementsByTagNameNS(PRESENTATION, 'sldId'),
      (node) => node.getAttributeNS(REL, 'id') ?? '',
    );
    if (ids.length > 100) return;
    const relationships = Array.from(
      rels.getElementsByTagNameNS(PACKAGE_REL, 'Relationship'),
      (node) => ({
        id: node.getAttribute('Id') ?? '',
        target: node.getAttribute('Target') ?? '',
        external: node.getAttribute('TargetMode') === 'External',
      }),
    );
    const paths = orderSlides(ids, relationships);
    if (!paths) return;
    const pages: string[][] = [];
    for (const path of paths) {
      const xml = parts.slides[path];
      if (!xml) return;
      const doc = parse(xml);
      if (
        !doc ||
        doc.documentElement.namespaceURI !== PRESENTATION ||
        doc.documentElement.localName !== 'sld'
      )
        return;
      const text = paragraphs(doc, DRAWING);
      if (!text) return;
      pages.push(text);
    }
    return pages;
  } catch {
    return;
  }
}
