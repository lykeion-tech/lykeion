import { describe, expect, it } from 'vitest';
import type { AgentRun, ReviewAttempt, ReviewCheck, ReviewItem, StudyRecord } from '../api/types';
import {
  checkLabel,
  defaultSpoke,
  feedLine,
  indexOf,
  legacyColumns,
  reviewingLine,
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
      'Jev · Grounding, try 1 · needs another try',
    );
  });

  it('shows reviewing only after the latest round finishes and before it has a review', () => {
    const runs = [
      { board_column: 'hypotheses', round: 2, status: 'running' },
      { board_column: 'hypotheses', round: 3, status: 'completed' },
      { board_column: 'hypotheses', round: 3, status: 'completed' },
    ] as AgentRun[];
    const reviews = [{ column: 'hypotheses' as const, attempts: [attempt()] }];
    expect(reviewingLine('hypotheses', runs, reviews)).toBe('Jev · Hypotheses, try 2 · reviewing…');
    expect(
      reviewingLine('hypotheses', [...runs, { ...runs[2]!, status: 'running' }], reviews),
    ).toBeNull();
    expect(
      reviewingLine('hypotheses', runs, [
        { column: 'hypotheses', attempts: [attempt({ round: 3 })] },
      ]),
    ).toBeNull();
    expect(reviewingLine('grounding', runs, reviews)).toBeNull();
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
