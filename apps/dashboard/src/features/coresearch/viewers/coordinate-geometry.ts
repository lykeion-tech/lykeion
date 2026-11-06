import { ELEMENTS } from './cif-geometry';
const numeric = /^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/;
function coordinate(s: string): number | undefined {
  if (!numeric.test(s)) return undefined;
  const n = Number(s);
  return Number.isFinite(n) && Math.abs(n) <= 100000 ? n : undefined;
}
export function coordinateGeometry(
  text: string,
  format: string,
): { text: string; format: string; atoms: number } | undefined {
  if (text.length > 1048576 || text.includes('\0')) return undefined;
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  while (lines.length && !lines.at(-1)!.trim()) lines.pop();
  const output: string[] = [];
  if (format === 'xyz') {
    if (lines.length < 3 || !/^\d+$/.test(lines[0]!.trim())) return undefined;
    const count = Number(lines[0]!.trim());
    if (count < 1 || count > 1000 || lines.length !== count + 2) return undefined;
    for (const line of lines.slice(2)) {
      const fields = line.trim().split(/\s+/);
      if (
        fields.length !== 4 ||
        !ELEMENTS.has(fields[0]!) ||
        fields.slice(1).some((s) => coordinate(s) === undefined)
      )
        return undefined;
      output.push(fields.join(' '));
    }
  } else if (format === 'gro') {
    if (lines.length < 4 || !/^\d+$/.test(lines[1]!.trim())) return undefined;
    const count = Number(lines[1]!.trim());
    if (count < 1 || count > 1000 || lines.length !== count + 3) return undefined;
    for (const line of lines.slice(2, 2 + count)) {
      if (
        line.length < 44 ||
        line.slice(44).trim() ||
        !/^\d+$/.test(line.slice(0, 5).trim()) ||
        !line.slice(5, 10).trim() ||
        !/^\d+$/.test(line.slice(15, 20).trim())
      )
        return undefined;
      const atom = line.slice(10, 15).trim();
      if (!['H', 'B', 'C', 'N', 'O', 'F', 'P', 'S', 'I'].includes(atom)) return undefined;
      const coords = [line.slice(20, 28), line.slice(28, 36), line.slice(36, 44)].map((s) =>
        coordinate(s.trim()),
      );
      if (coords.some((n) => n === undefined)) return undefined;
      output.push(`${atom} ${coords.map((n) => Number((n! * 10).toPrecision(12))).join(' ')}`);
    }
    const box = lines[count + 2]!.trim().split(/\s+/);
    if (![3, 9].includes(box.length) || box.some((v) => coordinate(v) === undefined))
      return undefined;
  } else return undefined;
  return {
    text: `${output.length}\nCoordinates in angstroms; bonds inferred\n${output.join('\n')}\n`,
    format: 'xyz',
    atoms: output.length,
  };
}
