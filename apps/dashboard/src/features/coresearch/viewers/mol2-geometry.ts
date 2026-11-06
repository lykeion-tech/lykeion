const TYPES = new Set([
  'C.1',
  'C.2',
  'C.3',
  'C.ar',
  'C.cat',
  'N.1',
  'N.2',
  'N.3',
  'N.4',
  'N.am',
  'N.ar',
  'N.pl3',
  'O.2',
  'O.3',
  'O.co2',
  'S.2',
  'S.3',
  'S.o',
  'S.o2',
  'P.3',
  'H',
  'F',
  'Cl',
  'Br',
  'I',
]);
const NUM = /^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/;
export function mol2Geometry(
  text: string,
): { text: string; format: string; atoms: number } | undefined {
  if (text.length > 1048576 || text.includes('\0')) return undefined;
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  if (lines.length > 20000) return undefined;
  const tags = lines.map((line) => line.trim());
  const positions = (s: string) => tags.flatMap((value, i) => (value === s ? [i] : []));
  const molecule = positions('@<TRIPOS>MOLECULE'),
    atom = positions('@<TRIPOS>ATOM'),
    bond = positions('@<TRIPOS>BOND');
  if (
    molecule.length !== 1 ||
    atom.length !== 1 ||
    bond.length !== 1 ||
    molecule[0]! > atom[0]! ||
    atom[0]! > bond[0]!
  )
    return undefined;
  const first = molecule[0]!;
  if (tags.slice(0, first).some((s) => s && !s.startsWith('#')) || !tags[first + 1])
    return undefined;
  const counts = tags[first + 2]?.split(/\s+/);
  if (!counts || counts.length < 2 || !/^\d+$/.test(counts[0]!) || !/^\d+$/.test(counts[1]!))
    return undefined;
  const atomCount = Number(counts[0]),
    bondCount = Number(counts[1]);
  if (
    !Number.isSafeInteger(atomCount) ||
    !Number.isSafeInteger(bondCount) ||
    atomCount < 1 ||
    atomCount > 1000 ||
    bondCount < 1 ||
    bondCount > 4000
  )
    return undefined;
  const atomRows = tags.slice(atom[0]! + 1, bond[0]!).filter(Boolean);
  const end = tags.findIndex((s, i) => i > bond[0]! && s.startsWith('@<TRIPOS>'));
  const bondRows = tags.slice(bond[0]! + 1, end < 0 ? undefined : end).filter(Boolean);
  if (atomRows.length !== atomCount || bondRows.length !== bondCount) return undefined;
  const ids = new Map<string, number>(),
    safeAtoms: string[] = [],
    safeBonds: string[] = [],
    bondIds = new Set<string>(),
    pairs = new Set<string>();
  for (const row of atomRows) {
    const f = row.split(/\s+/);
    if (f.length < 6 || !/^[1-9]\d{0,8}$/.test(f[0]!) || ids.has(f[0]!) || !TYPES.has(f[5]!))
      return undefined;
    if (
      f
        .slice(2, 5)
        .some((v) => !NUM.test(v) || !Number.isFinite(Number(v)) || Math.abs(Number(v)) > 1e6)
    )
      return undefined;
    const id = ids.size + 1;
    ids.set(f[0]!, id);
    safeAtoms.push(`${id} A${id} ${f[2]} ${f[3]} ${f[4]} ${f[5]} 1 MOL 0.0`);
  }
  for (const row of bondRows) {
    const f = row.split(/\s+/);
    if (
      f.length !== 4 ||
      !/^[1-9]\d{0,8}$/.test(f[0]!) ||
      bondIds.has(f[0]!) ||
      !['1', '2', '3'].includes(f[3]!)
    )
      return undefined;
    const a = ids.get(f[1]!),
      b = ids.get(f[2]!);
    if (!a || !b || a === b) return undefined;
    const pair = [Math.min(a, b), Math.max(a, b)].join(':');
    if (pairs.has(pair)) return undefined;
    pairs.add(pair);
    bondIds.add(f[0]!);
    safeBonds.push(`${safeBonds.length + 1} ${a} ${b} ${f[3]}`);
  }
  return {
    atoms: atomCount,
    format: 'mol2',
    text: `@<TRIPOS>MOLECULE\nstructure\n${atomCount} ${bondCount} 0 0 0\nSMALL\nNO_CHARGES\n\n@<TRIPOS>ATOM\n${safeAtoms.join('\n')}\n@<TRIPOS>BOND\n${safeBonds.join('\n')}\n`,
  };
}
