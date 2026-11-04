import type {
  BlockKind,
  Card,
  CareerStage,
  Column,
  AgentRun,
  Comment,
  DecisionItem,
  LykEvent,
  StudyRecord,
} from '../api/types';

// A workspace URL the server accepts (^[a-z0-9][a-z0-9-]{1,38}$), or '' when the
// name has too few letters or digits to make one.
export function slugFrom(name: string): string {
  const slug = name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 39)
    .replace(/-+$/, '');
  return slug.length >= 2 ? slug : '';
}

// The prefix the server shows when a workspace sets none: the slug's letters,
// uppercased, the first four, at least two.
export function prefixFromSlug(slug: string): string {
  const letters = slug
    .replace(/[^a-z]/g, '')
    .toUpperCase()
    .slice(0, 4);
  return letters.length >= 2 ? letters : 'LYK';
}

const firsts = ['Amber', 'Basalt', 'Cedar', 'Delta', 'Ember', 'Fjord', 'Granite', 'Harbor'];
const seconds = ['Lab', 'Group', 'Bench', 'Institute', 'Collective', 'Studio'];
export function randomWorkspaceName(pick: () => number = Math.random): string {
  const a = firsts[Math.floor(pick() * firsts.length)]!;
  const b = seconds[Math.floor(pick() * seconds.length)]!;
  return `${a} ${b}`;
}

export const careerStageLabels: Record<Exclude<CareerStage, ''>, string> = {
  undergraduate: 'Undergraduate student',
  masters: "Master's student",
  phd: 'Doctoral researcher',
  postdoc: 'Postdoc',
  research_staff: 'Research staff',
  faculty: 'Faculty',
  industry: 'Industry researcher',
  other: 'Other',
};

// The four stages, one line each, as the Welcome step introduces them.
export const stageIntro = [
  { name: 'Grounding', line: 'Find the literature and data that bear on the question.' },
  { name: 'Hypotheses', line: 'Form rival hypotheses that an experiment can tell apart.' },
  { name: 'Experimentation', line: 'Run the analysis and keep reproducible results.' },
  { name: 'Validation', line: 'Independent reviewers test the findings.' },
];

export type BoardColumn = Column | 'blocked';

export const boardColumns: { id: BoardColumn; name: string }[] = [
  { id: 'grounding', name: 'Grounding' },
  { id: 'hypotheses', name: 'Hypotheses' },
  { id: 'experimentation', name: 'Experimentation' },
  { id: 'validation', name: 'Validation' },
  { id: 'done', name: 'Done' },
  { id: 'blocked', name: 'Blocked' },
];
export const stages: Column[] = ['grounding', 'hypotheses', 'experimentation', 'validation'];
export const columnName = (id: string) => boardColumns.find((c) => c.id === id)?.name ?? id;

// A study scored low twice, or flagged for a person, sits in Blocked; its
// board_column still names the column it is stuck in, and it returns there
// when the block clears.
export function boardColumnOf(card: Card): BoardColumn {
  return !card.ending && (card.state === 'blocked' || card.flag === 'escalated')
    ? 'blocked'
    : card.board_column;
}

export const studyDisplayTitle = (title: string) => title.replace(/\s+\[[a-z][0-9a-f]{12}\]$/i, '');

export const studyNumber = (ws: { display_prefix: string } | undefined, card: { number: number }) =>
  `${ws?.display_prefix || 'LYK'}-${card.number}`;

export function stateLabel(card: Card): string {
  if (card.ending) return '';
  return (
    { working: 'Working', done: 'Done', blocked: 'Blocked', stopped: 'Stopped' }[card.state] ?? ''
  );
}
export function endLabel(card: Card): string {
  if (card.ending === 'released')
    return card.end_reason ? `Released · ${card.end_reason}` : 'Released';
  if (card.ending === 'stopped') return 'Stopped';
  return '';
}
export function roleLabel(role: string): string {
  const words = role.replace(/-/g, ' ');
  return words ? words[0]!.toUpperCase() + words.slice(1) : '';
}
// Validation's objector and rerun are runs Lykeion starts itself, with no agent.
export function systemRun(role: string): boolean {
  return role === 'objector' || role === 'rerun';
}
export function timeAgo(iso: string, now = Date.now()): string {
  const s = (now - Date.parse(iso)) / 1000;
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

// Each column shows the newest studies first.
export function groupByColumn(cards: Card[]): Record<BoardColumn, Card[]> {
  const out = Object.fromEntries(boardColumns.map((c) => [c.id, [] as Card[]])) as Record<
    BoardColumn,
    Card[]
  >;
  for (const c of cards) out[boardColumnOf(c)].push(c);
  for (const list of Object.values(out))
    list.sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at) || b.number - a.number);
  return out;
}

