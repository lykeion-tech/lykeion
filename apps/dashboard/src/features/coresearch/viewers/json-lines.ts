export function plotFraction(value: number, min: number, max: number) {
  const span = max - min;
  if (span === 0) return 0.5;
  return Number.isFinite(span) ? (value - min) / span : (value / 2 - min / 2) / (max / 2 - min / 2);
}

function inspectNumberLimits(line: string) {
  let depth = 0,
    inString = false,
    escaped = false,
    underflow = false;
  const number = /-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/y;
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (inString) {
      if (escaped) escaped = false;
      else if (ch === '\\') escaped = true;
      else if (ch === '"') inString = false;
    } else if (ch === '"') inString = true;
    else if (ch === '[' || ch === '{') {
      if (++depth > 100) return { tooDeep: true, underflow };
    } else if (ch === ']' || ch === '}') depth--;
    else if (ch === '-' || (ch >= '0' && ch <= '9')) {
      number.lastIndex = i;
      const token = number.exec(line)?.[0];
      if (token) {
        if (Number(token) === 0 && /[1-9]/.test(token.split(/[eE]/)[0])) underflow = true;
        i += token.length - 1;
      }
    }
  }
  return { tooDeep: false, underflow };
}

export function parseJsonLines(
  text: string,
  partial: boolean,
): {
  columns: string[];
  rows: string[][];
  numbers: (number | null)[][];
  partial: boolean;
  precisionWarning?: boolean;
  error?: string;
} {
  const lines = text.split('\n');
  if (partial && !text.endsWith('\n')) lines.pop();
  const columns: string[] = [];
  const records: Map<string, string>[] = [];
  const numericRecords: Map<string, number>[] = [];
  let truncated = partial;
  let precisionWarning = false;
  for (const [i, line] of lines.entries()) {
    if (!line.trim()) continue;
    if (records.length === 100) {
      truncated = true;
      break;
    }
    const inspection = inspectNumberLimits(line);
    if (inspection.tooDeep)
      return {
        columns: [],
        rows: [],
        numbers: [],
        partial: truncated,
        error: `JSON on line ${i + 1} is too deeply nested to preview.`,
      };
    let lineWarning = inspection.underflow;
    let value: unknown;
    try {
      value = JSON.parse(line, (_key, item: unknown) => {
        if (
          typeof item === 'number' &&
          (!Number.isFinite(item) || (Number.isInteger(item) && !Number.isSafeInteger(item)))
        )
          lineWarning = true;
        return item;
      });
    } catch {
      return {
        columns: [],
        rows: [],
        numbers: [],
        partial: truncated,
        error: `Invalid JSON on line ${i + 1}.`,
      };
    }
    precisionWarning ||= lineWarning;
    const entries =
      value !== null && typeof value === 'object' && !Array.isArray(value)
        ? Object.entries(value)
        : [['Value', value] as [string, unknown]];
    const record = new Map<string, string>();
    const numericRecord = new Map<string, number>();
    for (const [key, cell] of entries) {
      if (!columns.includes(key)) {
        if (columns.length === 30) {
          truncated = true;
          continue;
        }
        columns.push(key);
      }
      const rendered =
        typeof cell === 'string' || (typeof cell === 'number' && !Number.isFinite(cell))
          ? String(cell)
          : JSON.stringify(cell, (_key, item: unknown) =>
              typeof item === 'number' && !Number.isFinite(item) ? String(item) : item,
            );
      record.set(key, rendered.length > 200 ? `${rendered.slice(0, 200)}…` : rendered);
      if (
        typeof cell === 'number' &&
        !lineWarning &&
        Number.isFinite(cell) &&
        (!Number.isInteger(cell) || Number.isSafeInteger(cell))
      )
        numericRecord.set(key, cell);
    }
    records.push(record);
    numericRecords.push(numericRecord);
  }
  if (!columns.length && records.length)
    return {
      columns: ['Value'],
      rows: records.map(() => ['{}']),
      numbers: records.map(() => [null]),
      partial: truncated,
      precisionWarning,
    };
  return {
    columns,
    rows: records.map((record) => columns.map((key) => record.get(key) ?? '')),
    numbers: numericRecords.map((record) => columns.map((key) => record.get(key) ?? null)),
    partial: truncated,
    precisionWarning,
  };
}
