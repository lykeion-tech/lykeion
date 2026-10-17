import { describe, expect, it } from 'vitest';
import * as studies from '../apps/dashboard/src/lib/studies';
import {
  prefixFromSlug,
  randomWorkspaceName,
  slugFrom,
} from '../apps/dashboard/src/lib/studies';

describe('removed features', () => {
  it('removed features are gone', () => {
    for (const name of ['focusLabel', 'focusPlaceholder', 'gateLabel', 'unblockActions'])
      expect(Object.keys(studies)).not.toContain(name);
  });
});

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
  positionAt,
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
    research_id: null,
    assignee_type: null,
    assignee_id: null,
    owner_id: 'u1',
    deadline: null,
    spent_usd: 0,
    board_column: 'grounding',
    state: 'working',
    flag: '',
    ending: '',
    end_reason: '',
    rounds: 0,
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
    pack_id: null,
    block_kind: '',
    scope_from: 'grounding',
    scope_to: 'reporting',
    teams: {},
    redos: 0,
    off_label: [],
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

describe('backlog positions', () => {
  const list = [
    card({ id: 'a', position: 1 }),
    card({ id: 'b', position: 2 }),
    card({ id: 'c', position: 4 }),
  ];
  it('puts a moved study between its new neighbours', () => {
    expect(positionAt(list, 'c', 1)).toBe(1.5);
    expect(positionAt(list, 'a', 1)).toBe(3);
  });
  it('goes above the top and below the bottom', () => {
    expect(positionAt(list, 'c', 0)).toBe(0);
    expect(positionAt(list, 'a', 2)).toBe(5);
    expect(positionAt([card({ id: 'a', position: 7 })], 'a', 0)).toBe(7);
  });
});

import {
  blockLabel,
  earlierColumns,
  eventText,
  recordLine,
  transcript,
} from '../apps/dashboard/src/lib/studies';
import type {
  AgentRun,
  BlockKind,
  Comment,
  LykEvent,
  StudyRecord,
} from '../apps/dashboard/src/api/types';

const rec = (kind: string, body: Record<string, unknown>, key = ''): StudyRecord => ({
  id: kind,
  card_id: 'c',
  board_column: 'grounding',
  round: 1,
  kind,
  role: 'literature-scout',
  key,
  body,
  blob_key: '',
  agent_run_id: null,
  superseded_at: null,
  created_at: '2026-10-15T10:00:00Z',
});
const lykEvent = (type: string, payload: Record<string, unknown>, at: string): LykEvent => ({
  id: 1,
  workspace_id: 'w',
  card_id: 'c',
  type,
  actor_type: 'system',
  actor_id: null,
  payload,
  created_at: at,
});

describe('stages and blocks', () => {
  it('lists the stages before the current one', () => {
    expect(earlierColumns(card({ board_column: 'experimentation' }))).toEqual([
      'grounding',
      'hypotheses',
    ]);
    expect(earlierColumns(card({ board_column: 'grounding' }))).toEqual([]);
    expect(earlierColumns(card({ board_column: 'done', ending: 'stopped' }))).toEqual([]);
  });
  it('names the block reason', () => {
    const kinds: BlockKind[] = ['', 'low_score'];
    expect(kinds.map(blockLabel)).toEqual(['', 'low score']);
  });
});