export const filterCategories = ['Column', 'State', 'Flag', 'Owner'] as const;
export type FilterCategory = (typeof filterCategories)[number];
export type StudyFilters = Partial<Record<FilterCategory, string[]>>;
export const filterOptions: Record<
  Exclude<FilterCategory, 'Owner'>,
  { value: string; label: string }[]
> = {
  Column: boardColumns.map((c) => ({ value: c.id, label: c.name })),
  State: [
    { value: 'working', label: 'Working' },
    { value: 'done', label: 'Done' },
    { value: 'blocked', label: 'Blocked' },
  ],
  Flag: [
    { value: 'queued', label: 'Queued' },
    { value: 'escalated', label: 'Escalated' },
  ],
};
function valuesOf(card: Card, category: FilterCategory): string[] {
  if (category === 'Column') return [boardColumnOf(card)];
  if (category === 'State') return [card.state];
  if (category === 'Owner') return [card.owner_id];
  return [...(card.queued ? ['queued'] : []), ...(card.flag ? [card.flag] : [])];
}
// A study matches when, in every category with a selection, it has one of the selected values.
export function matchesFilters(card: Card, filters: StudyFilters): boolean {
  return filterCategories.every((category) => {
    const chosen = filters[category];
    return !chosen?.length || valuesOf(card, category).some((v) => chosen.includes(v));
  });
}

