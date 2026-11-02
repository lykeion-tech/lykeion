import { describe, expect, it } from 'vitest';
import * as studies from './studies';
import { prefixFromSlug, randomWorkspaceName, slugFrom } from './studies';

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
  matchesFilters,
  roleLabel,
  stateLabel,
  studyNumber,
  timeAgo,
} from './studies';
import type { Card } from '../api/types';

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
    hold_text: '',
    scope_from: 'grounding',
    scope_to: 'validation',
    teams: {},
    redos: 0,
    off_label: [],
    attachments: [],
    services: [],
    ...extra,
  };
}

describe('board columns', () => {
  it('orders the six columns with Blocked after Done', () => {
    expect(boardColumns.map((c) => c.name)).toEqual([
      'Grounding',
      'Hypotheses',
      'Experimentation',
      'Validation',
      'Done',
      'Blocked',
    ]);
  });
  it('has four stages', () => {
    expect(studies.stages).toEqual(['grounding', 'hypotheses', 'experimentation', 'validation']);
    expect(studies.stageIntro).toHaveLength(4);
  });
  it('shows a flagged active study in Blocked, and back in its column once the flag clears', () => {
    expect(boardColumnOf(card({ flag: 'escalated' }))).toBe('blocked');
    expect(boardColumnOf(card({ flag: '' }))).toBe('grounding');
    expect(boardColumnOf(card({ state: 'blocked', block_kind: 'low_score' }))).toBe('blocked');
    expect(boardColumnOf(card({ state: 'done' }))).toBe('grounding');
    expect(
      boardColumnOf(card({ flag: 'escalated', ending: 'stopped', board_column: 'done' })),
    ).toBe('done');
  });
  it('orders every column by creation time, newest first', () => {
    const g = groupByColumn([
      card({
        id: 'b',
        board_column: 'hypotheses',
        position: 2,
        number: 1,
        created_at: '2026-10-16T10:00:00Z',
      }),
      card({
        id: 'a',
        board_column: 'hypotheses',
        position: -1,
        number: 5,
        created_at: '2026-10-15T10:00:00Z',
      }),
      card({
        id: 'c',
        board_column: 'hypotheses',
        position: 2,
        number: 0,
        created_at: '2026-10-17T10:00:00Z',
      }),
      card({ id: 'old-done', board_column: 'done', created_at: '2026-10-15T10:00:00Z' }),
      card({ id: 'new-done', board_column: 'done', created_at: '2026-10-17T10:00:00Z' }),
      card({ id: 'x', flag: 'escalated' }),
    ]);
    expect(g.hypotheses.map((c) => c.id)).toEqual(['c', 'b', 'a']);
    expect(g.blocked.map((c) => c.id)).toEqual(['x']);
    expect(g.done.map((c) => c.id)).toEqual(['new-done', 'old-done']);
  });
});

