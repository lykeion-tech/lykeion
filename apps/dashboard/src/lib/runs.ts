import type { ExecutionRun } from '../../../../packages/contracts/src/research';

// Totals are null ("not reported") unless every run reports the figure.

export function runSeconds(run: ExecutionRun) {
  return run.endedAt
    ? Math.max(0, (Date.parse(run.endedAt) - Date.parse(run.startedAt)) / 1000)
    : null;
}

export function totalCost(runs: ExecutionRun[]) {
  return runs.length && runs.every((x) => x.costUsd !== undefined)
    ? runs.reduce((sum, x) => sum + x.costUsd!, 0)
    : null;
}

export function agentSeconds(runs: ExecutionRun[]) {
  return runs.length && runs.every((x) => runSeconds(x) !== null)
    ? runs.reduce((sum, x) => sum + runSeconds(x)!, 0)
    : null;
}

// Wall-clock span from the first start to the last end, so overlapping runs count once.
export function elapsedSeconds(runs: ExecutionRun[]) {
  return runs.length && runs.every((x) => x.endedAt)
    ? Math.max(
        0,
        (Math.max(...runs.map((x) => Date.parse(x.endedAt!))) -
          Math.min(...runs.map((x) => Date.parse(x.startedAt)))) /
          1000,
      )
    : null;
}

export function formatMoney(amount?: number | null) {
  return amount === undefined || amount === null
    ? 'Not reported'
    : new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format(amount);
}

// The charts are 850 wide and 150 tall; the time axis spans at least one second.
function timeAxis(runs: ExecutionRun[]) {
  const min = Math.min(...runs.map((x) => Date.parse(x.startedAt)));
  return {
    min,
    max: Math.max(min + 1000, ...runs.map((x) => Date.parse(x.endedAt ?? x.startedAt))),
  };
}

// SVG points for cumulative cost as a step line over runs sorted by start; '' when the cost is
// not fully reported or a start time is unreadable.
export function costLine(runs: ExecutionRun[]) {
  const total = totalCost(runs);
  if (total === null || runs.some((x) => !Number.isFinite(Date.parse(x.startedAt)))) return '';
  const min = Date.parse(runs[0]!.startedAt),
    max = Math.max(min + 1000, ...runs.map((x) => Date.parse(x.endedAt ?? x.startedAt)));
  const y = (sum: number) => 150 - (sum / Math.max(total, 0.01)) * 140;
  let sum = 0;
  const points = ['0,150'];
  for (const run of runs) {
    const x = ((Date.parse(run.startedAt) - min) / (max - min)) * 850;
    points.push(`${x},${y(sum)}`);
    sum += run.costUsd!;
    points.push(`${x},${y(sum)}`);
  }
  points.push(`850,${y(sum)}`);
  return points.join(' ');
}

// A run's bar on the timeline, at least 2 wide so instant runs stay visible.
export function runBar(run: ExecutionRun, runs: ExecutionRun[]) {
  const { min, max } = timeAxis(runs);
  return {
    x: ((Date.parse(run.startedAt) - min) / (max - min)) * 850,
    width: Math.max(
      2,
      ((Date.parse(run.endedAt ?? run.startedAt) - Date.parse(run.startedAt)) / (max - min)) * 850,
    ),
  };
}
