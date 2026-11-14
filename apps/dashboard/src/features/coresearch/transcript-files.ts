import type { FileRow, FileVersion } from './types';

export interface TranscriptFile {
  file: FileRow;
  version: FileVersion;
}

/** Only include versions actually saved by this agent run. */
export function filesForRun(
  run: string,
  files: FileRow[],
  versions: (FileVersion[] | undefined)[],
): TranscriptFile[] {
  if (!run) return [];
  return files.flatMap((file, i) => {
    const version = (versions[i] ?? [])
      .filter((v) => v.agent_run_id === run && (!v.file_id || v.file_id === file.id))
      .reduce<FileVersion | undefined>(
        (latest, v) => (!latest || v.n > latest.n ? v : latest),
        undefined,
      );
    return version ? [{ file, version }] : [];
  });
}
