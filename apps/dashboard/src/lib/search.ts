import type { Card, Research } from '../api/types';

// Case-insensitive substring match; an empty query matches everything.
export function matchesQuery(text: string, query: string) {
  return text.toLowerCase().includes(query.toLowerCase());
}

export interface PaletteEntry {
  id: string;
  kind: 'research' | 'study';
  label: string;
  title: string;
  subtitle: string;
  path: string;
  updated: string;
  research?: string;
  stage?: string;
  state?: string;
}

export function paletteEntries(
  researches: Research[],
  studies: Card[],
  prefix: string,
): PaletteEntry[] {
  const names = new Map(researches.map((research) => [research.id, research.name]));
  return [
    ...researches.map((research): PaletteEntry => ({
      id: `research-${research.id}`,
      kind: 'research',
      label: `Go to ${research.name}`,
      title: research.name,
      subtitle: 'Research',
      path: `/projects/${research.id}`,
      updated: research.updated_at,
    })),
    ...studies.map((study): PaletteEntry => {
      const code = `${prefix}-${study.number}`;
      return {
        id: `study-${study.id}`,
        kind: 'study',
        label: `${code} · ${study.title}`,
        title: code,
        subtitle: study.title,
        path: `/studies/${study.id}`,
        research: study.research_id ? names.get(study.research_id) : undefined,
        stage: study.board_column,
        state: study.state,
        updated: study.updated_at,
      };
    }),
  ];
}

function matchScore(label: string, query: string): number {
  if (label === query) return 100;
  if (label.startsWith(query)) return 80;
  const index = label.indexOf(query);
  if (index >= 0) return 60 - Math.min(index, 40) / 2;
  let matched = 0;
  for (const character of label) {
    if (character === query[matched]) matched++;
    if (matched === query.length) return 10;
  }
  return 0;
}

export function rankPaletteEntries(entries: PaletteEntry[], query: string): PaletteEntry[] {
  const normalized = query.trim().toLowerCase();
  if (!normalized) return entries;
  return entries
    .map((entry) => ({ entry, score: matchScore(entry.label.toLowerCase(), normalized) }))
    .filter(({ score }) => score > 0)
    .sort((left, right) => right.score - left.score)
    .map(({ entry }) => entry);
}

// The top entry stays selected until someone moves the selection. After that a
// refreshed list keeps their entry selected wherever it moved, and falls back
// to the top once it is gone.
export function paletteSelection(
  previous: PaletteEntry[],
  next: PaletteEntry[],
  active: number,
  keep: boolean,
): number {
  if (!keep) return 0;
  const chosen = previous[active]?.id;
  const index = next.findIndex((entry) => entry.id === chosen);
  return index < 0 ? 0 : index;
}
