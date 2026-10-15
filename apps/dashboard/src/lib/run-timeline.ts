import type { AgentRun } from '../api/types';
export const activeRun = (r: AgentRun) => ['queued', 'dispatched', 'running'].includes(r.status);
export const milliseconds = (s: string | null) =>
  s && Number.isFinite(Date.parse(s)) ? Date.parse(s) : null;
export function runDuration(r: AgentRun, now: number) {
  const start = milliseconds(r.started_at),
    end = milliseconds(r.completed_at);
  if (start === null) return null;
  if (end !== null) return Math.max(0, end - start);
  return r.status === 'running' ? Math.max(0, now - start) : null;
}
export const durationLabel = (ms: number | null) =>
  ms === null
    ? '—'
    : ms < 60000
      ? `${Math.floor(ms / 1000)}s`
      : ms < 3600000
        ? `${Math.floor(ms / 60000)}m ${Math.floor(ms / 1000) % 60}s`
        : `${Math.floor(ms / 3600000)}h ${Math.floor(ms / 60000) % 60}m`;
export const runOrder = (a: AgentRun, b: AgentRun) =>
  (milliseconds(a.completed_at ?? a.started_at ?? a.created_at) ?? 0) -
    (milliseconds(b.completed_at ?? b.started_at ?? b.created_at) ?? 0) || a.id.localeCompare(b.id);
export function runTimeline(input: readonly AgentRun[], now: number) {
  const runs = [...input].sort(runOrder);
  const times = runs.flatMap((r) =>
    [
      milliseconds(r.created_at),
      milliseconds(r.started_at),
      milliseconds(r.completed_at),
      r.status === 'running' ? now : null,
    ].filter((t): t is number => t !== null),
  );
  const first = times.length ? Math.min(...times) : now,
    last = times.length ? Math.max(...times) : now;
  const span = Math.max(60000, last - first),
    from = first - span * 0.03,
    to = first + span * 1.03;
  const x = (t: number) => ((t - from) / (to - from)) * 1000;
  const total = runs.reduce((n, r) => n + r.cost_usd, 0);
  let sum = 0,
    path = 'M0,100';
  for (const r of runs) {
    const at = milliseconds(r.completed_at);
    if (at === null) continue;
    const before = 100 - (sum / Math.max(total, 0.01)) * 95;
    sum += r.cost_usd;
    path += ` L${x(at)},${before} L${x(at)},${100 - (sum / Math.max(total, 0.01)) * 95}`;
  }
  path += ` L1000,${100 - (sum / Math.max(total, 0.01)) * 95}`;
  const durations = runs.map((r) => runDuration(r, now));
  const partial = runs.some(
    (r, i) => r.attempt > 1 || (r.status !== 'queued' && durations[i] === null),
  );
  const groups = new Map<string, AgentRun[]>();
  for (const r of [...runs].reverse()) {
    const d = new Date(r.started_at ?? r.created_at).toLocaleDateString();
    groups.set(d, [...(groups.get(d) ?? []), r]);
  }
  return {
    runs,
    total,
    path,
    area: path + ' L1000,100 L0,100 Z',
    x,
    from,
    to,
    first,
    last,
    elapsed: times.length ? last - first : null,
    agentMs: runs.some((r, i) => r.status !== 'queued' && durations[i] === null)
      ? null
      : durations.reduce<number>((n, d) => n + (d ?? 0), 0),
    partial,
    groups: [...groups],
  };
}