const str = (v: unknown) => (typeof v === 'string' ? v : '');
// What a record holds, in one line: its claim, its first line of text, the
// dataset or search it names, or its reason.
export function recordLine(r: StudyRecord) {
  const b = r.body;
  const firstLine =
    str(b.text)
      .replace(/^#+\s*/, '')
      .split('\n')[0] ?? '';
  const text =
    str(b.claim) ||
    firstLine ||
    str(b.accession) ||
    str(b.query) ||
    str(b.reason) ||
    str(b.check) ||
    r.kind;
  return { key: r.key || r.kind, text, source: str(b.source_id) };
}

const steered: Record<string, () => string> = {
  resume: () => 'Resumed',
};
export function eventText(e: LykEvent): string {
  const p = e.payload ?? {};
  switch (e.type) {
    case 'card.created':
      return 'Created';
    case 'card.started':
      return `Started in ${columnName(str(p.column))}`;
    case 'card.edited':
      return 'Edited';
    case 'card.escalated':
      return 'Escalated: waiting for a person';
    case 'card.steered':
      return steered[str(p.path).split('/').pop() ?? '']?.() ?? '';
    default:
      return '';
  }
}

export type TranscriptEntry =
  | { kind: 'comment'; at: string; comment: Comment }
  | { kind: 'run'; at: string; run: AgentRun }
  | { kind: 'jev'; at: string; text: string; column: Column }
  | { kind: 'event'; at: string; text: string; id: number };
export function transcript(
  comments: Comment[],
  runs: AgentRun[],
  events: LykEvent[],
  jev: { at: string; text: string; column: Column }[] = [],
): TranscriptEntry[] {
  const out: TranscriptEntry[] = [
    ...comments.map((comment) => ({ kind: 'comment' as const, at: comment.created_at, comment })),
    ...runs.map((run) => ({ kind: 'run' as const, at: run.started_at ?? run.created_at, run })),
    ...events
      .map((e) => ({
        kind: 'event' as const,
        at: e.created_at,
        text: eventText(e),
        id: e.id,
      }))
      .filter((e) => e.text),
    ...jev.map((entry) => ({ ...entry, kind: 'jev' as const })),
  ];
  return out.sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
}

const blockLabels: Record<BlockKind, string> = {
  '': '',
  low_score: 'low score',
  decision: 'Waiting on a decision',
  parked: 'Parked',
};
export const blockLabel = (kind: BlockKind): string => blockLabels[kind];

const couldNotDo = /the agent could not do this column: ([\s\S]+)$/;

// Why a Blocked study waits for its owner, from its open item: the reason its
// agent gave for not doing the column, or the failure that stopped it.
export function blockedMessage(card: Card): string {
  if (card.block_kind === 'low_score')
    return 'Scored low twice. Tell the Team what to fix to redo this column.';
  if (card.block_kind === 'decision')
    return 'The agent asked you a question: answer it in Decisions.';
  if (card.block_kind === 'parked') return 'Parked: comment to redo this column.';
  const hold = card.hold_text ?? '';
  const reason = couldNotDo.exec(hold)?.[1];
  if (reason) return `The agent could not do this column: ${reason}`;
  return hold.replace(/^Study #\d+(?:'s)?:? ?/, '') || 'This study is waiting for you.';
}

// The board chip of a Blocked study: the kind of hold, never a bare "escalated".
export function blockBadge(card: Card): string {
  if (card.block_kind) return blockLabel(card.block_kind);
  const hold = card.hold_text ?? '';
  if (couldNotDo.test(hold)) return 'could not do';
  if (hold.includes(' column stopped: ')) return 'run failed';
  return 'escalated';
}

export type ComposerAction = { kind: 'comment' } | { kind: 'stop' };

// A Blocked study's picker: Comment, which redoes the column, for its owner,
// and for anyone on a parked study. A study waiting on its agent's question
// is answered in Decisions, not by a comment. Everyone else, and everyone on
// an unblocked study, gets [] and keeps the ordinary picker.
export function blockedActions(card: Card, viewer: string | undefined): ComposerAction['kind'][] {
  if (card.ending || card.state !== 'blocked' || card.block_kind === 'decision') return [];
  return card.block_kind === 'parked' || card.owner_id === viewer ? ['comment'] : [];
}

export function actionLabel(a: ComposerAction): string {
  return { comment: 'Comment', stop: 'Stop' }[a.kind];
}

// A score level to one decimal; Jev's levels are floats like 4.779999999999999.
export const levelText = (level: number): string => String(Math.round(level * 10) / 10);

// What redid a low-scored attempt: the automatic redo (the card's redos) and
// the redos the owner's comments started. The column's attempts run from its
// first round (a column advance also raises the card's round) to the current one.
export function redoLabel(redos: number, round: number, firstRound: number): string {
  const afterReply = Math.max(0, round - firstRound - redos);
  if (redos >= 1 && afterReply >= 1) return 'Redone once, then after your reply';
  if (redos >= 1) return 'Redone once';
  return afterReply >= 1 ? 'Redone after your reply' : '';
}

export interface FlowView {
  scores: { criterion: string; level: number }[];
  verdict: string;
  steps: { key: string; text: string; self: boolean; detail: string }[];
  audit: { text: string; findings: string[] } | null;
  calls: { key: string; server: string; tool: string; at: string; answer: string }[];
}

const strings = (v: unknown): string[] =>
  Array.isArray(v)
    ? v.map((x) => (typeof x === 'string' ? x : str((x as Record<string, unknown>)?.text)))
    : [];

// The flow's own records of a column, in the order they were written: the
// steps and calls of the latest attempt (round), the auditor's note, and the
// latest scored attempt.
export function flowView(recs: StudyRecord[]): FlowView {
  const out: FlowView = { scores: [], verdict: '', steps: [], audit: null, calls: [] };
  const scored = new Map<number, FlowView['scores']>();
  let outcomeRound = -1;
  const latest = Math.max(0, ...recs.map((r) => r.round));
  for (const r of recs) {
    const b = r.body;
    if (
      r.round !== latest &&
      (r.kind === 'delegation' || r.kind === 'self_step' || r.kind === 'call')
    )
      continue;
    if (r.kind === 'delegation')
      out.steps.push({
        key: r.key,
        text: `Leader → ${str(b.member)}: ${str(b.step)}`,
        self: false,
        detail: '',
      });
    else if (r.kind === 'self_step')
      out.steps.push({
        key: r.key,
        text: 'The leader did this itself — no member fit',
        self: true,
        detail: str(b.step),
      });
    else if (r.kind === 'audit') out.audit = { text: str(b.text), findings: strings(b.findings) };
    else if (r.kind === 'call')
      out.calls.push({
        key: r.key,
        server: str(b.server),
        tool: str(b.tool),
        at: str(b.at),
        answer: str(b.answer),
      });
    else if (r.kind === 'score')
      scored.set(r.round, [
        ...(scored.get(r.round) ?? []),
        { criterion: str(b.criterion), level: Number(b.level) },
      ]);
    else if (r.kind === 'outcome' && r.round >= outcomeRound) {
      outcomeRound = r.round;
      out.verdict = str(b.verdict);
    }
  }
  out.scores = scored.get(outcomeRound) ?? [];
  return out;
}

// An Inbox item in the words the researcher reads, or null when the server's
// sentence is to be shown as it is. The criterion and the need are lifted out
// of the server's sentence (see the Go functions that write them).
// How many studies have an agent at work, and how many have a run waiting
// on a machine; a run queued for a free slot is neither.
export function boardActivity(
  cards: { role: string; queued: boolean; waiting?: string; ending: string }[],
): { working: number; waiting: number } {
  const live = cards.filter((c) => c.role && !c.ending);
  return {
    working: live.filter((c) => !c.queued && !c.waiting).length,
    waiting: live.filter((c) => c.waiting).length,
  };
}

// An Inbox item's title: its study's, or what the item is when it has no
// study, as a machine's wait has none.
export function inboxTitle(item: { kind: DecisionItem['kind'] }, study?: string): string {
  if (study) return study;
  if (item.kind === 'autoresearch_report') return 'Autoresearch';
  if (item.kind === 'waiting_for_machine') return 'Waiting for machine';
  return 'Study';
}

// A machine's wait, or a study whose run its machines kept failing: the items
// its owner resolves. Every other needs_decision opens its study.
export const isMachineWait = (item: {
  kind: DecisionItem['kind'];
  cause?: string | null;
}): boolean =>
  item.kind === 'waiting_for_machine' ||
  item.kind === 'machine_offline' ||
  (item.kind === 'needs_decision' && item.cause === 'machine_failures');

// What an agent's decision is about, and the stop kinds a decision can be in.
export const decisionKindLabel: Record<string, string> = {
  scope: 'Scope',
  change_question: 'Change the question',
  ethics: 'Ethics or sensitive data',
  cost: 'Cost above the limit',
  access: 'Missing access',
  blocked: 'Blocked',
  low_score: 'Blocked',
  parked: 'Parked',
};

// How each item kind reads in the rail and on a decision; the lookup falls
// back for a kind the dashboard does not know yet.
const decisionKinds: Record<DecisionItem['kind'], { label: string; mark: string }> = {
  agent_decision: { label: 'Agent decision', mark: '?' },
  autoresearch_card: { label: 'Autoresearch study', mark: '↻' },
  autoresearch_report: { label: 'Autoresearch report', mark: '↻' },
  middle_score: { label: 'Middle score', mark: '~' },
  blocked: { label: 'Blocked', mark: '!' },
  no_tool: { label: 'No tool', mark: '?' },
  scoring_unavailable: { label: 'Scoring unavailable', mark: '!' },
  waiting_for_machine: { label: 'Waiting for machine', mark: '!' },
  machine_offline: { label: 'Machine offline', mark: '!' },
  needs_decision: { label: 'Needs a decision', mark: '?' },
  open_doubts: { label: 'Passed with open doubts', mark: '?' },
  done: { label: 'Done', mark: '✓' },
};
export const decisionKindOf = (kind: string): { label: string; mark: string } =>
  (decisionKinds as Record<string, { label: string; mark: string }>)[kind] ?? {
    label: 'Decision',
    mark: '?',
  };

// The decide-by choices a person can set, as the page names them.
export const decideByLabel: Record<string, string> = {
  today: 'Today',
  this_week: 'This week',
  whenever: 'Whenever',
};

// An agent's decision reads "<title>\n\n<body>".
export function agentDecisionParts(text: string): { title: string; body: string } {
  const [title, ...rest] = text.split('\n\n');
  return { title: title ?? '', body: rest.join('\n\n') };
}

// Who raised a decision: the agent (and the member it asked for), or Lykeion.
export function decisionRaiser(item: { raised_by: string; from_member: string }): string {
  if (item.raised_by !== 'agent') return 'Lykeion';
  return item.from_member ? `Agent (${item.from_member})` : 'Agent';
}

// What became of a decision that is no longer open.
export function decisionOutcome(item: {
  status: string;
  chosen_option: string;
  default_option: string;
  options: { id: string; label: string }[] | null;
}): string {
  const label = (id: string) => item.options?.find((o) => o.id === id)?.label ?? id;
  if (item.status === 'decided') return label(item.chosen_option);
  if (item.status === 'expired')
    return item.default_option === 'park'
      ? 'Parked: expired without a decision'
      : `Expired without a decision${item.default_option ? ` (${label(item.default_option)})` : ''}`;
  return item.status === 'dismissed' ? 'Dismissed' : '';
}

// What a decision or a pass with doubts is about, by the item's cause.
const decisionLabels: Record<string, string> = {
  all_withdrawn: 'Everything withdrawn',
  validation_invalid: 'Validation found invalid results',
};

// Validation's final statuses, one line per hypothesis: "<hypothesis> —
// <status> · <first reason>". A route-back leaves earlier rounds' statuses
// live, so only the newest round that has final statuses is shown.
export function validationLines(records: StudyRecord[]): string[] {
  const final = records.filter(
    (r) => r.kind === 'validation_status' && !r.superseded_at && r.body.final === true,
  );
  const newest = Math.max(...final.map((r) => r.round));
  return final
    .filter((r) => r.round === newest)
    .map((r) => {
      const reasons = Array.isArray(r.body.reasons) ? r.body.reasons : [];
      const what = str(r.body.hypothesis).trim();
      const clipped = what.length > 60 ? `${what.slice(0, 60)}…` : what;
      const verdict = [str(r.body.status), ...reasons.slice(0, 1).map(str)].join(' · ');
      return clipped ? `${clipped} — ${verdict}` : verdict;
    });
}

export function inboxText(
  item: { kind: DecisionItem['kind']; text: string; cause?: string | null },
  study: string,
): string | null {
  switch (item.kind) {
    case 'open_doubts':
      return `${study}: Passed with open doubts`;
    case 'needs_decision': {
      const label = item.cause ? decisionLabels[item.cause] : undefined;
      return label ? `${study}: ${label}` : null;
    }
    case 'middle_score': {
      const criterion = /“([^”]+)”/.exec(item.text)?.[1];
      if (!criterion) return null;
      const finished = item.text.includes(' and the study finished;');
      return `${study} ${finished ? 'finished' : 'moved on'} with a middling score: ${criterion}`;
    }
    case 'blocked': {
      // A column stopped by its agent's failed runs: the server's text names why.
      if (item.text.includes(' column stopped: ')) return null;
      const column = /'s (\w+) column failed Jev's review (?:again|twice)/.exec(item.text)?.[1];
      return column
        ? `${study}'s ${columnName(column)} column failed Jev's review again and is waiting for you`
        : `${study} scored low twice and is waiting for you`;
    }
    case 'no_tool': {
      const need = /what it needed: ([\s\S]+)$/.exec(item.text)?.[1];
      return need ? `${study}'s agent found no skill or connector for: ${need}` : null;
    }
    case 'scoring_unavailable':
      return `${study} couldn't be scored; Lykeion will retry`;
    case 'done':
      return `${study} is done`;
    default:
      return null;
  }
}

// An assignee as the picker holds it: a Team names a Team per stage, and
// agent_id works the stages no Team covers.
export type Picked =
  | { type: 'agent'; id: string }
  | { type: 'team'; teams: Record<string, string>; agent_id?: string };
export type Range = { from: string; to: string };

// Whether a study runs the stage: its range when the form sends one, else the
// lone Team's column.
export function coversStage(stage: Column, range: Range | null, teamColumns: string[]): boolean {
  if (!range) return teamColumns.includes(stage);
  const at = (s: string) => stages.indexOf(s as Column);
  return at(range.from) <= at(stage) && at(stage) <= at(range.to);
}

// Use the current round's recorded conclusion, or its latest assigned step.
// The complete records remain available in the study's evidence disclosure.
export function studyOverview(records: StudyRecord[], round: number): string {
  const current = records
    .filter((r) => r.round === round && !r.superseded_at)
    .reverse()
    .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
  const summary = current.find((r) => r.kind === 'summary' && str(r.body.text).trim());
  const step = current.find(
    (r) => ['delegation', 'self_step'].includes(r.kind) && str(r.body.step).trim(),
  );
  const text = summary ? str(summary.body.text) : step ? str(step.body.step) : '';
  return text.trim().split('\n')[0] || 'No work recorded yet.';
}

// Older runs stored coordination envelopes as their assignment. Present the
// task itself rather than repeating the embedded records and prompt framing.
export function readableAssignment(
  run: AgentRun,
  study?: { title: string; question: string },
): string {
  const text = run.assignment.trim();
  const title = study?.title?.trim();
  const question = study?.question?.trim();
  if (title && question && text === `${title}\n\n${question}`) return question;
  const stage = columnName(run.board_column);
  if (
    run.role === 'auditor' &&
    text.startsWith(
      'The leader declared the column done. Audit its summary and the records it cites:',
    )
  )
    return `Audit the ${stage} summary and its cited evidence.`;
  const member = text.match(/^([a-z][a-z0-9-]*) (finished the step you named\.)/);
  const interrupted = text.match(
    /^([a-z][a-z0-9-]*)'s run (failed:|was cancelled before it finished the step\.)/,
  );
  if (
    run.role === 'leader' &&
    (member || interrupted) &&
    (text.includes('What it wrote:') || text.includes('It wrote no records.'))
  ) {
    const opening = member
      ? `${member[1]} finished the step you named.`
      : `${interrupted![1]}’s run ${interrupted![2] === 'failed:' ? 'failed' : 'was cancelled'} before finishing the step.`;
    const noRecords = text.includes('It wrote no records.') ? ' It wrote no records.' : '';
    return `${opening}${noRecords}\n\nReview the recorded work and choose the next step or finish ${stage}.`;
  }
  return text;
}
