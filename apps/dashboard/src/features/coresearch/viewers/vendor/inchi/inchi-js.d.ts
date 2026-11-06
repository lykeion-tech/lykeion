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
): {
  toMolfile(): string;
  getBond(a: number, b: number): number;
  setBondParity(bond: number, parity: number, pseudo: boolean): void;
  setParitiesValid(bits: number): void;
  inventCoordinates(): void;
  setStereoBondsFromParity(): void;
};
