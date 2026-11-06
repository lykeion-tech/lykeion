import { inchiRecord, safeInchiMolfile } from './inchi';

type Atom = {
  element: string;
  charge: number;
  isotopicMass: number;
  radical: number;
  implicitH: number[];
  bonds: { to: number; type: number; stereo: number }[];
};
type Stereo = { type: number; parity: number; centralAtom: number; neighbors: number[] };
export function safeStereoStructure(
  atoms: Atom[],
  stereo: Stereo[],
  kind: 'tetrahedral' | 'geometric' | 'mixed' = 'tetrahedral',
): boolean {
  if (
    !Array.isArray(atoms) ||
    !atoms.length ||
    atoms.length > 200 ||
    !Array.isArray(stereo) ||
    !stereo.length ||
    stereo.length > 200
  )
    return false;
  let bonds = 0;
  const geometricBonds = new Set<string>();
  let tetrahedralCount = 0;
  for (const [index, atom] of atoms.entries()) {
    if (
      !atom ||
      typeof atom.element !== 'string' ||
      !/^[A-Z][a-z]?$/.test(atom.element) ||
      !Array.isArray(atom.bonds) ||
      atom.bonds.length > 16 ||
      !Array.isArray(atom.implicitH) ||
      atom.implicitH.length !== 4 ||
      atom.implicitH.some((n) => !Number.isInteger(n) || n < 0 || n > 8) ||
      [atom.charge, atom.isotopicMass, atom.radical].some(
        (n) => !Number.isInteger(n) || Math.abs(n) > 1000,
      )
    )
      return false;
    bonds += atom.bonds.length;
    if (bonds > 800) return false;
    for (const bond of atom.bonds) {
      if (
        !bond ||
        !Number.isInteger(bond.to) ||
        bond.to < 0 ||
        bond.to >= atoms.length ||
        bond.to === index ||
        ![1, 2, 3, 4].includes(bond.type) ||
        !Number.isInteger(bond.stereo) ||
        Math.abs(bond.stereo) > 10
      )
        return false;
    }
  }
  for (const s of stereo) {
    if (kind !== 'tetrahedral' && s?.type === 1) {
      if (
        !s ||
        s.type !== 1 ||
        ![1, 2].includes(s.parity) ||
        s.centralAtom !== -1 ||
        !Array.isArray(s.neighbors) ||
        s.neighbors.length !== 4 ||
        s.neighbors.some((n) => !Number.isInteger(n) || n < 0 || n >= atoms.length) ||
        new Set(s.neighbors).size !== 4
      )
        return false;
      const [left, a, b, right] = s.neighbors as [number, number, number, number];
      const bondKey = [Math.min(a, b), Math.max(a, b)].join(':');
      if (geometricBonds.has(bondKey)) return false;
      geometricBonds.add(bondKey);
      if (geometricBonds.size > 2) return false;
      if (
        !atoms[a]!.bonds.some((x) => x.to === b && x.type === 2) ||
        !atoms[a]!.bonds.some((x) => x.to === left) ||
        !atoms[b]!.bonds.some((x) => x.to === right)
      )
        return false;
      continue;
    }
    if (kind === 'geometric') return false;
    if (
      !s ||
      s.type !== 2 ||
      ![1, 2].includes(s.parity) ||
      !Number.isInteger(s.centralAtom) ||
      s.centralAtom < 0 ||
      s.centralAtom >= atoms.length ||
      !Array.isArray(s.neighbors) ||
      s.neighbors.length !== 4 ||
      s.neighbors.some((n) => !Number.isInteger(n) || n < -1 || n >= atoms.length)
    )
      return false;
    const actual = s.neighbors.filter((n) => n !== -1 && n !== s.centralAtom);
    if (actual.length < 3 || new Set(actual).size !== actual.length) return false;
    tetrahedralCount++;
  }
  return kind === 'mixed' ? geometricBonds.size > 0 && tetrahedralCount > 0 : true;
}
export async function geometricMolfileCandidates(input: string): Promise<string[]> {
  if (
    inchiRecord(input) !== input ||
    !/\/b\d+-\d+[+-](?:,\d+-\d+[+-])?(?:\/t[^/]+\/m[01]\/s1)?$/.test(input)
  )
    return [];
  try {
    const [ocl, inchi] = await Promise.all([
      import('openchemlib'),
      import('./vendor/inchi/inchi-js.js'),
    ]);
    const structure = await inchi.structureFromInchi(input);
    if (
      structure.returnCode !== 0 ||
      !safeStereoStructure(
        structure.atoms,
        structure.stereo,
        /\/t/.test(input) ? 'mixed' : 'geometric',
      )
    )
      return [];
    let count = 0;
    const mapping = structure.atoms.map((atom) =>
      atom.element === 'H' && !atom.charge && !atom.radical && !atom.isotopicMass ? -1 : count++,
    );
    const pairs = structure.stereo
      .filter((s) => s.type === 1)
      .map(({ neighbors }) => [mapping[neighbors[1]!]!, mapping[neighbors[2]!]!] as const);
    if (pairs.some(([a, b]) => a < 0 || b < 0)) return [];
    const candidates: string[] = [];
    for (let combination = 0; combination < 2 ** pairs.length; combination++) {
      const molecule = inchi.oclMoleculeFromStructure(structure, ocl);
      for (const [i, [a, b]] of pairs.entries()) {
        const bond = molecule.getBond(a, b);
        if (bond < 0) return [];
        const parity =
          combination & (1 << i) ? ocl.Molecule.cBondParityZor2 : ocl.Molecule.cBondParityEor1;
        molecule.setBondParity(bond, parity, false);
      }
      molecule.setParitiesValid(0);
      molecule.inventCoordinates();
      molecule.setStereoBondsFromParity();
      const molfile = `${molecule.toMolfile().trimEnd()}\n$$$$\n`;
      if (!safeInchiMolfile(molfile)) return [];
      candidates.push(molfile);
    }
    return candidates;
  } catch {
    return [];
  }
}
export async function tetrahedralMolfile(input: string): Promise<string | undefined> {
  if (inchiRecord(input) !== input || !/\/t[^/]+\/m[01]\/s1$/.test(input)) return;
  try {
    const [ocl, inchi] = await Promise.all([
      import('openchemlib'),
      import('./vendor/inchi/inchi-js.js'),
    ]);
    const structure = await inchi.structureFromInchi(input);
    if (structure.returnCode !== 0 || !safeStereoStructure(structure.atoms, structure.stereo))
      return;
    const molecule = inchi.oclMoleculeFromStructure(structure, ocl);
    const molfile = `${molecule.toMolfile().trimEnd()}\n$$$$\n`;
    return safeInchiMolfile(molfile) ? molfile : undefined;
  } catch {
    return;
  }
}
