export function molfileFromInchi(
  input: string,
): Promise<{ returnCode: number; molfile: string; message: string; log: string }>;
export function structureFromInchi(input: string): Promise<{
  returnCode: number;
  atoms: {
    element: string;
    charge: number;
    isotopicMass: number;
    radical: number;
    implicitH: number[];
    bonds: { to: number; type: number; stereo: number }[];
  }[];
  stereo: { type: number; parity: number; centralAtom: number; neighbors: number[] }[];
}>;
export function oclMoleculeFromStructure(
  structure: Awaited<ReturnType<typeof structureFromInchi>>,
  ocl: typeof import('openchemlib'),
): { toMolfile(): string };