describe('study labels', () => {
  it('numbers studies with the workspace prefix', () => {
    expect(studyNumber({ display_prefix: 'BETA' }, { number: 38 })).toBe('BETA-38');
    expect(studyNumber(undefined, { number: 2 })).toBe('LYK-2');
  });
  it('names the state, the ending and the role', () => {
    expect(stateLabel(card({ state: 'done' }))).toBe('Done');
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

import { blockLabel, eventText, recordLine, transcript } from './studies';
import type { AgentRun, BlockKind, Comment, LykEvent, StudyRecord } from '../api/types';

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
    expect(eventText(lykEvent('card.steered', { path: '/api/cards/c/stop' }, ''))).toBe('');
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

import { actionLabel, blockBadge, blockedActions, blockedMessage } from './studies';

describe('composer actions', () => {
  it('labels the chosen action', () => {
    expect(actionLabel({ kind: 'comment' })).toBe('Comment');
    expect(actionLabel({ kind: 'stop' })).toBe('Stop');
  });
  it('offers only Comment on a blocked study, and only to its owner, whose comment redoes it', () => {
    const blocked = card({ state: 'blocked', block_kind: 'low_score', owner_id: 'u1' });
    expect(blockedActions(blocked, 'u1')).toEqual(['comment']);
    expect(blockedActions(blocked, 'u2')).toEqual([]);
    expect(blockedActions(card({ owner_id: 'u1' }), 'u1')).toEqual([]);
  });
});

import { groupDaemons } from '../features/machines/machines';
import type { Runtime } from '../api/types';

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
    os: '',
    arch: '',
    kernel: '',
    language: '',
    bases: {},
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

describe('flow records and Inbox sentences', () => {
  const rec = (kind: string, round: number, key: string, body: Record<string, unknown>) =>
    ({
      kind,
      round,
      key,
      body,
    }) as unknown as import('../api/types').StudyRecord;
  it('shows the steps and calls of the latest round only', () => {
    const v = studies.flowView([
      rec('delegation', 1, 's1', { member: 'a', step: 'old' }),
      rec('call', 1, 'c1', { server: 'x', tool: 't' }),
      rec('delegation', 2, 's2', { member: 'b', step: 'new' }),
    ]);
    expect(v.steps.map((s) => s.key)).toEqual(['s2']);
    expect(v.calls).toEqual([]);
  });
  it('shows the scores of the outcome even when the outcome comes first', () => {
    const v = studies.flowView([
      rec('score', 1, 'r1', { criterion: 'old', level: 2 }),
      rec('outcome', 2, 'r5', { verdict: 'middle' }),
      rec('score', 2, 'r3', { criterion: 'a', level: 4 }),
      rec('score', 2, 'r4', { criterion: 'b', level: 3 }),
    ]);
    expect(v.verdict).toBe('middle');
    expect(v.scores.map((s) => s.criterion)).toEqual(['a', 'b']);
  });
  it('shows a score level to one decimal', () => {
    expect(studies.levelText(4.779999999999999)).toBe('4.8');
    expect(studies.levelText(5)).toBe('5');
  });
  it("labels a redo by what started it, counting the column's own attempts", () => {
    expect(studies.redoLabel(1, 2, 1)).toBe('Redone once');
    expect(studies.redoLabel(1, 3, 1)).toBe('Redone once, then after your reply');
    expect(studies.redoLabel(0, 2, 1)).toBe('Redone after your reply');
    expect(studies.redoLabel(0, 1, 1)).toBe('');
    expect(studies.redoLabel(0, 2, 2)).toBe('');
    expect(studies.redoLabel(1, 3, 2)).toBe('Redone once');
    expect(studies.redoLabel(1, 4, 2)).toBe('Redone once, then after your reply');
  });
  it('counts a study whose run waits on a machine as waiting, not working', () => {
    const card = (over: {
      role?: string;
      queued?: boolean;
      waiting?: string;
      ending?: string;
    }) => ({
      role: 'scoper',
      queued: false,
      waiting: '',
      ending: '',
      ...over,
    });
    expect(
      studies.boardActivity([
        card({}),
        card({ waiting: 'Waiting for laptop: it is offline.' }),
        card({ queued: true }),
        card({ role: '' }),
        card({ ending: 'stopped', waiting: 'Waiting for laptop: it is offline.' }),
      ]),
    ).toEqual({ working: 1, waiting: 1 });
  });
  it('titles an Inbox item by its study, or by what it is when it has none', () => {
    expect(studies.inboxTitle({ kind: 'blocked' }, 'Why?')).toBe('Why?');
    expect(studies.inboxTitle({ kind: 'waiting_for_machine' })).toBe('Waiting for machine');
    expect(studies.inboxTitle({ kind: 'autoresearch_report' })).toBe('Autoresearch');
    expect(studies.inboxTitle({ kind: 'blocked' })).toBe('Study');
  });
  it('leaves the server text alone when it does not fit', () => {
    expect(studies.inboxText({ kind: 'middle_score', text: 'no quotes here' }, 'S')).toBeNull();
    expect(studies.inboxText({ kind: 'no_tool', text: 'found nothing' }, 'S')).toBeNull();
    expect(studies.inboxText({ kind: 'escalated', text: 'Asked a question' }, 'S')).toBeNull();
  });
  it('lifts the criterion and the need out of the server sentence', () => {
    expect(
      studies.inboxText(
        {
          kind: 'middle_score',
          text: 'Study #1 scored; its weakest criterion was “Does it say what it couldn’t do?” (level 3 of 5).',
        },
        'S',
      ),
    ).toBe('S moved on with a middling score: Does it say what it couldn’t do?');
    expect(
      studies.inboxText(
        {
          kind: 'middle_score',
          text: "Study #1's Validation column scored in the middle and the study finished; its weakest criterion was “Does it say what it couldn’t do?” (level 3 of 5).",
        },
        'S',
      ),
    ).toBe('S finished with a middling score: Does it say what it couldn’t do?');
    expect(
      studies.inboxText(
        { kind: 'no_tool', text: 'found no skill or connector that fits what it needed: a parser' },
        'S',
      ),
    ).toBe("S's agent found no skill or connector for: a parser");
  });
});

describe('study overview from recorded work', () => {
  it('uses the latest current-round summary opening without repeating the evidence payload', () => {
    const old = { ...rec('summary', { text: 'Old summary.' }), round: 1 };
    const summary = {
      ...rec('summary', {
        text: 'Insulin lookup recorded.\n- Accession: P01308.\nDetailed evidence and citations follow.',
      }),
      round: 2,
    };
    expect(studies.studyOverview([summary, old], 2)).toBe('Insulin lookup recorded.');
  });
  it('uses a current step before a completion summary exists, never a superseded or earlier-round result', () => {
    const old = { ...rec('summary', { text: 'Old result.' }), round: 1 };
    const removed = {
      ...rec('summary', { text: 'Withdrawn result.' }),
      round: 2,
      superseded_at: '2026-10-19T10:00:00Z',
    };
    const step = {
      ...rec('delegation', { member: 'researcher', step: 'Retrieve the protein entry.' }),
      round: 2,
    };
    expect(studies.studyOverview([old, removed, step], 2)).toBe('Retrieve the protein entry.');
    expect(studies.studyOverview([old], 2)).toBe('No work recorded yet.');
  });
});

describe('Grounding criteria and review blocks', () => {
  it('knows when the scope covers a stage', () => {
    const covers = studies.coversStage;
    expect(covers('grounding', { from: 'grounding', to: 'validation' }, [])).toBe(true);
    expect(covers('grounding', { from: 'hypotheses', to: 'validation' }, [])).toBe(false);
    expect(covers('experimentation', { from: 'grounding', to: 'hypotheses' }, [])).toBe(false);
    expect(covers('grounding', null, ['grounding'])).toBe(true);
    expect(covers('grounding', null, ['hypotheses'])).toBe(false);
  });
  it('words a review block', () => {
    expect(
      studies.inboxText(
        {
          kind: 'blocked',
          text: "Study #3's grounding column failed Jev's review again after its redo. Still failing: paper P1 (meets every criterion). Comment on the study to tell the Team what to fix.",
        },
        '#3',
      ),
    ).toBe("#3's Grounding column failed Jev's review again and is waiting for you");
    expect(
      studies.inboxText(
        {
          kind: 'blocked',
          text: "Study #3's grounding column failed Jev's review twice: paper P1. Comment on the study.",
        },
        '#3',
      ),
    ).toBe("#3's Grounding column failed Jev's review again and is waiting for you");
    expect(studies.inboxText({ kind: 'blocked', text: 'Study #3 scored low twice.' }, '#3')).toBe(
      '#3 scored low twice and is waiting for you',
    );
  });
  it("leaves a failed run's block in the server's words", () => {
    expect(
      studies.inboxText(
        {
          kind: 'blocked',
          text: "Study #3's Hypotheses column stopped: the agent ran out of context, twice (context window full). Comment on the study to redo it.",
        },
        '#3',
      ),
    ).toBeNull();
  });
});

describe('a held study', () => {
  const held = card({
    state: 'blocked',
    block_kind: '',
    hold_text: 'Study #3: the agent could not do this column: GEO has no aged mice',
  });
  it('names the reason its agent gave, not a low score', () => {
    expect(blockedMessage(held)).toBe('The agent could not do this column: GEO has no aged mice');
    expect(blockBadge(held)).toBe('could not do');
    expect(blockedMessage(card({ state: 'blocked', block_kind: 'low_score' }))).toMatch(
      /^Scored low twice/,
    );
  });
  it('names a stopped column by its failure', () => {
    const stopped = card({
      state: 'blocked',
      hold_text:
        "Study #3's Hypotheses column stopped: the run failed, twice (x). Comment on the study to redo it.",
    });
    expect(blockedMessage(stopped)).toContain('Hypotheses column stopped');
    expect(blockBadge(stopped)).toBe('run failed');
  });
  it('renders a held card that came without its hold text', () => {
    const listed = card({ state: 'blocked', block_kind: '' });
    delete (listed as Partial<Card>).hold_text;
    expect(blockBadge(listed)).toBe('escalated');
    expect(blockedMessage(listed)).toBe('This study is waiting for you.');
  });
});
