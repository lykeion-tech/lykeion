import { afterEach, describe, expect, it } from 'vitest';
import type { DecisionItem } from '../../api/types';
import { decisionFilters, isQuestion, matchesDecisionFilters } from './decisionFilters';

const item = (date: string, question = true) => ({ created_at: date, options: question ? [{ id: 'a' }] : null } as DecisionItem);
const now = new Date(2026, 10, 3, 12);
const date = (day: number) => new Date(2026, 10, day, 12).toISOString();
afterEach(() => Object.assign(decisionFilters, { type: 'all', date: 'all', from: '', to: '' }));

describe('decision filters', () => {
  it('filters question decisions while retaining other activity in All', () => {
    expect(isQuestion(item(date(3)))).toBe(true);
    expect(isQuestion(item(date(3), false))).toBe(false);
    decisionFilters.type = 'questions';
    expect(matchesDecisionFilters(item(date(3)), now)).toBe(true);
    expect(matchesDecisionFilters(item(date(3), false), now)).toBe(false);
  });
  it('uses local calendar dates for presets and custom inclusive ranges', () => {
    decisionFilters.date = 'today';
    expect(matchesDecisionFilters(item(date(3)), now)).toBe(true);
    expect(matchesDecisionFilters(item(date(2)), now)).toBe(false);
    decisionFilters.date = 'yesterday';
    expect(matchesDecisionFilters(item(date(2)), now)).toBe(true);
    decisionFilters.date = 'week';
    expect(matchesDecisionFilters(item(date(-3)), now)).toBe(true);
    expect(matchesDecisionFilters(item(date(-4)), now)).toBe(false);
    decisionFilters.date = 'month';
    expect(matchesDecisionFilters(item(date(1)), now)).toBe(true);
    expect(matchesDecisionFilters(item(date(0)), now)).toBe(false);
    Object.assign(decisionFilters, { date: 'custom', from: '2026-11-01', to: '2026-11-02' });
    expect(matchesDecisionFilters(item(date(1)), now)).toBe(true);
    expect(matchesDecisionFilters(item(date(2)), now)).toBe(true);
    expect(matchesDecisionFilters(item(date(3)), now)).toBe(false);
  });
});
