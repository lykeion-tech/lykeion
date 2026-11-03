import { expect, it } from 'vitest';
import { notebookCells } from './notebook';
import type { StudyRecord } from '../api/types';

function script(extra: Partial<StudyRecord> = {}): StudyRecord {
  return {
    id: 'id-1',
    card_id: 'c',
    board_column: 'experimentation',
    round: 1,
    kind: 'run',
    role: 'agent',
    key: 'x1',
    blob_key: '',
    agent_run_id: 'run-1',
    superseded_at: null,
    created_at: '2026-11-03T10:00:00Z',
    body: {
      tool: 'run_script',
      language: 'python',
      path: 'fit.py',
      script: 'print(1)',
      output: '1',
      exit_status: 0,
    },
    ...extra,
  };
}
it('keeps superseded script history and its recorded identity', () => {
  const cells = notebookCells([script({ superseded_at: '2026-11-03T11:00:00Z' })]);
  expect(cells).toHaveLength(1);
  expect(cells[0]).toMatchObject({
    key: 'x1',
    run: 'run-1',
    column: 'experimentation',
    round: 1,
    language: 'python',
    path: 'fit.py',
    code: 'print(1)',
    output: '1',
    at: '2026-11-03T10:00:00Z',
  });
});
it('skips other tools, other record kinds and records without a script', () => {
  expect(
    notebookCells([
      script({ body: { tool: 'call_tool', script: 'print(1)' } }),
      script({ body: { tool: 'run_code', code: 'print(1)' } }),
      script({ kind: 'call_tool' }),
      script({ body: { tool: 'run_script' } }),
      script({ body: { tool: 'run_script', script: 123 } }),
    ]),
  ).toEqual([]);
});
it('orders cells by recording time without changing the source records', () => {
  const records = [script({ key: 'later', created_at: '2026-11-03T12:00:00Z' }), script()];
  expect(notebookCells(records).map((c) => c.key)).toEqual(['x1', 'later']);
  expect(records[0]!.key).toBe('later');
});
it('keeps failed exit status, recorded errors and truncation metadata', () => {
  expect(
    notebookCells([
      script({
        body: {
          tool: 'run_script',
          script: 'bad()',
          exit_status: 1,
          output: 'partial',
          error: 'NameError: bad',
          truncated: true,
          error_truncated: true,
        },
      }),
    ])[0],
  ).toMatchObject({
    exit: 1,
    output: 'partial',
    error: 'NameError: bad',
    outputTruncated: true,
    errorTruncated: true,
  });
});
it('does not invent missing execution metadata or a successful exit', () => {
  expect(
    notebookCells([script({ agent_run_id: null, body: { tool: 'run_script', script: '' } })])[0],
  ).toMatchObject({
    run: null,
    language: '',
    path: '',
    code: '',
    exit: null,
    output: '',
    error: '',
  });
});
