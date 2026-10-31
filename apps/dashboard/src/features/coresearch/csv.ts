/**
 * A tiny, dependency-free delimited-text parser for the TableChart viewer.
 * Handles the common cases: CRLF/LF line endings, a chosen delimiter (comma
 * or tab), and minimally-quoted fields — `"a,b"` keeps the delimiter, `""`
 * is a literal quote, and a quoted field may span newlines.
 */

/** Parse `text` into a grid of rows × cells for the given `delimiter`. */
export function parseDelimited(text: string, delimiter: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let field = '';
  let inQuotes = false;
  let started = false;

  const pushField = () => {
    row.push(field);
    field = '';
  };
  const pushRow = () => {
    pushField();
    rows.push(row);
    row = [];
  };

  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    started = true;
    if (inQuotes) {
      if (ch === '"') {
        if (text[i + 1] === '"') {
          field += '"';
          i++;
        } else {
          inQuotes = false;
        }
      } else {
        field += ch;
      }
      continue;
    }
    if (ch === '"') {
      inQuotes = true;
    } else if (ch === delimiter) {
      pushField();
    } else if (ch === '\n') {
      pushRow();
    } else if (ch === '\r') {
      // Swallow CR; a following LF ends the row (CRLF), a lone CR ends it too.
      if (text[i + 1] !== '\n') pushRow();
    } else {
      field += ch;
    }
  }
  // Flush a trailing field/row unless the input ended exactly on a newline.
  if (started && (field.length > 0 || row.length > 0)) pushRow();

  return rows;
}

/**
 * Indices of columns whose every non-empty data cell parses as a finite
 * number — the chartable columns. `dataRows` excludes the header.
 */
export function numericColumns(header: string[], dataRows: string[][]): number[] {
  const out: number[] = [];
  for (let c = 0; c < header.length; c++) {
    let sawValue = false;
    let allNumeric = true;
    for (const r of dataRows) {
      const cell = (r[c] ?? '').trim();
      if (cell === '') continue;
      sawValue = true;
      if (!Number.isFinite(Number(cell))) {
        allNumeric = false;
        break;
      }
    }
    if (sawValue && allNumeric) out.push(c);
  }
  return out;
}

/** What a column holds, for the badge the table draws beside its name. */
export type ColumnType = 'number' | 'text';

/**
 * Per-column types, one entry per header cell.
 *
 * A column is `"number"` on exactly the rule `numericColumns` uses — every
 * non-empty data cell parses finite — so the badge and the chart can never
 * disagree about what is numeric. Deliberately NOT used for alignment: every
 * column reads left-aligned (see `viewers.css`), and this only labels.
 */
export function columnTypes(header: string[], dataRows: string[][]): ColumnType[] {
  const numeric = new Set(numericColumns(header, dataRows));
  return header.map((_, c) => (numeric.has(c) ? 'number' : 'text'));
}
