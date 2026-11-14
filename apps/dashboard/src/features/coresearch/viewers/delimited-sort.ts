type Decimal = { sign: number; digits: string; magnitude: number };
function decimal(value: string): Decimal | undefined {
  const m = /^([+-]?)(?:(\d+)(?:\.(\d*))?|\.(\d+))(?:[eE]([+-]?\d{1,6}))?$/.exec(value.trim());
  if (!m) return;
  const fraction = m[3] ?? m[4] ?? '';
  const digits = ((m[2] ?? '') + fraction).replace(/^0+/, '').replace(/0+$/, '');
  if (!digits) return { sign: 0, digits: '', magnitude: 0 };
  const raw = ((m[2] ?? '') + fraction).replace(/^0+/, '');
  return { sign: m[1] === '-' ? -1 : 1, digits, magnitude: raw.length - fraction.length + Number(m[5] ?? 0) };
}
function compareDecimal(a: Decimal, b: Decimal): number {
  if (a.sign !== b.sign) return a.sign - b.sign;
  if (!a.sign) return 0;
  if (a.magnitude !== b.magnitude) return Math.sign(a.magnitude - b.magnitude) * a.sign;
  for (let i = 0; i < Math.max(a.digits.length, b.digits.length); i++) {
    const n = (a.digits.charCodeAt(i) || 48) - (b.digits.charCodeAt(i) || 48);
    if (n) return Math.sign(n) * a.sign;
  }
  return 0;
}
export function sortRows(rows: string[][], column: number, direction: 'ascending' | 'descending'): string[][] {
  const entries = rows.map((row, index) => ({ row, index, value: row[column] ?? '', number: decimal(row[column] ?? '') }));
  const numeric = entries.some(x => x.value.trim() !== '') && entries.every(x => !x.value.trim() || x.number);
  const order = direction === 'ascending' ? 1 : -1;
  return entries.sort((a, b) => {
    const emptyA = !a.value.trim(), emptyB = !b.value.trim();
    if (emptyA !== emptyB) return emptyA ? 1 : -1;
    if (emptyA) return a.index - b.index;
    const comparison = numeric ? compareDecimal(a.number!, b.number!) : (a.value < b.value ? -1 : a.value > b.value ? 1 : 0);
    return comparison * order || a.index - b.index;
  }).map(x => x.row);
}
