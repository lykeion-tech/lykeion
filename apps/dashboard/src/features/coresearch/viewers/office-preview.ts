import { extractNpz, listZip } from './npz-preview';
const MAX_PART = 1024 * 1024;
const MAX_TOTAL = 4 * 1024 * 1024;
export interface OfficeParts {
  document?: string;
  presentation?: string;
  relationships?: string;
  slides?: Record<string, string>;
}
export async function readOfficeArchive(
  blob: Blob,
  format: 'docx' | 'pptx',
): Promise<OfficeParts | undefined> {
  const entries = await listZip(blob, 512);
  if (!entries) return;
  const selected = entries.filter(({ name }) =>
    format === 'docx'
      ? name === 'word/document.xml'
      : name === 'ppt/presentation.xml' ||
        name === 'ppt/_rels/presentation.xml.rels' ||
        /^ppt\/slides\/slide[1-9]\d*\.xml$/.test(name),
  );
  if (
    !selected.length ||
    selected.length > 102 ||
    selected.some(({ size }) => size > MAX_PART) ||
    selected.reduce((sum, entry) => sum + entry.size, 0) > MAX_TOTAL
  )
    return;
  const parts: Record<string, string> = Object.create(null);
  try {
    for (const entry of selected) {
      const content = await extractNpz(blob, entry);
      if (!content) return;
      const xml = new TextDecoder('utf-8', { fatal: true }).decode(await content.arrayBuffer());
      if (/<!\s*(?:DOCTYPE|ENTITY)\b/i.test(xml)) return;
      parts[entry.name] = xml;
    }
  } catch {
    return;
  }
  if (format === 'docx')
    return parts['word/document.xml'] ? { document: parts['word/document.xml'] } : undefined;
  const presentation = parts['ppt/presentation.xml'],
    relationships = parts['ppt/_rels/presentation.xml.rels'];
  if (!presentation || !relationships) return;
  const slides: Record<string, string> = Object.create(null);
  for (const [name, xml] of Object.entries(parts))
    if (/^ppt\/slides\/slide[1-9]\d*\.xml$/.test(name)) slides[name] = xml;
  return { presentation, relationships, slides };
}
