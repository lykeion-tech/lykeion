import { extractNpz, listZip } from './npz-preview';
import { safeOfficeXml } from './office-xml-limits';
export interface WorkbookParts {
  workbook: string;
  relationships: string;
  sheets: Record<string, string>;
  sharedStrings?: string;
}
export async function readWorkbookArchive(blob: Blob): Promise<WorkbookParts | undefined> {
  const entries = await listZip(blob, 512);
  if (!entries) return;
  const wanted = entries.filter(
    ({ name }) =>
      name === 'xl/workbook.xml' ||
      name === 'xl/_rels/workbook.xml.rels' ||
      name === 'xl/sharedStrings.xml' ||
      /^xl\/worksheets\/sheet[1-9]\d*\.xml$/.test(name),
  );
  if (
    !wanted.length ||
    wanted.length > 103 ||
    wanted.some((entry) => entry.size > 1024 * 1024) ||
    wanted.reduce((sum, entry) => sum + entry.size, 0) > 4 * 1024 * 1024
  )
    return;
  const parts: Record<string, string> = Object.create(null);
  try {
    for (const entry of wanted) {
      const content = await extractNpz(blob, entry);
      if (!content) return;
      const xml = new TextDecoder('utf-8', { fatal: true }).decode(await content.arrayBuffer());
      if (!safeOfficeXml(xml)) return;
      parts[entry.name] = xml;
    }
  } catch {
    return;
  }
  if (!parts['xl/workbook.xml'] || !parts['xl/_rels/workbook.xml.rels']) return;
  const sheets: Record<string, string> = Object.create(null);
  for (const [name, xml] of Object.entries(parts))
    if (/^xl\/worksheets\/sheet[1-9]\d*\.xml$/.test(name)) sheets[name] = xml;
  return {
    workbook: parts['xl/workbook.xml'],
    relationships: parts['xl/_rels/workbook.xml.rels'],
    sheets,
    sharedStrings: parts['xl/sharedStrings.xml'],
  };
}
