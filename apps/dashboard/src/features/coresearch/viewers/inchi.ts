import { safeMoleculeSvg } from './smiles';
interface Molecule {
  is_valid(): boolean;
  get_num_atoms(): number;
  get_inchi(): string;
  get_molblock?(): string;
  set_new_coords(): unknown;
  get_svg(): string;
  delete(): void;
}
export function inchiRecord(text: string): string | undefined {
  if (text.length > 2048) return undefined;
  const value = text.trim();
  if (!/^InChI=1S?\/[A-Za-z0-9+(),.\-;\/]+$/.test(value)) return;
  if (!/\/[btms]/.test(value)) return value;
  // Permit up to two geometric bonds and optional absolute tetrahedral stereo.
  // Isotopic and relative/racemic stereo remain unavailable.
  const bondLayer = value.indexOf('/b');
  if (
    bondLayer >= 0 &&
    !/\/[itms]/.test(value.slice(0, bondLayer)) &&
    /\/b\d+-\d+[+-](?:,\d+-\d+[+-])?(?:\/t[^/]+\/m[01]\/s1)?$/.test(value)
  )
    return value;
  return !/\/[bi]/.test(value) && /\/t[^/]+\/m[01]\/s1$/.test(value) ? value : undefined;
}
// Accept only the bounded V2000 subset emitted by the bundled converter.
// Other variants fail closed before RDKit receives the molecule.
export function safeInchiMolfile(file: string): boolean {
  if (file.length > 65536 || /[^\x09\x0a\x0d\x20-\x7e]/.test(file)) return false;
  const lines = file.replace(/\r\n/g, '\n').split('\n');
  if (lines.length < 6 || lines.some((line) => line.length > 256)) return false;
  const counts = lines[3]!;
  if (!/^\s*\d{1,3}\s+\d{1,3}(?:\s+\d+)+\s+V2000\s*$/.test(counts)) return false;
  const atoms = Number(counts.slice(0, 3)),
    bonds = Number(counts.slice(3, 6));
  if (
    !Number.isInteger(atoms) ||
    atoms < 1 ||
    atoms > 200 ||
    !Number.isInteger(bonds) ||
    bonds < 0 ||
    bonds > 400 ||
    lines.length < 6 + atoms + bonds
  )
    return false;
  const elements = new Set(
    'H He Li Be B C N O F Ne Na Mg Al Si P S Cl Ar K Ca Sc Ti V Cr Mn Fe Co Ni Cu Zn Ga Ge As Se Br Kr Rb Sr Y Zr Nb Mo Tc Ru Rh Pd Ag Cd In Sn Sb Te I Xe Cs Ba La Ce Pr Nd Pm Sm Eu Gd Tb Dy Ho Er Tm Yb Lu Hf Ta W Re Os Ir Pt Au Hg Tl Pb Bi Po At Rn Fr Ra Ac Th Pa U Np Pu Am Cm Bk Cf Es Fm Md No Lr Rf Db Sg Bh Hs Mt Ds Rg Cn Nh Fl Mc Lv Ts Og'.split(
      ' ',
    ),
  );
  for (const line of lines.slice(4, 4 + atoms)) {
    const fields = line.trim().split(/\s+/);
    if (
      fields.length < 5 ||
      fields.length > 20 ||
      !elements.has(fields[3]!) ||
      fields.some(
        (x, i) =>
          i !== 3 &&
          (!/^[+-]?\d+(?:\.\d+)?$/.test(x) ||
            !Number.isFinite(Number(x)) ||
            Math.abs(Number(x)) > 100000),
      )
    )
      return false;
  }
  for (const line of lines.slice(4 + atoms, 4 + atoms + bonds)) {
    const f = line.trim().split(/\s+/);
    if (f.length < 4 || f.length > 8 || f.some((x) => !/^\d{1,3}$/.test(x))) return false;
    const [a, b, kind] = f.map(Number);
    if (!a || !b || a > atoms || b > atoms || a === b || !kind || kind > 3) return false;
  }
  let cursor = 4 + atoms + bonds;
  while (lines[cursor]?.startsWith('M  ') && lines[cursor] !== 'M  END') {
    const fields = lines[cursor]!.trim().split(/\s+/);
    if (
      fields.length < 5 ||
      fields[0] !== 'M' ||
      !['CHG', 'ISO', 'RAD'].includes(fields[1]!) ||
      !/^\d+$/.test(fields[2]!)
    )
      return false;
    const n = Number(fields[2]);
    if (n < 1 || n > 8 || fields.length !== 3 + n * 2) return false;
    for (let i = 0; i < n; i++) {
      if (
        !/^\d+$/.test(fields[3 + i * 2]!) ||
        Number(fields[3 + i * 2]) < 1 ||
        Number(fields[3 + i * 2]) > atoms ||
        !/^[+-]?\d{1,4}$/.test(fields[4 + i * 2]!)
      )
        return false;
    }
    if (++cursor > 4 + atoms + bonds + 64) return false;
  }
  return (
    lines[cursor++] === 'M  END' &&
    lines[cursor++] === '$$$$' &&
    lines.slice(cursor).every((line) => !line.trim())
  );
}
export function depictInchi(
  kit: { get_mol(input: string): Molecule | null },
  molfile: string,
  original: string,
  verifyDrawing = false,
): string | undefined {
  if (!safeInchiMolfile(molfile)) return undefined;
  let mol: Molecule | null = null;
  try {
    mol = kit.get_mol(molfile);
    if (!mol || !mol.is_valid() || mol.get_num_atoms() < 1 || mol.get_num_atoms() > 200)
      return undefined;
    if (mol.get_inchi() !== original) return undefined;
    mol.set_new_coords();
    if (mol.get_inchi() !== original) return undefined;
    if (verifyDrawing) {
      const block = mol.get_molblock?.();
      if (!block) return undefined;
      const validated = `${block.trimEnd()}\n$$$$\n`;
      if (!safeInchiMolfile(validated)) return undefined;
      const drawing = kit.get_mol(validated);
      try {
        if (!drawing || !drawing.is_valid() || drawing.get_inchi() !== original) return undefined;
      } finally {
        drawing?.delete();
      }
    }
    const svg = mol.get_svg();
    return safeMoleculeSvg(svg) ? svg : undefined;
  } catch {
    return undefined;
  } finally {
    mol?.delete();
  }
}
