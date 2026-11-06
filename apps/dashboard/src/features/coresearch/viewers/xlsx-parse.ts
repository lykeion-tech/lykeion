export function worksheetTarget(target: string): string | undefined {
  if (/^worksheets\/sheet[1-9]\d*\.xml$/.test(target)) return `xl/${target}`;
  if (/^\/xl\/worksheets\/sheet[1-9]\d*\.xml$/.test(target)) return target.slice(1);
}
export function cellCoordinate(reference: string): { row: number; column: number } | undefined {
  const match = /^([A-Z]{1,3})([1-9]\d{0,6})$/.exec(reference);
  if (!match) return;
  let column = 0;
  for (const char of match[1]!) column = column * 26 + char.charCodeAt(0) - 64;
  const row = Number(match[2]);
  if (column > 16384 || row > 1048576) return;
  return { row: row - 1, column: column - 1 };
}
