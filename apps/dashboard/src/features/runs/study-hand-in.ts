import type { HypothesisMeasure, ReviewAttempt, StudyRecord } from '../../api/types';
import { objectFields, readMeasure } from '../../lib/measurement';

// The run list truncates latest_text, sometimes in the middle of a JSON string.
export function messagePreview(text: string): string {
  if (!/^\s*\{\s*"/.test(text)) return text;
  try {
    const value: unknown = JSON.parse(text);
    return value &&
      typeof value === 'object' &&
      !Array.isArray(value) &&
      typeof (value as Record<string, unknown>).answer === 'string'
      ? String((value as Record<string, unknown>).answer)
      : '';
  } catch {
    const match = /^\s*\{\s*"answer"\s*:\s*"((?:[^"\\]|\\.)*)/.exec(text);
    if (!match) return '';
    let value = match[1] ?? '';
    // The excerpt can end within a JSON escape or Unicode escape.
    for (let n = 0; n < 6; n++) {
      try {
        return JSON.parse('"' + value + '"') as string;
      } catch {
        value = value.slice(0, -1);
      }
    }
    return '';
  }
}

// These are recorded messages, not proof that any message is the accepted report.
export function structuredMessage(text: string): Record<string, unknown> | null {
  try {
    const value: unknown = JSON.parse(text);
    if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
    const record = value as Record<string, unknown>;
    return typeof record.answer === 'string' && typeof record.status === 'string' ? record : null;
  } catch {
    return null;
  }
}

function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value && typeof value === 'object') {
    const obj = value as Record<string, unknown>;
    return `{${Object.keys(obj)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${canonical(obj[key])}`)
      .join(',')}}`;
  }
  return JSON.stringify(value) ?? 'null';
}
export function matchesAccepted(text: string, handIn: Record<string, unknown>): boolean {
  try {
    return canonical(JSON.parse(text)) === canonical(handIn);
  } catch {
    return false;
  }
}

// member_result is the door's recorded response to an explicit delegation.
export function memberResult(tool: string, output: string) {
  if (!/(?:^|__)member_result$/.test(tool)) return null;
  const [first, ...rest] = output.split('\n\n');
  try {
    const result = JSON.parse(first ?? '') as Record<string, unknown>;
    const record = result.hand_in as Record<string, unknown> | null;
    const handIn = record?.hand_in as Record<string, unknown> | null;
    if (
      result.status !== 'done' ||
      typeof record?.agent_run_id !== 'string' ||
      typeof record?.attempt !== 'number' ||
      !handIn ||
      typeof handIn.answer !== 'string'
    )
      return null;
    return {
      handIn: handIn as Record<string, unknown>,
      agentRunId: record.agent_run_id,
      trailing: rest.join('\n\n').trim(),
    };
  } catch {
    return null;
  }
}

// Match accepted review items to the live declarations, including approvals
// whose deterministic checks were stored in a decision snapshot.
export function acceptedMeasures(
  hypotheses: StudyRecord[],
  reviews: ReviewAttempt[],
  outcomes: StudyRecord[],
  snapshots: StudyRecord[],
): Map<string, HypothesisMeasure> {
  const outcome = [...outcomes]
    .reverse()
    .find((r) => r.board_column === 'hypotheses' && r.role === 'server');
  if (!outcome) return new Map();
  const verdict = outcome.body.verdict;
  let items: unknown;
  if (verdict === 'pass' || verdict === 'carry') {
    items = [...reviews]
      .reverse()
      .find((a) => a.round === outcome.round && a.decision === verdict)?.items;
  } else if (verdict === 'approved_by_owner' && typeof outcome.body.decision_id === 'string') {
    const snapshot = [...snapshots]
      .reverse()
      .find(
        (r) =>
          r.board_column === 'hypotheses' &&
          r.role === 'server' &&
          r.round === outcome.round &&
          r.body.complete === true &&
          r.body.approval_decision === outcome.body.decision_id,
      );
    items = objectFields(snapshot?.body.checks)?.items;
  }
  if (!Array.isArray(items)) return new Map();
  const kept = new Set(
    items.flatMap((value) => {
      const item = objectFields(value);
      return item?.kind === 'hypothesis' &&
        typeof item.key === 'string' &&
        (item.status === 'pass' || item.status === 'unclear')
        ? [item.key]
        : [];
    }),
  );
  const measures = new Map<string, HypothesisMeasure>();
  const conflicts = new Set<string>();
  const seen = new Set<string>();
  for (const r of [...hypotheses].reverse()) {
    if (r.board_column !== 'hypotheses' || r.superseded_at || typeof r.body.text !== 'string')
      continue;
    const identity = r.body.text.trim().replace(/\s+/g, ' ').toLowerCase();
    if (seen.has(identity)) continue;
    seen.add(identity);
    if ((r.role !== 'agent' && r.role !== 'leader') || !kept.has(r.key)) continue;
    const measure = readMeasure(r.body.measure);
    if (!measure) continue;
    const existing = measures.get(measure.id);
    if (existing && (existing.name !== measure.name || existing.unit !== measure.unit))
      conflicts.add(measure.id);
    else measures.set(measure.id, measure);
  }
  for (const id of conflicts) measures.delete(id);
  return measures;
}
