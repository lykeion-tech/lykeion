interface Molecule {
  is_valid(): boolean;
  get_num_atoms(): number;
  get_svg(): string;
  delete(): void;
}
interface Chemistry {
  get_mol(input: string): Molecule | null;
}
export function smilesRecord(text: string): string | undefined {
  if (text.length > 2048) return undefined;
  const lines = text.split(/\r?\n/).filter((line) => line.trim());
  if (lines.length !== 1) return undefined;
  const value = lines[0]!.trim().split(/\s+/)[0]!;
  if (
    !value ||
    value.length > 512 ||
    /^InChI=/i.test(value) ||
    !/^[A-Za-z0-9@+\-[\]()=#$%\\/.::*]+$/.test(value)
  )
    return undefined;
  return value;
}
export function safeMoleculeSvg(svg: string): boolean {
  return (
    svg.length <= 262144 &&
    /<svg\b/i.test(svg) &&
    /<\/svg\s*>\s*$/i.test(svg) &&
    !/<\s*(?:script|foreignObject|iframe|object|embed|image|use|style)\b/i.test(svg) &&
    !/\b(?:href|xlink:href|on[a-z]+)\s*=/i.test(svg) &&
    !/url\s*\(|<!\s*(?:doctype|entity)|<\?xml-stylesheet/i.test(svg)
  );
}
export function depictSmiles(kit: Chemistry, input: string): string | undefined {
  let mol: Molecule | null = null;
  try {
    mol = kit.get_mol(input);
    if (!mol || !mol.is_valid() || mol.get_num_atoms() < 1 || mol.get_num_atoms() > 200)
      return undefined;
    const svg = mol.get_svg();
    return safeMoleculeSvg(svg) ? svg : undefined;
  } catch {
    return undefined;
  } finally {
    mol?.delete();
  }
}
