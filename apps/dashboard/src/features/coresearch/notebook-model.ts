// Adapted from Lykeion-L2 components/tasks/notebook-model.ts. Coresearch has
// one chat namespace and records kernel names, rather than delegated contexts
// and separate language identities. A free kernel name is never a language claim.
import type { CellOutput, CellRow } from './types';
export interface NotebookContext {
  name: string;
  cells: CellRow[];
}
export function buildNotebookContexts(cells: CellRow[]): NotebookContext[] {
  return [{ name: 'main', cells: [...cells].sort((a, b) => a.seq - b.seq) }];
}
export function kernelsOf(context: NotebookContext): string[] {
  return [...new Set(context.cells.map((cell) => cell.kernel))].sort((a, b) => a.localeCompare(b));
}
export function outputsOf(value: unknown): CellOutput[] {
  if (value == null) return [];
  if (Array.isArray(value))
    return value.filter((v): v is CellOutput => v !== null && typeof v === 'object');
  return [{ type: 'text', text: typeof value === 'string' ? value : JSON.stringify(value) }];
}
