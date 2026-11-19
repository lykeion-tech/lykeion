import { describe, expect, it } from 'vitest';
import type { ReviewAttempt, ReviewCheck, ReviewItem, StudyRecord } from '../api/types';
import {
  checkLabel,
  defaultSpoke,
  feedLine,
  indexOf,
  legacyColumns,
  reviewTimeline,
  spokes,
  statusWord,
  tileOf,
} from './review';

const check = (extra: Partial<ReviewCheck> = {}): ReviewCheck => ({
  id: 'supported',
  by: 'jev',
  status: 'pass',
  ...extra,
});
const item = (extra: Partial<ReviewItem> = {}): ReviewItem => ({
  key: 'A',
  kind: 'paper',
  label: 'Paper',
  status: 'pass',
  checks: [],
  quote: '',
  cites: [],
  ...extra,
});
const attempt = (extra: Partial<ReviewAttempt> = {}): ReviewAttempt => ({
  column: 'hypotheses',
  attempt: 1,
  round: 1,
  at: '',
  items: [],
  decision: 'pass',
  ...extra,
});
const record = (extra: Partial<StudyRecord> = {}): StudyRecord => ({
  id: 'r',
  card_id: 'c',
  board_column: 'grounding',
  round: 1,
  kind: 'score',
  role: 'jev',
  key: '',
  body: {},
  blob_key: '',
  agent_run_id: null,
  superseded_at: null,
  created_at: '',
  ...extra,
});

describe('review helpers', () => {
  it('averages only checks with probabilities for the index', () => {
    expect(indexOf([check({ p: 0.92 }), check({ p: 0.22 }), check({ status: 'pass' })])).toBe(57);
    expect(indexOf([check({ status: 'fail' })])).toBeNull();
  });

  it('keeps positional spoke ids for missed items and omits withdrawn and unscored checks', () => {
    const result = spokes(
      attempt({
        items: [
          item({ status: 'withdrawn', checks: [check({ p: 0.1 })] }),
          item({ checks: [check({ by: 'server' })] }),
        ],
        missed: [
          item({ key: '', checks: [check({ id: 'out_of_scope', p: 0.2 })] }),
          item({ key: '', checks: [check({ id: 'out_of_scope', p: 0.4, warning: true })] }),
        ],
      }),
    );
    expect(result).toEqual([
      { id: '2-0', item: 2, check: 0, p: 0.2, status: 'pass', warning: false },
      { id: '3-0', item: 3, check: 0, p: 0.4, status: 'pass', warning: true },
    ]);
    expect(new Set(result.map((s) => s.id)).size).toBe(2);
  });

  it('selects the weakest item before falling back to the lowest probability', () => {
    const a = attempt({
      items: [
        item({ checks: [check({ p: 0.1 })] }),
        item({ key: 'B', checks: [check({ p: 0.8 }), check({ p: 0.4 })] }),
      ],
      weakest: { key: 'B', p: 0.4 },
    });
    expect(defaultSpoke(a)).toBe('1-1');
    expect(defaultSpoke({ ...a, weakest: undefined })).toBe('0-0');
    expect(defaultSpoke(attempt({ items: [item({ checks: [check()] })] }))).toBeNull();
  });

  it('counts carried passes and missed items using the last attempt for the tile', () => {
    const a = attempt({
      attempt: 2,
      items: [
        item({ checks: [check({ p: 0.8 })] }),
        item({ status: 'fail', checks: [check({ p: 0.2 })] }),
        item({ status: 'withdrawn', checks: [check({ p: 0 })] }),
        item({ carried: true }),
      ],
      missed: [item({ status: 'fail' })],
    });
    expect(tileOf({ column: 'hypotheses', attempts: [attempt(), a] }, 'validation')).toEqual({
      pass: 2,
      total: 4,
      index: 50,
      tryN: 2,
      state: 'reviewed',
    });
    expect(tileOf({ column: 'hypotheses', attempts: [] }, 'hypotheses').state).toBe('working');
    expect(tileOf({ column: 'grounding', attempts: [] }, 'hypotheses').state).toBe('not reached');
  });

  it('describes the review decision in the feed', () => {
    expect(feedLine(attempt({ column: 'grounding', attempt: 1, decision: 'redo' }))).toBe(
      'Jev · Grounding · needs another try',
    );
    expect(feedLine(attempt({ column: 'validation', decision: 'route_back' }))).toBe(
      'Jev · Validation · sent back to Experimentation',
    );
  });

  it('uses the latest legacy round per column and rounds levels to words', () => {
    expect(
      legacyColumns(
        [
          record({ round: 1, body: { criterion: 'Old', level: 1 } }),
          record({ round: 2, body: { criterion: 'Current', level: 3.6 } }),
          record({
            board_column: 'hypotheses',
            round: 1,
            body: { criterion: 'Separate', level: 2 },
          }),
        ],
        [
          record({ kind: 'outcome', round: 1, body: { verdict: 'low' } }),
          record({ kind: 'outcome', round: 2, body: { verdict: 'high' } }),
        ],
      ),
    ).toEqual([
      { column: 'grounding', verdict: 'high', criteria: [{ name: 'Current', level: 'Mostly' }] },
      { column: 'hypotheses', verdict: '', criteria: [{ name: 'Separate', level: 'Barely' }] },
    ]);
  });

  it('labels numbered criteria, saved files, and unknown checks', () => {
    expect(checkLabel('criterion:2')).toBe('Criterion 2');
    expect(checkLabel('file:out.csv')).toBe('File out.csv saved');
    expect(checkLabel('new_thing')).toBe('new thing');
  });

  it('describes check status in words', () => {
    expect(
      ['pass', 'unclear', 'fail', 'withdrawn'].map((s) => statusWord(s as ReviewCheck['status'])),
    ).toEqual(['Supported', 'Unclear', 'Unsupported', 'Withdrawn']);
  });
});

