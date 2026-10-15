import type { Card, CareerStage, Column } from '../api/types';

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
