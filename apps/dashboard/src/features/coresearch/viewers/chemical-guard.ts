import { cifGeometry } from './cif-geometry';
const MAX_BYTES = 1024 * 1024;
const MAX_ATOMS = 1000;
export function chemicalInput(
  text: string,
  ext: string,
  partial: boolean,
): { text: string; format: string; atoms: number } | undefined {
  if (partial || text.length > MAX_BYTES || text.includes('\0')) return undefined;
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  if (lines.length > 20000) return undefined;
  if (['cif', 'mmcif'].includes(ext)) return cifGeometry(text);
  if (['pdb', 'ent'].includes(ext)) {
    if (lines.filter((line) => line.startsWith('MODEL ')).length > 1) return undefined;
    const keep: string[] = [];
    let atoms = 0;
    for (const line of lines) {
      if (/^(ATOM  |HETATM)/.test(line)) {
        if (++atoms > MAX_ATOMS || line.length < 54) return undefined;
        for (const [a, b] of [
          [30, 38],
          [38, 46],
          [46, 54],
        ]) {
          const value = line.slice(a, b).trim();
          if (!/^[+-]?(?:\d+\.?\d*|\.\d+)$/.test(value) || Math.abs(Number(value)) > 1e6)
            return undefined;
        }
        keep.push(line.slice(0, 100));
      } else if (/^(CONECT|TER   |END   |ENDMDL)/.test(line)) keep.push(line.slice(0, 100));
    }
    return atoms ? { text: keep.join('\n') + '\nEND\n', format: 'pdb', atoms } : undefined;
  }
  if (['sdf', 'mol'].includes(ext)) {
    if (lines.filter((line) => line.trim() === '$$$$').length > 1 || lines.length < 5)
      return undefined;
    const counts = lines[3]!;
    if (!counts.includes('V2000')) return undefined;
    const atomText = counts.slice(0, 3).trim(),
      bondText = counts.slice(3, 6).trim();
    if (!/^\d+$/.test(atomText) || !/^\d+$/.test(bondText)) return undefined;
    const atoms = Number(atomText),
      bonds = Number(bondText);
    if (!atoms || atoms > MAX_ATOMS || bonds > 4000 || lines.length < 5 + atoms + bonds)
      return undefined;
    for (const line of lines.slice(4, 4 + atoms)) {
      if (line.length < 34 || !/^[A-Za-z*]{1,3}$/.test(line.slice(31, 34).trim())) return undefined;
      for (const [a, b] of [
        [0, 10],
        [10, 20],
        [20, 30],
      ]) {
        const value = line.slice(a, b).trim();
        if (!/^[+-]?(?:\d+\.?\d*|\.\d+)$/.test(value) || Math.abs(Number(value)) > 1e6)
          return undefined;
      }
    }
    for (const line of lines.slice(4 + atoms, 4 + atoms + bonds)) {
      const a = Number(line.slice(0, 3)),
        b = Number(line.slice(3, 6)),
        type = Number(line.slice(6, 9));
      if (
        !Number.isInteger(a) ||
        a < 1 ||
        a > atoms ||
        !Number.isInteger(b) ||
        b < 1 ||
        b > atoms ||
        ![1, 2, 3, 4].includes(type)
      )
        return undefined;
    }
    const end = lines.findIndex((line, i) => i >= 4 + atoms + bonds && line.trim() === 'M  END');
    if (end < 0) return undefined;
    return { text: lines.slice(0, end + 1).join('\n') + '\n$$$$\n', format: 'sdf', atoms };
  }
  return undefined;
}
