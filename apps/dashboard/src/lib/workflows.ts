import type { WorkflowSummary } from '../api/types';
import { matchesQuery } from './search';

export function domainLabel(domain: string) {
  const text = domain.replace(/-/g, ' ');
  return text.charAt(0).toUpperCase() + text.slice(1);
}

export function domainEmoji(domain: string) {
  const first = domain.split('-')[0];
  const emoji: Record<string, string> = {
    earth: '🌍',
    life: '🧬',
    physical: '🔭',
    mathematical: '📐',
    engineering: '⚙️',
  };
  return (first && emoji[first]) || '🧪';
}

// Groups by domain sorted by label, cards sorted by name, filtered on name,
// description and tags.
export function byDomain(list: WorkflowSummary[], query: string) {
  const groups = new Map<string, WorkflowSummary[]>();
  for (const workflow of list) {
    if (!matchesQuery(`${workflow.name} ${workflow.description} ${workflow.tags.join(' ')}`, query))
      continue;
    groups.set(workflow.domain, [...(groups.get(workflow.domain) ?? []), workflow]);
  }
  return [...groups]
    .map(([domain, workflows]) => ({
      domain,
      label: domainLabel(domain),
      emoji: domainEmoji(domain),
      workflows: workflows.sort((a, b) => a.name.localeCompare(b.name)),
    }))
    .sort((a, b) => a.label.localeCompare(b.label));
}

// Drops HTML comments, which the README and instruction files use for notes
// to authors and the markdown renderer would show as text.
export function stripComments(text: string) {
  return text.replace(/<!--[\s\S]*?-->/g, '');
}
