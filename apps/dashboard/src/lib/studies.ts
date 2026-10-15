import type { CareerStage } from '../api/types';

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
