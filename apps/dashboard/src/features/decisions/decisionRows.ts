import { computed } from 'vue';
import { matchesDecisionFilters } from './decisionFilters';
import { workspaceId } from '../../api/session';
import { useCards, useDecisions, useMe } from '../../api/queries';
import type { DecisionItem } from '../../api/types';
import { agentDecisionParts, decisionKindOf, inboxText, inboxTitle } from '../../lib/studies';

// The decisions a person sees in this workspace, newest first, with the title
// and line each one is shown under.
export function useDecisionRows() {
  const open = useDecisions(false);
  const past = useDecisions(true);
  const cards = useCards();
  const me = useMe();
  const kindOf = (item: DecisionItem) => decisionKindOf(item.kind);
  const titleOf = (item: DecisionItem) =>
    item.kind === 'manual_decision'
      ? (item.title ?? '')
      : item.kind === 'agent_decision'
        ? agentDecisionParts(item.text).title
        : inboxTitle(item, cards.data.value?.find((card) => card.id === item.card_id)?.title);
  const line = (item: DecisionItem) =>
    item.kind === 'manual_decision'
      ? item.context || 'Private note'
      : item.kind === 'agent_decision'
        ? agentDecisionParts(item.text).body || kindOf(item).label
        : (inboxText(item, titleOf(item)) ?? `${kindOf(item).label}: ${item.text}`);
  const shown = computed(() => {
    const items = new Map(
      [...(open.data.value ?? []), ...(past.data.value ?? [])].map((item) => [item.id, item]),
    );
    return [...items.values()]
      .filter(
        (item) =>
          item.workspace_id === workspaceId.value &&
          (item.kind !== 'manual_decision' ||
            (item.user_id === me.data.value?.id && !open.error.value && !past.error.value)) &&
          matchesDecisionFilters(item),
      )
      .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
  });
  return {
    shown,
    kindOf,
    titleOf,
    line,
    pending: computed(() => open.isPending.value || past.isPending.value),
    error: computed(() => open.error.value ?? past.error.value),
  };
}
