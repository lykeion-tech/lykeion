type Output =
  | { kind: 'text'; text: string }
  | { kind: 'image'; src: string }
  | { kind: 'table'; rows: string[][] };
type Cell = { type: 'markdown' | 'code' | 'raw'; source: string; outputs: Output[] };

const asText = (v: unknown): string =>
  typeof v === 'string'
    ? v
    : Array.isArray(v)
      ? v.filter((x) => typeof x === 'string').join('')
      : '';

function htmlTable(html: string): string[][] {
  if (html.length > 250_000 || !/<table\b/i.test(html)) return [];
  const rows: string[][] = [];
  const rowOpen = /<tr\b[^<>]*>/gi,
    rowClose = /<\/tr\s*>/gi;
  let start: RegExpExecArray | null;
  while (rows.length < 100 && (start = rowOpen.exec(html))) {
    rowClose.lastIndex = rowOpen.lastIndex;
    const end = rowClose.exec(html);
    if (!end) break;
    const section = html.slice(rowOpen.lastIndex, end.index);
    rowOpen.lastIndex = rowClose.lastIndex;
    const cells: string[] = [];
    const cellOpen = /<t[dh]\b[^<>]*>/gi,
      cellClose = /<\/t[dh]\s*>/gi;
    while (cells.length < 100 && cellOpen.exec(section)) {
      cellClose.lastIndex = cellOpen.lastIndex;
      const close = cellClose.exec(section);
      if (!close) break;
      const value = section.slice(cellOpen.lastIndex, close.index);
      cellOpen.lastIndex = cellClose.lastIndex;
      cells.push(
        value
          .replace(/<[^<>]*>/g, '')
          .replace(
            /&(?:amp|lt|gt|quot|#39);/g,
            (entity) =>
              ({ '&amp;': '&', '&lt;': '<', '&gt;': '>', '&quot;': '"', '&#39;': "'" })[entity] ??
              entity,
          )
          .trim(),
      );
    }
    if (cells.length) rows.push(cells);
  }
  return rows;
}

export function notebookPreview(input: string): { cells: Cell[]; language: string } | undefined {
  try {
    const doc: unknown = JSON.parse(input);
    if (!doc || typeof doc !== 'object' || !Array.isArray((doc as { cells?: unknown }).cells))
      return;
    const metadata = (
      doc as {
        metadata?: { language_info?: { name?: unknown }; kernelspec?: { language?: unknown } };
      }
    ).metadata;
    const candidate = metadata?.language_info?.name ?? metadata?.kernelspec?.language;
    const language =
      typeof candidate === 'string' && /^[a-zA-Z0-9+#._-]{1,40}$/.test(candidate)
        ? candidate.toLowerCase()
        : 'text';
    const cells: Cell[] = [];
    for (const item of (doc as { cells: unknown[] }).cells.slice(0, 500)) {
      if (!item || typeof item !== 'object') continue;
      const c = item as Record<string, unknown>;
      const type = c.cell_type === 'markdown' || c.cell_type === 'code' ? c.cell_type : 'raw';
      const outputs: Output[] = [];
      if (Array.isArray(c.outputs))
        for (const raw of c.outputs.slice(0, 100)) {
          if (!raw || typeof raw !== 'object') continue;
          const output = raw as Record<string, unknown>;
          const data =
            output.data && typeof output.data === 'object'
              ? (output.data as Record<string, unknown>)
              : {};
          const raster = ['image/png', 'image/jpeg', 'image/gif'].find((mime) => {
            const value = asText(data[mime]).replace(/\s/g, '');
            return (
              value.length > 0 && value.length <= 4_000_000 && /^[a-zA-Z0-9+/]*={0,2}$/.test(value)
            );
          });
          if (raster)
            outputs.push({
              kind: 'image',
              src: `data:${raster};base64,${asText(data[raster]).replace(/\s/g, '')}`,
            });
          else {
            const rows = htmlTable(asText(data['text/html']));
            if (rows.length) {
              outputs.push({ kind: 'table', rows });
              continue;
            }
            const text =
              asText(output.traceback) ||
              asText(output.text) ||
              asText(data['text/plain']) ||
              asText(data['text/html']);
            if (text) outputs.push({ kind: 'text', text });
          }
        }
      cells.push({ type, source: asText(c.source), outputs });
    }
    return { cells, language };
  } catch {
    return;
  }
}
