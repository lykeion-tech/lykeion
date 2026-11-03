import type { Column, StudyRecord } from '../api/types';

export type NotebookCell = {
  key: string;
  run: string | null;
  column: Column;
  round: number;
  language: string;
  path: string;
  code: string;
  exit: number | null;
  output: string;
  at: string;
  error: string;
  outputTruncated: boolean;
  errorTruncated: boolean;
};

const text = (value: unknown): string => (typeof value === 'string' ? value : '');

/** Recorded scripts are history: superseding a record must not hide its cell. */
export function notebookCells(records: StudyRecord[]): NotebookCell[] {
  return records
    .filter(
      (r) => r.kind === 'run' && r.body.tool === 'run_script' && typeof r.body.script === 'string',
    )
    .sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at))
    .map((r) => ({
      key: r.key,
      run: r.agent_run_id,
      column: r.board_column,
      round: r.round,
      language: text(r.body.language),
      path: text(r.body.path),
      code: text(r.body.script),
      exit: typeof r.body.exit_status === 'number' ? r.body.exit_status : null,
      output: text(r.body.output),
      at: r.created_at,
      error: text(r.body.error),
      outputTruncated: r.body.truncated === true,
      errorTruncated: r.body.error_truncated === true,
    }));
}
