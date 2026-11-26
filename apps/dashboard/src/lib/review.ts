import type {
  LykEvent,
  Card,
  CheckStatus,
  Column,
  ReviewAttempt,
  ReviewCheck,
  ReviewColumn,
  ReviewItem,
  StudyRecord,
} from '../api/types';
import { columnName } from './studies';
import { measurementText } from './measurement';

const checkLabels: Record<string, string> = {
  exists: 'Paper exists',
  not_retracted: 'Not retracted',
  missed: 'Missed paper',
  none_qualify: 'No paper qualifies',
  out_of_scope: 'Out of scope',
  cites_recorded: 'Cites a recorded source',
  control: 'Has a control',
  falsifiable: 'Falsifiable',
  supported: 'Evidence supports it',
  lookup: 'Not already tested',
  declared: 'Declared',
  traced: 'Traced to a run',
  bound: 'Bound to a saved field',
  measure: 'Matches a hypothesis measure',
  scale: 'Matches the declared scale',
  unbound: 'No field binding',
  described: 'Described correctly',
  quote_in_source: 'Quote is in the source',
  verdict: 'Verdict holds',
  wrong: 'Error is real',
  specific: 'Specific, not generic',
};

export function checkLabel(id: string): string {
  if (/^criterion:\d+$/.test(id)) return `Criterion ${id.slice('criterion:'.length)}`;
  if (id.startsWith('file:')) return `File ${id.slice('file:'.length)} saved`;
  return Object.hasOwn(checkLabels, id) ? checkLabels[id]! : id.replaceAll('_', ' ');
}

export function reviewItemLabel(item: ReviewItem): string {
  return measurementText(item.measure, item.value) ?? item.label;
}

export function statusWord(s: CheckStatus): string {
  return { pass: 'Supported', unclear: 'Unclear', fail: 'Unsupported', withdrawn: 'Withdrawn' }[s];
}

export function indexOf(checks: ReviewCheck[]): number | null {
  const ps = checks.flatMap((c) => (c.p === undefined ? [] : [c.p]));
  return ps.length ? Math.round((100 * ps.reduce((sum, p) => sum + p, 0)) / ps.length) : null;
}

export interface Spoke {
  id: string;
  item: number;
  check: number;
  p: number;
  status: CheckStatus;
  warning: boolean;
}

export function spokes(a: ReviewAttempt): Spoke[] {
  return [...a.items, ...(a.missed ?? [])].flatMap((item, i) =>
    item.status === 'withdrawn'
      ? []
      : item.checks.flatMap((check, j) =>
          check.p === undefined
            ? []
            : [
                {
                  id: `${i}-${j}`,
                  item: i,
                  check: j,
                  p: check.p,
                  status: check.status,
                  warning: check.warning ?? false,
                },
              ],
        ),
  );
}

export function defaultSpoke(a: ReviewAttempt): string | null {
  const all = spokes(a);
  const weakest = a.weakest;
  const i = weakest
    ? [...a.items, ...(a.missed ?? [])].findIndex((item) => item.key === weakest.key)
    : -1;
  const preferred = all.filter((s) => s.item === i);
  return (
    (preferred.length ? preferred : all).reduce<Spoke | null>(
      (min, s) => (!min || s.p < min.p ? s : min),
      null,
    )?.id ?? null
  );
}

export function tileOf(
  col: ReviewColumn,
  current: Column,
): {
  index: number | null;
  pass: number | null;
  statusSummary?: string;
  total: number;
  tryN: number | null;
  state: 'reviewed' | 'working' | 'not reached';
} {
  const a = col.attempts.at(-1);
  if (!a)
    return {
      index: null,
      pass: 0,
      total: 0,
      tryN: null,
      state: col.column === current ? 'working' : 'not reached',
    };
  const items = [...a.items, ...(a.missed ?? [])].filter((item) => item.status !== 'withdrawn');
  const runStatuses = col.column === 'validation' && col.mode === 'runs';
  const counts = new Map<string, number>();
  if (runStatuses) {
    for (const item of items) counts.set(item.status, (counts.get(item.status) ?? 0) + 1);
  }
  return {
    index: indexOf(items.flatMap((item) => item.checks)),
    pass: runStatuses ? null : items.filter((item) => item.status === 'pass').length,
    statusSummary: runStatuses
      ? [...counts].map(([status, count]) => `${count} ${status}`).join(' · ') ||
        'No hypothesis statuses'
      : undefined,
    total: items.length,
    tryN: a.attempt,
    state: 'reviewed',
  };
}

