import { describe, expect, it } from 'vitest';
import { filesForRun } from './transcript-files';
import type { FileRow, FileVersion } from './types';

const version = (n: number, agent_run_id: string): FileVersion => ({
  n,
  agent_run_id,
  size: 1,
  sha256: 'x',
  created_at: '',
});
const file: FileRow = { id: 'f', path: 'result.csv', head: version(3, 'other') };
describe('transcript file provenance', () => {
  it('shows only the latest saved version produced by the given run', () => {
    expect(filesForRun('run', [file], [[version(1, 'run'), version(2, 'run'), file.head]])).toEqual(
      [{ file, version: version(2, 'run') }],
    );
    expect(filesForRun('missing', [file], [[version(1, 'run')]])).toEqual([]);
    expect(filesForRun('', [file], [[version(1, 'run')]])).toEqual([]);
  });
});