describe('records and transcript', () => {
  it('summarises a record by what it holds', () => {
    expect(
      recordLine(rec('evidence', { claim: 'X raises Y', source_id: 'europepmc:PMC1' }, 'E1')),
    ).toEqual({ key: 'E1', text: 'X raises Y', source: 'europepmc:PMC1' });
    expect(recordLine(rec('open_dataset', { accession: 'GSE84133' }))).toMatchObject({
      key: 'open_dataset',
      text: 'GSE84133',
    });
    expect(
      recordLine(rec('question', { text: '## Scoped question\nDoes X raise Y?' })),
    ).toMatchObject({ text: 'Scoped question' });
  });
  it('names the events that become transcript lines', () => {
    expect(eventText(lykEvent('card.started', { column: 'grounding' }, ''))).toBe(
      'Started in Grounding',
    );
    expect(eventText(lykEvent('card.steered', { path: '/api/cards/c/resume' }, ''))).toBe(
      'Resumed',
    );
    expect(eventText(lykEvent('card.escalated', {}, ''))).toBe('Escalated: waiting for a person');
    expect(eventText(lykEvent('record.created', {}, ''))).toBe('');
  });
  it('merges comments, runs and events in time order', () => {
    const comment = {
      id: 'k',
      card_id: 'c',
      author_id: 'u',
      body: 'hi',
      created_at: '2026-10-15T10:02:00Z',
    } as Comment;
    const run = {
      id: 'r',
      status: 'completed',
      started_at: '2026-10-15T10:01:00Z',
      created_at: '2026-10-15T10:00:30Z',
    } as AgentRun;
    const events = [
      lykEvent('card.started', { column: 'grounding' }, '2026-10-15T10:00:00Z'),
      lykEvent('record.created', {}, '2026-10-15T10:01:30Z'),
    ];
    expect(transcript([comment], [run], events).map((e) => e.kind)).toEqual([
      'event',
      'run',
      'comment',
    ]);
  });
});

import { actionLabel, blockedActions, canResume } from '../apps/dashboard/src/lib/studies';

describe('composer actions', () => {
  it('offers Resume to a blocked active study only', () => {
    expect(canResume(card({ flag: 'escalated' }))).toBe(true);
    expect(canResume(card({ flag: '' }))).toBe(false);
    expect(canResume(card({ flag: 'escalated', ending: 'stopped' }))).toBe(false);
  });
  it('labels the chosen action', () => {
    expect(actionLabel({ kind: 'comment' })).toBe('Comment');
    expect(actionLabel({ kind: 'stop' })).toBe('Stop');
  });
  it('offers only Comment to a blocked study', () => {
    expect(blockedActions(card({ flag: 'escalated', block_kind: 'low_score' }))).toEqual(['comment']);
    expect(blockedActions(card())).toEqual([]);
  });
});

import { groupDaemons } from '../apps/dashboard/src/lib/machines';
import type { Runtime } from '../apps/dashboard/src/api/types';

describe('machines', () => {
  const rt = (extra: Partial<Runtime>): Runtime => ({
    id: 'r',
    workspace_id: 'w',
    owner_id: 'u',
    daemon_id: 'd1',
    harness: 'claude',
    device_name: 'Mac',
    custom_name: null,
    status: 'online',
    last_seen_at: '2026-10-15T10:00:00Z',
    max_concurrent: 1,
    created_at: '2026-10-15T09:00:00Z',
    active_runs: 0,
    cli_version: '',
    ...extra,
  });
  it('groups runtimes by daemon, names them, and lists online machines first', () => {
    const daemons = groupDaemons([
      rt({
        id: 'a',
        daemon_id: 'd2',
        device_name: 'ben-laptop',
        status: 'offline',
        last_seen_at: '2026-10-12T10:00:00Z',
      }),
      rt({ id: 'b', harness: 'codex', custom_name: 'Lab workstation', active_runs: 2 }),
      rt({ id: 'c', last_seen_at: '2026-10-15T11:00:00Z' }),
    ]);
    expect(daemons.map((d) => d.name)).toEqual(['Lab workstation', 'ben-laptop']);
    expect(daemons[0]!.runtimes.map((r) => r.harness)).toEqual(['claude', 'codex']);
    expect(daemons[0]!.online).toBe(true);
    expect(daemons[0]!.lastSeen).toBe('2026-10-15T11:00:00Z');
    expect(daemons[0]!.activeRuns).toBe(2);
    expect(daemons[1]!.activeRuns).toBe(0);
    expect(daemons[1]!.online).toBe(false);
  });
});
