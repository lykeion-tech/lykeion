// AutoDock Vina AD4 types: https://github.com/ccsb-scripps/AutoDock-Vina/blob/develop/src/lib/atom_constants.h
const TYPE_TO_ELEMENT: Record<string, string> = {
  C: 'C',
  A: 'C',
  N: 'N',
  NA: 'N',
  O: 'O',
  OA: 'O',
  P: 'P',
  S: 'S',
  SA: 'S',
  H: 'H',
  HD: 'H',
  F: 'F',
  I: 'I',
  Mg: 'Mg',
  Mn: 'Mn',
  Zn: 'Zn',
  Ca: 'Ca',
  Fe: 'Fe',
  Cl: 'Cl',
  Br: 'Br',
  Si: 'Si',
  At: 'At',
};
const NUMBER = /^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/;
export function pdbqtGeometry(
  text: string,
): { text: string; format: string; atoms: number } | undefined {
  if (text.length > 1048576 || text.includes('\0')) return undefined;
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  if (lines.length > 20000) return undefined;
  const models = lines.flatMap((s, i) => (/^MODEL(?:\s|$)/.test(s) ? [i] : []));
  const ends = lines.flatMap((s, i) => (/^ENDMDL(?:\s|$)/.test(s) ? [i] : []));
  if (
    models.length > 1 ||
    ends.length > 1 ||
    models.length !== ends.length ||
    (models.length === 1 && models[0]! >= ends[0]!)
  )
    return undefined;
  const ids = new Set<number>(),
    output: string[] = [];
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]!;
    if (!/^(ATOM  |HETATM)/.test(line)) continue;
    if (
      (models.length && (i < models[0]! || i > ends[0]!)) ||
      line.length < 78 ||
      line[16] !== ' ' ||
      output.length >= 1000
    )
      return undefined;
    const serial = line.slice(6, 11).trim();
    if (!/^\d+$/.test(serial) || Number(serial) < 1 || ids.has(Number(serial))) return undefined;
    ids.add(Number(serial));
    const typeParts = line.slice(77).trim().split(/\s+/);
    if (typeParts.length !== 1 || !Object.hasOwn(TYPE_TO_ELEMENT, typeParts[0]!)) return undefined;
    const coords = [line.slice(30, 38), line.slice(38, 46), line.slice(46, 54)].map((s) =>
      s.trim(),
    );
    const charge = line.slice(70, 76).trim();
    if (
      [...coords, charge].some((s) => !NUMBER.test(s) || !Number.isFinite(Number(s))) ||
      coords.some((s) => Math.abs(Number(s)) > 1e6)
    )
      return undefined;
    output.push(`${TYPE_TO_ELEMENT[typeParts[0]!]} ${coords.join(' ')}`);
  }
  if (!output.length) return undefined;
  return {
    text: `${output.length}\nAutoDock atom coordinates; bonds inferred\n${output.join('\n')}\n`,
    format: 'xyz',
    atoms: output.length,
  };
}