describe('recorded Jev activity', () => {
  const start = {
    id: 10,
    type: 'review.started',
    created_at: '2026-11-16T10:02:00Z',
    payload: { pass_id: 'pass', column: 'grounding', round: 1, attempt: 1 },
  } as any;
  const finish = {
    id: 11,
    type: 'review.finished',
    created_at: '2026-11-16T10:05:00Z',
    payload: { pass_id: 'pass', status: 'completed' },
  } as any;
  const card = {
    board_column: 'grounding',
    round: 1,
    state: 'working',
    gating_until: '2026-11-16T10:15:00Z',
  } as any;
  it('shows no placeholder without an actual start', () => {
    expect(reviewTimeline([], [], card, Date.parse('2026-11-16T10:03:00Z'))).toEqual([]);
  });
  it('does not attribute a new server-only result to Jev', () => {
    const result = attempt({
      column: 'grounding',
      at: '2026-11-16T10:04:00Z',
      legacy_timeline: false,
    });
    expect(
      reviewTimeline(
        [],
        [{ column: 'grounding', attempts: [result] }],
        card,
        Date.parse('2026-11-16T10:06:00Z'),
      ),
    ).toEqual([]);
  });
  it('places the result at the actual start without duplicating it', () => {
    const result = attempt({ column: 'grounding', at: '2026-11-16T10:04:00Z' });
    expect(
      reviewTimeline(
        [start, finish],
        [{ column: 'grounding', attempts: [result] }],
        card,
        Date.parse('2026-11-16T10:06:00Z'),
      ),
    ).toEqual([
      { at: start.created_at, text: 'Jev · Grounding · good to go', column: 'grounding' },
    ]);
  });
  it('uses the recorded pass when lifecycle events are missing or belong to another pass', () => {
    const result = attempt({
      column: 'grounding',
      at: '2026-11-16T10:04:00Z',
      legacy_timeline: false,
      jev_pass_id: 'other',
      jev_started_at: '2026-11-16T10:03:00Z',
    });
    const lines = reviewTimeline(
      [start, finish],
      [{ column: 'grounding', attempts: [result] }],
      card,
      Date.parse('2026-11-16T10:06:00Z'),
    );
    expect(lines).toHaveLength(2);
    expect(lines[1]).toEqual({
      at: result.jev_started_at,
      text: 'Jev · Grounding · good to go',
      column: 'grounding',
    });
  });
  it('shows a live review only until it finishes or its lease expires', () => {
    expect(
      reviewTimeline([start], [], card, Date.parse('2026-11-16T10:03:00Z'))[0]?.text,
    ).toContain('reviewing');
    expect(
      reviewTimeline([start], [], card, Date.parse('2026-11-16T10:16:00Z'))[0]?.text,
    ).not.toContain('reviewing');
    expect(
      reviewTimeline(
        [start, { ...finish, payload: { ...finish.payload, status: 'failed' } }],
        [],
        card,
        Date.parse('2026-11-16T10:06:00Z'),
      )[0]?.text,
    ).toContain('unavailable');
  });
  it('does not revive an abandoned pass when the same round is retried', () => {
    const retry = {
      ...start,
      id: 12,
      created_at: '2026-11-16T10:04:00Z',
      payload: { ...start.payload, pass_id: 'retry' },
    };
    const lines = reviewTimeline(
      [start, retry],
      [],
      { ...card, legacy_column: 'grounding' },
      Date.parse('2026-11-16T10:06:00Z'),
    );
    expect(lines.map((line) => line.text)).toEqual([
      'Jev · Grounding · review unavailable',
      'Jev · Grounding · reviewing',
    ]);
  });
});
