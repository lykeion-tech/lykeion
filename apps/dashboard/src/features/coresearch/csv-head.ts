import { columnTypes, parseDelimited } from './csv';

/** At or below this, the whole file is fetched and behaves exactly as it
 *  always has, charting included.
 *
 *  Deliberately the same number as `PREVIEW_MAX_BYTES`. It could have been
 *  chosen from what a browser parses without stuttering, which would put it
 *  higher — but 8 MiB is the boundary a researcher has already met, because
 *  it is the size at which a table used to say "too large to show". Keeping
 *  the number means the transport changed and the boundary did not. */
export const TABLE_FULL_MAX_BYTES = 8 * 1024 * 1024;

/** The window a larger file is read through: hundreds of rows of any
 *  realistic CSV, and small enough that opening one is not a download. */
export const TABLE_HEAD_BYTES = 256 * 1024;

export interface TableData {
  header: string[];
  rows: string[][];
  /** Per-column type badges, as display labels — `"number"`/`"text"` from a
   *  delimited file, the declared Parquet type from a Parquet one. A union
   *  would have to grow every time a format names a type differently, and the
   *  badge only ever prints them.
   *
   *  ABSENT on a partial read: `columnTypes` claims
   *  every non-empty cell in a column parses finite, and a head can only
   *  prove that of the head. A badge that quietly meant something narrower
   *  than it says is worse than no badge. */
  types?: readonly string[];
  /** Rows in the whole file, when the format states one. ABSENT for a
   *  truncated delimited read, which knows a byte count and nothing else —
   *  estimating rows from bytes is a guess wearing a number's clothes. */
  totalRows?: number;
  /** True when bytes remain past what was read. Gates charting. */
  partial: boolean;
}

/**
 * A delimited head, cut safely.
 *
 * The final record of a partial read is DROPPED. A cut can land mid-record,
 * and worse, mid-quoted-field — `parseDelimited` lets a quoted field span
 * newlines, so an unterminated quote absorbs every remaining byte into one
 * field. Because the parser runs left to right, that damage cannot reach
 * back: everything before the opening quote is already correct. One record
 * is therefore the exact cut. It costs a complete row whenever the read
 * happened to end on a record boundary, which is the cheaper mistake.
 */
export function tableFromDelimited(text: string, delimiter: string, partial: boolean): TableData {
  const grid = parseDelimited(text, delimiter);
  const header = grid[0] ?? [];
  let rows = grid.slice(1);
  if (partial && rows.length > 0) rows = rows.slice(0, -1);
  return {
    header,
    rows,
    ...(partial ? {} : { types: columnTypes(header, rows) }),
    partial,
  };
}
