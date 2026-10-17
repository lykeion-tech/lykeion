import type {
  BlockKind,
  Card,
  CareerStage,
  Choice,
  Column,
  AgentRun,
  Comment,
  Gate,
  GateCheck,
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

// The five stages, one line each, as the Welcome step introduces them.
export const stageIntro = [
  { name: 'Grounding', line: 'Find the literature and data that bear on the question.' },
  { name: 'Hypotheses', line: 'Form rival hypotheses that an experiment can tell apart.' },
  { name: 'Experimentation', line: 'Run the analysis and keep reproducible results.' },
  { name: 'Validation', line: 'Independent reviewers test the findings.' },
  { name: 'Reporting', line: 'Release the result with traceable evidence.' },
];

export type BoardColumn = Column | 'blocked';

export const boardColumns: { id: BoardColumn; name: string }[] = [
  { id: 'backlog', name: 'Backlog' },
  { id: 'grounding', name: 'Grounding' },
  { id: 'hypotheses', name: 'Hypotheses' },
  { id: 'experimentation', name: 'Experimentation' },
  { id: 'validation', name: 'Validation' },
  { id: 'blocked', name: 'Blocked' },
  { id: 'reporting', name: 'Reporting' },
  { id: 'done', name: 'Done' },
];
export const stages: Column[] = [
  'grounding',
  'hypotheses',
  'experimentation',
  'validation',
  'reporting',
];
export const columnName = (id: string) => boardColumns.find((c) => c.id === id)?.name ?? id;

// An active study waiting on a person sits in Blocked; its board_column still
// names the column it is stuck in, and it returns there when the flag clears.
export function boardColumnOf(card: Card): BoardColumn {
  return !card.ending && (card.flag === 'escalated' || card.flag === 'out_of_budget')
    ? 'blocked'
    : card.board_column;
}

export const studyNumber = (ws: { display_prefix: string } | undefined, card: { number: number }) =>
  `${ws?.display_prefix || 'LYK'}-${card.number}`;

export function stateLabel(card: Card): string {
  if (card.ending || card.board_column === 'backlog') return '';
  return { working: 'Working', in_review: 'In review', ready: 'Ready' }[card.state] ?? '';
}
export function endLabel(card: Card): string {
  if (card.ending === 'released')
    return card.end_reason ? `Released · ${card.end_reason}` : 'Released';
  if (card.ending === 'stopped') return `Stopped · ${card.end_reason}`;
  return '';
}
export function roleLabel(role: string): string {
  const words = role.replace(/-/g, ' ');
  return words ? words[0]!.toUpperCase() + words.slice(1) : '';
}
export function timeAgo(iso: string, now = Date.now()): string {
  const s = (now - Date.parse(iso)) / 1000;
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

// Each column lowest position first, then by number: the top of Backlog is the
// study WIP pull starts next.
export function groupByColumn(cards: Card[]): Record<BoardColumn, Card[]> {
  const out = Object.fromEntries(boardColumns.map((c) => [c.id, [] as Card[]])) as Record<
    BoardColumn,
    Card[]
  >;
  for (const c of cards) out[boardColumnOf(c)].push(c);
  for (const list of Object.values(out))
    list.sort((a, b) => a.position - b.position || a.number - b.number);
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
    { value: 'in_review', label: 'In review' },
    { value: 'ready', label: 'Ready' },
  ],
  Flag: [
    { value: 'queued', label: 'Queued' },
    { value: 'escalated', label: 'Escalated' },
    { value: 'out_of_budget', label: 'Out of budget' },
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

// Place the moved study between its new neighbours, or one past either end.
export function positionAt(list: Card[], movedId: string, index: number): number {
  const rest = list.filter((c) => c.id !== movedId);
  const before = rest[index - 1];
  const after = rest[index];
  if (before && after) return (before.position + after.position) / 2;
  if (after) return after.position - 1;
  if (before) return before.position + 1;
  return list.find((c) => c.id === movedId)?.position ?? 0;
}

export const gateOf = (r: StudyRecord) => r.body as unknown as Gate;

// Jev's verdict bands.
export const jevThresholds = { pass: 0.7, fail: 0.35 };

// A check whose question was answered in Jev's place, or by a reviewer on the
// authors' own model family, says so: the live choice record for that
// question, in the gate's column, holds who judged it.
function standIn(c: GateCheck, records: StudyRecord[]): string {
  const choice = records
    .filter((r) => r.kind === 'choice' && !r.superseded_at)
    .map((r) => r.body as unknown as Choice)
    .find((ch) => ch.question === c.id || (c.question && ch.question === c.question));
  if (choice?.independence === 'same_family')
    return 'judged by the reviewer (stand-in, same model family)';
  return choice?.judge === 'fallback' ? 'judged by the reviewer (stand-in)' : '';
}

export function checkLine(c: GateCheck, records: StudyRecord[] = []) {
  const tone: 'ok' | 'bad' | 'att' =
    c.result === 'pass' ? 'ok' : c.result === 'fail' ? 'bad' : 'att';
  return {
    mark: { ok: '✓', bad: '✗', att: '!' }[tone],
    tone,
    name: c.id,
    why: [c.note, standIn(c, records)].filter(Boolean).join(' · '),
    jev: c.judge === 'jev' && Boolean(c.question),
  };
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

const steered: Record<string, (p: Record<string, unknown>) => string> = {
  'send-back': (p) => `Sent back to ${columnName(str(p.column))}`,
  stop: () => 'Stopped',
  resume: () => 'Resumed',
  budget: () => 'Budget changed',
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
    case 'card.out_of_budget':
      return 'Out of budget: waiting for a person';
    case 'card.steered':
      return steered[str(p.path).split('/').pop() ?? '']?.(p) ?? '';
    case 'gate.decided':
      if (p.verdict === 'pass') return `Gate passed ${columnName(str(p.column))}`;
      if (p.verdict === 'cannot_proceed') return `Gate blocked the study: ${str(p.reason)}`;
      return `Gate routed the study back to ${columnName(str(p.to_column))}: ${str(p.reason)}`;
    default:
      return '';
  }
}

export type TranscriptEntry =
  | { kind: 'comment'; at: string; comment: Comment }
  | { kind: 'run'; at: string; run: AgentRun }
  | { kind: 'event'; at: string; text: string; id: number };
export function transcript(
  comments: Comment[],
  runs: AgentRun[],
  events: LykEvent[],
): TranscriptEntry[] {
  const out: TranscriptEntry[] = [
    ...comments.map((comment) => ({ kind: 'comment' as const, at: comment.created_at, comment })),
    ...runs.map((run) => ({ kind: 'run' as const, at: run.started_at ?? run.created_at, run })),
    ...events
      .map((e) => ({ kind: 'event' as const, at: e.created_at, text: eventText(e), id: e.id }))
      .filter((e) => e.text),
  ];
  return out.sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
}

// The stages shown collapsed above the current one.
export function earlierColumns(card: Card, gates: Gate[]): Column[] {
  const index = stages.indexOf(card.board_column);
  if (index >= 0) return stages.slice(0, index);
  return stages.filter((s) => gates.some((g) => g.column === s));
}

// The failed gates a study may use before it is blocked: route-backs, redos
// and revisions together.
export const routeBackLimit = (card: Card): number => card.cap;

const blockLabels: Record<BlockKind, string> = {
  '': '',
  cap: 'cap reached',
  infra: 'infrastructure',
  no_feasible_test: 'no feasible test',
  human_run: 'needs human-run test',
  too_small_to_split: 'too small to split',
  drift: 'drift',
};
export const blockLabel = (kind: BlockKind): string => blockLabels[kind];

export type ComposerAction =
  { kind: 'comment' } | { kind: 'send-back'; to: Column } | { kind: 'stop' } | { kind: 'resume' };

// Send back offers the stages before the study's own; Grounding has none.
export function sendBackTargets(card: Card): Column[] {
  const index = stages.indexOf(card.board_column);
  return card.ending || index < 0 ? [] : stages.slice(0, index);
}
export const canResume = (card: Card) => !card.ending && card.flag !== '';
export function actionLabel(a: ComposerAction): string {
  if (a.kind === 'send-back') return `Send back to ${columnName(a.to)}`;
  return { comment: 'Comment', stop: 'Stop', resume: 'Resume' }[a.kind];
}
