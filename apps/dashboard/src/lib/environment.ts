import type { AgentRun, Runtime, StudyRecord } from '../api/types';

const languages = [
  { key: 'python', name: 'Python' },
  { key: 'r', name: 'R' },
  { key: 'julia', name: 'Julia' },
] as const;

export type BaseState = 'Ready' | 'Building' | 'Failed' | 'Not reported';

function baseState(value: string | undefined): { state: BaseState; reason: string } {
  if (value === 'ready') return { state: 'Ready', reason: '' };
  if (value === 'building') return { state: 'Building', reason: '' };
  if (value?.startsWith('failed: ')) return { state: 'Failed', reason: value.slice(8) };
  return { state: 'Not reported', reason: '' };
}

// The record's full-precision creation time; the run's own time, kept to the
// second, only when that doesn't parse.
function recorded(r: StudyRecord) {
  const created = Date.parse(r.created_at);
  return Number.isNaN(created) ? Date.parse(String(r.body.at)) || 0 : created;
}

// What the study's runs ran on and added: each machine (one per daemon) whose
// runtimes ran the study with the state of its language bases, and per
// language the packages the study's latest run of it had added.
export function environmentView(runs: AgentRun[], runtimes: Runtime[], runRecords: StudyRecord[]) {
  const daemons = new Map<string, Runtime>();
  for (const run of runs) {
    const rt = runtimes.find((x) => x.id === run.runtime_id);
    if (rt && !daemons.has(rt.daemon_id)) daemons.set(rt.daemon_id, rt);
  }
  const machines = [...daemons.values()].map((rt) => ({
    name: rt.custom_name || rt.device_name,
    bases: languages.map((l) => ({ language: l.name, ...baseState(rt.bases?.[l.key]) })),
  }));
  const packages = languages.flatMap((l) => {
    const latest = runRecords
      .filter((r) => r.body.language === l.key)
      .reduce<StudyRecord | undefined>(
        (a, r) => (!a || recorded(r) >= recorded(a) ? r : a),
        undefined,
      );
    const text = latest?.body.packages_study;
    if (!latest || typeof text !== 'string' || !text.trim()) return [];
    return [{ language: l.name, text, at: String(latest.body.at ?? latest.created_at) }];
  });
  return { machines, packages };
}
