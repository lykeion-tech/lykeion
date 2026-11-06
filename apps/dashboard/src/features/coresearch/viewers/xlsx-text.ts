import { safeOfficeXml } from './office-xml-limits';
import { cellCoordinate, worksheetTarget } from './xlsx-parse';
import type { WorkbookParts } from './xlsx-preview';
const MAIN = 'http://schemas.openxmlformats.org/spreadsheetml/2006/main';
const REL = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships';
const PACKAGE_REL = 'http://schemas.openxmlformats.org/package/2006/relationships';
const MAX_ROWS = 50,
  MAX_COLS = 30;
function parse(xml: string, namespace: string, root: string): Document | undefined {
  if (!safeOfficeXml(xml)) return;
  const doc = new DOMParser().parseFromString(xml, 'application/xml');
  if (
    doc.getElementsByTagName('parsererror').length ||
    doc.documentElement.namespaceURI !== namespace ||
    doc.documentElement.localName !== root
  )
    return;
  return doc;
}
export interface WorkbookSheet {
  name: string;
  path: string;
  hidden: boolean;
}
export function workbookSheets(parts: WorkbookParts): WorkbookSheet[] | undefined {
  try {
    const workbook = parse(parts.workbook, MAIN, 'workbook'),
      relationships = parse(parts.relationships, PACKAGE_REL, 'Relationships');
    if (!workbook || !relationships) return;
    const rels = new Map<string, { path?: string; external: boolean }>();
    for (const rel of relationships.getElementsByTagNameNS(PACKAGE_REL, 'Relationship')) {
      const id = rel.getAttribute('Id') ?? '';
      if (!id || rels.has(id)) return;
      const type = rel.getAttribute('Type') ?? '';
      rels.set(id, {
        path:
          type === `${REL}/worksheet`
            ? worksheetTarget(rel.getAttribute('Target') ?? '')
            : undefined,
        external: rel.getAttribute('TargetMode') === 'External',
      });
    }
    const nodes = workbook.getElementsByTagNameNS(MAIN, 'sheet');
    if (nodes.length > 100) return;
    const paths = new Set<string>(),
      result: WorkbookSheet[] = [];
    for (const node of nodes) {
      const rel = rels.get(node.getAttributeNS(REL, 'id') ?? ''),
        name = node.getAttribute('name') ?? '';
      if (
        !rel?.path ||
        rel.external ||
        paths.has(rel.path) ||
        !parts.sheets[rel.path] ||
        !name ||
        name.length > 256
      )
        return;
      paths.add(rel.path);
      result.push({
        name,
        path: rel.path,
        hidden: (node.getAttribute('state') ?? 'visible') !== 'visible',
      });
    }
    return result;
  } catch {
    return;
  }
}
export interface WorkbookCell {
  text: string;
  formula: boolean;
  missing: boolean;
}
export interface WorkbookSample {
  rows: (WorkbookCell | null)[][];
  truncated: boolean;
}
export function boundedSpreadsheetText(
  root: Element,
): { text: string; truncated: boolean } | undefined {
  const pending: Node[] = Array.from(root.childNodes).reverse();
  let text = '',
    truncated = false;
  while (pending.length) {
    const node = pending.pop()!;
    if (node.nodeType !== 1) continue;
    const element = node as Element;
    if (element.namespaceURI === MAIN && element.localName === 'rPh') continue;
    if (element.namespaceURI === MAIN && element.localName === 't') {
      if (element.childElementCount > 0) return;
      const value = element.textContent ?? '';
      const remaining = 2000 - text.length;
      if (value.length > remaining) truncated = true;
      text += value.slice(0, remaining);
      continue;
    }
    for (let i = element.childNodes.length - 1; i >= 0; i--) pending.push(element.childNodes[i]!);
  }
  return { text: truncated ? `${text}…` : text, truncated };
}
export function workbookCell(
  type: string,
  present: boolean,
  value: string,
  shared: string[],
  formula: boolean,
  inline?: string,
): WorkbookCell | undefined {
  const missing = formula && (!present || (value === '' && type !== 'str'));
  if (missing) return { text: '', formula, missing: true };
  let text = value;
  if (type === 's') {
    if (
      !/^\d+$/.test(value) ||
      !Number.isSafeInteger(Number(value)) ||
      Number(value) >= shared.length
    )
      return;
    text = shared[Number(value)]!;
  } else if (type === 'inlineStr') {
    if (inline === undefined) return;
    text = inline;
  } else if (type === 'b') {
    if (!['0', '1'].includes(value)) return;
    text = value === '1' ? 'TRUE' : 'FALSE';
  } else if (!['n', 'str', 'e', 'd'].includes(type)) return;
  return { text, formula, missing: false };
}
export function workbookSample(
  parts: WorkbookParts,
  sheet: WorkbookSheet,
): WorkbookSample | undefined {
  try {
    const source = parts.sheets[sheet.path];
    if (!source) return;
    const doc = parse(source, MAIN, 'worksheet');
    if (!doc) return;
    let shared: string[] = [],
      sharedTruncated = false;
    if (parts.sharedStrings) {
      const strings = parse(parts.sharedStrings, MAIN, 'sst');
      if (!strings) return;
      const entries = strings.getElementsByTagNameNS(MAIN, 'si');
      if (entries.length > 5000) return;
      for (const entry of entries) if (entry.getElementsByTagNameNS(MAIN, 'si').length) return;
      for (const entry of entries) {
        const value = boundedSpreadsheetText(entry);
        if (!value) return;
        shared.push(value.text);
        sharedTruncated ||= value.truncated;
      }
    }
    const cells = doc.getElementsByTagNameNS(MAIN, 'c');
    const grid = new Map<string, WorkbookCell>();
    const seen = new Set<string>();
    let rowCount = 0,
      colCount = 0,
      truncated = sharedTruncated;
    for (const cell of cells) {
      if (cell.getElementsByTagNameNS(MAIN, 'c').length) return;
      const coordinate = cellCoordinate(cell.getAttribute('r') ?? '');
      if (!coordinate) return;
      const key = `${coordinate.row}:${coordinate.column}`;
      if (seen.has(key)) return;
      seen.add(key);
      if (coordinate.row >= MAX_ROWS || coordinate.column >= MAX_COLS) {
        truncated = true;
        continue;
      }
      const valueNode = cell.getElementsByTagNameNS(MAIN, 'v')[0];
      if (valueNode && valueNode.childElementCount > 0) return;
      const value = valueNode?.textContent ?? '';
      const type = cell.getAttribute('t') ?? 'n';
      const formula = cell.getElementsByTagNameNS(MAIN, 'f').length > 0;
      const inline = type === 'inlineStr' ? boundedSpreadsheetText(cell) : undefined;
      if (type === 'inlineStr' && !inline) return;
      truncated ||= inline?.truncated ?? false;
      const parsed = workbookCell(type, !!valueNode, value, shared, formula, inline?.text);
      if (!parsed) return;
      if (parsed.text.length > 2000 && !inline?.truncated && type !== 's') {
        parsed.text = `${parsed.text.slice(0, 2000)}…`;
        truncated = true;
      }
      grid.set(key, parsed);
      rowCount = Math.max(rowCount, coordinate.row + 1);
      colCount = Math.max(colCount, coordinate.column + 1);
    }
    const rows = Array.from({ length: rowCount }, (_, row) =>
      Array.from({ length: colCount }, (_, col) => grid.get(`${row}:${col}`) ?? null),
    );
    return { rows, truncated };
  } catch {
    return;
  }
}
