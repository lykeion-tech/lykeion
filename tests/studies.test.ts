import { describe, expect, it } from 'vitest';
import { prefixFromSlug, randomWorkspaceName, slugFrom } from '../apps/dashboard/src/lib/studies';

describe('workspace naming', () => {
  it('makes a slug the server accepts', () => {
    expect(slugFrom('Beta-cell lab')).toBe('beta-cell-lab');
    expect(slugFrom('  Ünïcode & Co. 2026 ')).toBe('n-code-co-2026');
    expect(slugFrom('x')).toBe('');
    expect(slugFrom('a'.repeat(60))).toHaveLength(39);
    expect(slugFrom('--Lab--')).toBe('lab');
  });
  it('derives the card prefix from the slug as the server does', () => {
    expect(prefixFromSlug('beta-cell-lab')).toBe('BETA');
    expect(prefixFromSlug('ab-12')).toBe('AB');
    expect(prefixFromSlug('a1')).toBe('LYK');
    expect(prefixFromSlug('')).toBe('LYK');
  });
  it('suggests a name whose slug is valid', () => {
    const name = randomWorkspaceName(() => 0);
    expect(name).toBeTruthy();
    expect(slugFrom(name)).toMatch(/^[a-z0-9][a-z0-9-]{1,38}$/);
    expect(randomWorkspaceName(() => 0.999)).not.toBe(name);
  });
});

import {
  boardColumnOf,
  boardColumns,
  endLabel,
  groupByColumn,
  matchesFilters,
  roleLabel,
  stateLabel,
  studyNumber,
  timeAgo,
} from '../apps/dashboard/src/lib/studies';
import type { Card } from '../apps/dashboard/src/api/types';

function card(extra: Partial<Card> = {}): Card {
  return {
    id: 'c',
    workspace_id: 'w',
    number: 1,
    title: 't',
    question: 'q',
    owner_id: 'u1',
    budget_usd: 0,
    deadline: null,
    spent_usd: 0,
    board_column: 'grounding',
    state: 'working',
    flag: '',
    ending: '',
    end_reason: '',
    rounds: 0,
    route_backs: 0,
    stale: [],
    rerun: '',
    follows_from: [],
    version: 1,
    created_at: '2026-10-15T10:00:00Z',
    updated_at: '2026-10-15T10:00:00Z',
    round: 1,
    handoff_note: '',
    gating_until: null,
    position: 1,
    queued: false,
    role: '',
    ...extra,
  };
}

describe('board columns', () => {
  it('orders the eight columns with Blocked between Validation and Reporting', () => {
    expect(boardColumns.map((c) => c.name)).toEqual([
      'Backlog',
      'Grounding',
      'Hypotheses',
      'Experimentation',
      'Validation',
      'Blocked',
      'Reporting',
      'Done',
    ]);
  });
  it('shows a flagged active study in Blocked, and back in its column once the flag clears', () => {
    expect(boardColumnOf(card({ flag: 'escalated' }))).toBe('blocked');
    expect(boardColumnOf(card({ flag: 'out_of_budget', board_column: 'validation' }))).toBe(
      'blocked',
    );
    expect(boardColumnOf(card({ flag: '' }))).toBe('grounding');
    expect(
      boardColumnOf(card({ flag: 'escalated', ending: 'stopped', board_column: 'done' })),
    ).toBe('done');
  });
  it('orders a column by position, then number', () => {
    const g = groupByColumn([
      card({ id: 'b', board_column: 'backlog', position: 2, number: 1 }),
      card({ id: 'a', board_column: 'backlog', position: -1, number: 5 }),
      card({ id: 'c', board_column: 'backlog', position: 2, number: 0 }),
      card({ id: 'x', flag: 'escalated' }),
    ]);
    expect(g.backlog.map((c) => c.id)).toEqual(['a', 'c', 'b']);
    expect(g.blocked.map((c) => c.id)).toEqual(['x']);
    expect(g.done).toEqual([]);
  });
});

describe('study labels', () => {
  it('numbers studies with the workspace prefix', () => {
    expect(studyNumber({ display_prefix: 'BETA' }, { number: 38 })).toBe('BETA-38');
    expect(studyNumber(undefined, { number: 2 })).toBe('LYK-2');
  });
  it('names the state, the ending and the role', () => {
    expect(stateLabel(card({ state: 'in_review' }))).toBe('In review');
    expect(stateLabel(card({ board_column: 'backlog' }))).toBe('');
    expect(stateLabel(card({ ending: 'released' }))).toBe('');
    expect(endLabel(card({ ending: 'stopped', end_reason: 'no usable data' }))).toBe(
      'Stopped · no usable data',
    );
    expect(endLabel(card({ ending: 'released' }))).toBe('Released');
    expect(roleLabel('literature-scout')).toBe('Literature scout');
  });
  it('says how long ago', () => {
    const now = Date.parse('2026-10-15T12:00:00Z');
    expect(timeAgo('2026-10-15T11:59:30Z', now)).toBe('just now');
    expect(timeAgo('2026-10-15T11:56:00Z', now)).toBe('4m ago');
    expect(timeAgo('2026-10-15T10:00:00Z', now)).toBe('2h ago');
    expect(timeAgo('2026-10-13T12:00:00Z', now)).toBe('2d ago');
  });
});

describe('study filters', () => {
  it('filters by column, state, flag and owner', () => {
    const queued = card({ queued: true, role: 'scoper' });
    const blocked = card({ flag: 'escalated', owner_id: 'u2' });
    expect(matchesFilters(blocked, { Column: ['blocked'] })).toBe(true);
    expect(matchesFilters(blocked, { Column: ['grounding'] })).toBe(false);
    expect(matchesFilters(queued, { Flag: ['queued'] })).toBe(true);
    expect(matchesFilters(blocked, { Flag: ['queued'] })).toBe(false);
    expect(matchesFilters(blocked, { Owner: ['u2'], State: ['working'] })).toBe(true);
    expect(matchesFilters(blocked, { Owner: ['u1'] })).toBe(false);
    expect(matchesFilters(blocked, {})).toBe(true);
  });
});
