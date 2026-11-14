export interface ManualDecisionDraft {
  title: string;
  context: string;
  deadline: string | null;
  card_id: string | null;
}
export const blankManualDecisionDraft = (): ManualDecisionDraft => ({
  title: '',
  context: '',
  deadline: null,
  card_id: null,
});
const fields = ['title', 'context', 'deadline', 'card_id'] as const;
function validDate(value: string) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const date = new Date(value + 'T00:00:00Z');
  return Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === value;
}
export function manualDecisionDraftError(draft: ManualDecisionDraft, unfinished = false): string {
  if (
    (!unfinished && !draft.title.trim()) ||
    /[\r\n]/.test(draft.title) ||
    [...draft.title].length > 200
  )
    return 'Use a one-line title of 1–200 characters.';
  if ([...draft.context].length > 4000) return 'Context can contain up to 4,000 characters.';
  if (draft.deadline !== null && !validDate(draft.deadline)) return 'Choose a valid deadline date.';
  return '';
}
export function parseManualDecisionDraft(
  value: unknown,
  unfinished = false,
): ManualDecisionDraft | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const draft = value as Record<string, unknown>;
  if (Object.keys(draft).length !== 4 || fields.some((key) => !(key in draft))) return null;
  if (
    typeof draft.title !== 'string' ||
    typeof draft.context !== 'string' ||
    (draft.deadline !== null && typeof draft.deadline !== 'string')
  )
    return null;
  if (
    draft.card_id !== null &&
    (typeof draft.card_id !== 'string' ||
      !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(draft.card_id))
  )
    return null;
  const result = draft as unknown as ManualDecisionDraft;
  return manualDecisionDraftError(result, unfinished) ? null : { ...result };
}
export function parseManualDecisionProposal(
  value: unknown,
): { message: string; draft: ManualDecisionDraft } | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const proposal = value as Record<string, unknown>;
  if (Object.keys(proposal).length !== 2 || typeof proposal.message !== 'string') return null;
  const draft = parseManualDecisionDraft(proposal.draft);
  return draft ? { message: proposal.message, draft } : null;
}
// Only fields still equal to the frozen turn input can receive the proposal.
export function mergeManualDecisionDraft(
  current: ManualDecisionDraft,
  proposal: unknown,
  input: ManualDecisionDraft = current,
): ManualDecisionDraft {
  const valid = parseManualDecisionDraft(proposal);
  if (!valid) return { ...current };
  return Object.fromEntries(
    fields.map((key) => [key, current[key] === input[key] ? valid[key] : current[key]]),
  ) as unknown as ManualDecisionDraft;
}
export function manualDeadlineLabel(
  deadline: string | null | undefined,
  timezone: string,
  now = new Date(),
): string {
  if (!deadline) return '';
  const parts = new Intl.DateTimeFormat('en', {
    timeZone: timezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(now);
  const part = (name: string) => parts.find((p) => p.type === name)!.value;
  const today = `${part('year')}-${part('month')}-${part('day')}`;
  return today > deadline ? `Overdue · ${deadline}` : `Due ${deadline}`;
}

export interface ManualDecisionTurn {
  user_request: string;
  current_draft: ManualDecisionDraft;
  harness: 'claude' | 'codex';
  model: string;
  request_id: string;
}
