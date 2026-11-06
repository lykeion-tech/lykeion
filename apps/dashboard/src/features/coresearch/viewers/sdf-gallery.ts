import { chemicalInput } from './chemical-guard';
const MAX_TEXT = 1024 * 1024;
const MAX_RECORDS = 256;
const DISPLAY_RECORDS = 16;
export interface SdfGallery {
  items: { name: string; input: { text: string; format: string; atoms: number } }[];
  total: number;
}
export function sdfGallery(text: string, partial: boolean): SdfGallery | undefined {
  if (partial || text.length > MAX_TEXT || text.includes('\0')) return;
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  if (lines.length > 20000) return;
  const items: SdfGallery['items'] = [];
  let total = 0;
  let start = 0;
  for (let i = 0; i < lines.length; i++) {
    if (lines[i]!.trim() !== '$$$$') continue;
    if (++total > MAX_RECORDS || i === start) return;
    const record = lines.slice(start, i).join('\n');
    const input = chemicalInput(`${record}\n$$$$\n`, 'sdf', false);
    if (!input) return;
    if (items.length < DISPLAY_RECORDS) {
      const title = lines[start]!.trim();
      if (/[\x00-\x1f\x7f]/.test(title)) return;
      items.push({ name: title.slice(0, 120) || `Molecule ${total}`, input });
    }
    start = i + 1;
  }
  if (!total || lines.slice(start).some((line) => line.trim())) return;
  return { items, total };
}
