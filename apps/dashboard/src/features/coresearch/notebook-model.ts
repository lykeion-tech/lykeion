// Adapted from Lykeion-L2 components/tasks/notebook-model.ts. Coresearch has
// one chat namespace and records kernel names, rather than delegated contexts
// and separate language identities. A free kernel name is never a language claim.
import type { CellOutput, CellRow, ChatRow } from './types';
import type { Runtime } from '../../api/types';
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
export function chatRuntime(chat: ChatRow, runtimes: Runtime[]): Runtime | undefined {
  if (!chat.daemon_id) return undefined;
  return runtimes.find(
    (r) =>
      r.workspace_id === chat.workspace_id &&
      r.daemon_id === chat.daemon_id &&
      r.owner_id === chat.owner_id &&
      r.harness === chat.cli,
  );
}