export function feedLine(a: ReviewAttempt): string {
  const outcome = {
    pass: 'good to go',
    carry: 'good to go, with open doubts',
    redo: 'needs another try',
    block: 'blocked, waiting for you',
    ask: 'waiting for you',
    route_back: 'sent back to Experimentation',
  }[a.decision];
  return `Jev · ${columnName(a.column)} · ${outcome}`;
}

export function reviewTimeline(
  events: LykEvent[],
  columns: ReviewColumn[],
  card:
    | Pick<
        Card,
        'board_column' | 'round' | 'state' | 'gating_until' | 'ending' | 'flag' | 'legacy_column'
      >
    | undefined,
  now: number,
): { at: string; text: string; column: Column }[] {
  const starts = events.filter(
    (event) =>
      event.type === 'review.started' &&
      typeof event.payload.pass_id === 'string' &&
      typeof event.payload.column === 'string' &&
      typeof event.payload.round === 'number',
  );
  const finishes = new Map(
    events
      .filter(
        (event) => event.type === 'review.finished' && typeof event.payload.pass_id === 'string',
      )
      .map((event) => [event.payload.pass_id, event]),
  );
  const used = new Set<number>();
  const lines: { at: string; text: string; column: Column }[] = [];
  for (const column of columns) {
    for (const result of column.attempts) {
      const start = [...starts]
        .reverse()
        .find(
          (event) =>
            !used.has(event.id) &&
            (result.jev_pass_id
              ? event.payload.pass_id === result.jev_pass_id
              : result.legacy_timeline !== false) &&
            event.payload.column === result.column &&
            event.payload.round === result.round &&
            Date.parse(event.created_at) <= Date.parse(result.at) &&
            finishes.get(event.payload.pass_id)?.payload.status !== 'failed',
        );
      if (start) used.add(start.id);
      if (!start && result.legacy_timeline === false && !result.jev_started_at) continue;
      lines.push({
        at: start?.created_at ?? result.jev_started_at ?? result.at,
        text: feedLine(result),
        column: result.column,
      });
    }
  }
  for (const start of starts) {
    if (used.has(start.id)) continue;
    const column = start.payload.column as Column;
    const finish = finishes.get(start.payload.pass_id);
    const active =
      !finish &&
      !starts.some(
        (other) =>
          other.id > start.id &&
          other.payload.column === column &&
          other.payload.round === start.payload.round,
      ) &&
      card?.board_column === column &&
      card.round === start.payload.round &&
      card.state === 'working' &&
      !card.ending &&
      !card.flag &&
      !!card.gating_until &&
      Date.parse(card.gating_until) > now;
    const status = active
      ? 'reviewing'
      : finish?.payload.status === 'completed'
        ? 'review complete'
        : 'review unavailable';
    lines.push({ at: start.created_at, text: `Jev · ${columnName(column)} · ${status}`, column });
  }
  return lines.sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
}

export function legacyColumns(
  scores: StudyRecord[],
  outcomes: StudyRecord[],
): { column: Column; verdict: string; criteria: { name: string; level: string }[] }[] {
  const records = [...scores, ...outcomes];
  const levels = ['Not at all', 'Barely', 'Partly', 'Mostly', 'Fully'];
  return [...new Set(records.map((r) => r.board_column))].map((column) => {
    const round = Math.max(...records.filter((r) => r.board_column === column).map((r) => r.round));
    const latest = (r: StudyRecord) => r.board_column === column && r.round === round;
    const outcome = outcomes.filter(latest).at(-1);
    return {
      column,
      verdict: String(outcome?.body.verdict ?? ''),
      criteria: scores.filter(latest).map((r) => ({
        name: String(r.body.criterion ?? ''),
        level: levels[Math.round(Number(r.body.level)) - 1] ?? '',
      })),
    };
  });
}
