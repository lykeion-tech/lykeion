import { reactive } from 'vue';
import type { DecisionItem } from '../../api/types';

export type DecisionDateFilter = 'all' | 'today' | 'yesterday' | 'week' | 'month' | 'custom';
export const decisionFilters = reactive({
  type: 'all' as 'all' | 'questions',
  date: 'all' as DecisionDateFilter,
  from: '',
  to: '',
});

export function isQuestion(item: DecisionItem): boolean {
  return !!(item.options?.length || item.question_set?.questions?.length);
}

export function matchesDecisionFilters(item: DecisionItem, now = new Date()): boolean {
  if (decisionFilters.type === 'questions' && !isQuestion(item)) return false;
  const date = new Date(item.created_at);
  if (Number.isNaN(date.getTime())) return decisionFilters.date === 'all';
  const day = new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const start = (days: number) => new Date(today.getFullYear(), today.getMonth(), today.getDate() - days).getTime();
  switch (decisionFilters.date) {
    case 'today': return day === today.getTime();
    case 'yesterday': return day === start(1);
    case 'week': return day >= start(6) && day <= today.getTime();
    case 'month': return date.getFullYear() === now.getFullYear() && date.getMonth() === now.getMonth() && day <= today.getTime();
    case 'custom': {
      const localDay = `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
      return (!decisionFilters.from || localDay >= decisionFilters.from) && (!decisionFilters.to || localDay <= decisionFilters.to);
    }
    default: return true;
  }
}
