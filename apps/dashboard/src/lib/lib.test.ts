import { backendLabel } from './backends';
import { beforeAll, describe, expect, it } from 'vitest';
import { dateKey, longDateLabel, monthWeeks, moveDate, parseDateKey } from '../features/studies/dates';
import { scheduleCron, scheduleError, scheduleLabel, toggleWeekday, type Schedule } from '../features/autoresearch/schedule';
import { validCreateFields } from './settings';
import { matchesQuery, paletteEntries, paletteSelection, rankPaletteEntries } from './search';
import type { Card, Research } from '../api/types';

describe('workspace search palette', () => {
  const researches = [{ id: 'r1', name: 'Metabolism', updated_at: '2026-10-21' }] as Research[];
  const studies = [
    { id: 's1', title: 'Standalone study', number: 1, research_id: null },
    { id: 's2', title: 'Nested study', number: 2, research_id: 'r1' },
    { id: 's3', title: 'Other study', number: 3, research_id: 'missing' },
  ] as Card[];
  it('shows researches and every study on opening, including standalone studies', () => {
    const entries = paletteEntries(researches, studies, 'LAB');
    expect(rankPaletteEntries(entries, '').map((entry) => entry.path)).toEqual([
      '/projects/r1',
      '/studies/s1',
      '/studies/s2',
      '/studies/s3',
    ]);
    expect(entries.find((entry) => entry.path === '/studies/s2')?.research).toBe('Metabolism');
    expect(entries.find((entry) => entry.path === '/studies/s1')?.research).toBeUndefined();
  });
  it('ranks case-insensitive code and title matches and supports subsequences', () => {
    const entries = paletteEntries(researches, studies, 'LAB');
    expect(rankPaletteEntries(entries, 'lab-2').map((entry) => entry.path)).toEqual([
      '/studies/s2',
    ]);
    expect(rankPaletteEntries(entries, 'nstds').map((entry) => entry.path)).toEqual([
      '/studies/s2',
    ]);
    expect(rankPaletteEntries(entries, 'unfindable')).toEqual([]);
  });
  it('finds an entry by its extra search text, below a label match', () => {
    const entry = (id: string, label: string, search?: string) => ({
      id,
      kind: 'conversation' as const,
      label,
      title: label,
      subtitle: '',
      path: `/c/${id}`,
      search,
    });
    const entries = [
      entry('a', 'Weekly notes', 'Weekly notes ask about the insulin protocol'),
      entry('b', 'Insulin plan', 'Insulin plan'),
      entry('c', 'Unrelated', 'Unrelated chat'),
    ];
    expect(rankPaletteEntries(entries, 'insulin protocol').map((e) => e.id)).toEqual(['a']);
    expect(rankPaletteEntries(entries, 'insulin').map((e) => e.id)).toEqual(['b', 'a']);
    expect(rankPaletteEntries(entries, 'nothing like it')).toEqual([]);
  });
  it('keeps a row someone chose through a refresh, otherwise follows the top', () => {
    const before = rankPaletteEntries(paletteEntries(researches, studies, 'LAB'), '');
    const added = { id: 's4', title: 'A new study', number: 4, research_id: null } as Card;
    const refreshed = rankPaletteEntries(
      paletteEntries(researches, [added, ...studies], 'LAB'),
      '',
    );
    const chosen = before.findIndex((entry) => entry.path === '/studies/s2');
    expect(refreshed[paletteSelection(before, refreshed, chosen, true)]?.path).toBe('/studies/s2');
    expect(paletteSelection(before, refreshed, chosen, false)).toBe(0);
    const gone = rankPaletteEntries(paletteEntries(researches, [studies[0]!], 'LAB'), '');
    expect(paletteSelection(before, gone, chosen, true)).toBe(0);
  });
});

// A zone with daylight saving, so calendar arithmetic crosses real 23- and 25-hour days.
beforeAll(() => {
  process.env.TZ = 'Europe/Copenhagen';
});


describe('autoresearch schedules', () => {
  const base: Schedule = {
    kind: 'schedule',
    timeMode: 'time',
    time: '09:00',
    interval: 1,
    unit: 'hours',
    windowStart: '00:00',
    windowEnd: '23:00',
    dayMode: 'every',
    weekdays: [1],
    monthDay: 1,
    timezone: 'Europe/Copenhagen',
    cron: '',
  };
  it('keeps the weekday set sorted and refuses an empty set only for weekly schedules', () => {
    expect(toggleWeekday([5, 1], 3)).toEqual([1, 3, 5]);
    expect(toggleWeekday([1, 3], 1)).toEqual([3]);
    expect(toggleWeekday([0], 0)).toEqual([]);
    expect(scheduleError({ ...base, dayMode: 'week', weekdays: [] })).toBe(
      'Choose at least one day of the week.',
    );
    expect(scheduleError({ ...base, dayMode: 'every', weekdays: [] })).toBe('');
    expect(scheduleError({ ...base, dayMode: 'week', weekdays: [0] })).toBe('');
  });
  it('turns a schedule into the five-field cron the server runs', () => {
    expect(scheduleCron(base)).toBe('0 9 * * *');
    expect(scheduleCron({ ...base, time: '18:30', dayMode: 'week', weekdays: [1, 3, 5] })).toBe(
      '30 18 * * 1,3,5',
    );
    expect(scheduleCron({ ...base, dayMode: 'month', monthDay: 15 })).toBe('0 9 15 * *');
    const interval = { ...base, timeMode: 'interval' as const };
    expect(scheduleCron({ ...interval, interval: 15, unit: 'minutes' })).toBe('*/15 * * * *');
    expect(
      scheduleCron({
        ...interval,
        interval: 20,
        unit: 'minutes',
        windowStart: '08:00',
        windowEnd: '18:00',
      }),
    ).toBe('*/20 8-18 * * *');
    expect(scheduleCron({ ...interval, interval: 3 })).toBe('0 */3 * * *');
    expect(
      scheduleCron({ ...interval, interval: 2, windowStart: '09:00', windowEnd: '17:00' }),
    ).toBe('0 9-17/2 * * *');
    expect(scheduleCron({ ...base, cron: ' 5 4 * * 0 ' })).toBe('5 4 * * 0');
  });
  it('labels hourly intervals and shows the window only when narrowed', () => {
    const hourly = { ...base, timeMode: 'interval' as const };
    expect(scheduleLabel(hourly)).toBe('Every hour at :00 · Every day');
    expect(scheduleLabel({ ...hourly, interval: 3 })).toBe('Every 3 hours · Every day');
    expect(scheduleLabel({ ...hourly, windowStart: '08:00' })).toBe(
      'Every hour at :00 · Every day · 08:00–23:00',
    );
    expect(scheduleLabel({ ...base, windowStart: '08:00' })).toBe('At 09:00 · Every day');
    expect(scheduleLabel({ ...base, dayMode: 'month', monthDay: 2 })).toBe('At 09:00 · Day 2');
    expect(scheduleLabel({ ...base, cron: '*/5 * * * *' })).toBe('*/5 * * * *');
  });
});

describe('calendar dates', () => {
  it('round-trips keys and falls back to now for incomplete ones', () => {
    expect(dateKey(new Date(2026, 0, 5))).toBe('2026-01-05');
    expect(dateKey(parseDateKey('2026-03-29'))).toBe('2026-03-29');
    expect(dateKey(parseDateKey('2026-03'))).toBe(dateKey(new Date()));
    expect(dateKey(parseDateKey(''))).toBe(dateKey(new Date()));
  });
  it('steps whole days across daylight-saving changes', () => {
    expect(dateKey(moveDate(new Date(2026, 2, 28), 'ArrowRight')!)).toBe('2026-03-29');
    expect(dateKey(moveDate(new Date(2026, 2, 29), 'ArrowRight')!)).toBe('2026-03-30');
    expect(dateKey(moveDate(new Date(2026, 9, 25), 'ArrowDown')!)).toBe('2026-11-01');
    expect(moveDate(new Date(2026, 2, 29), 'ArrowRight')!.getHours()).toBe(0);
  });
  it('clamps month jumps to the shorter month and handles leap years', () => {
    expect(dateKey(moveDate(new Date(2026, 0, 31), 'PageDown')!)).toBe('2026-02-28');
    expect(dateKey(moveDate(new Date(2026, 2, 31), 'PageUp')!)).toBe('2026-02-28');
    expect(dateKey(moveDate(new Date(2028, 0, 31), 'PageDown')!)).toBe('2028-02-29');
    expect(dateKey(moveDate(new Date(2026, 11, 15), 'PageDown')!)).toBe('2027-01-15');
  });
  it('moves to the week ends and ignores other keys', () => {
    expect(dateKey(moveDate(new Date(2026, 9, 14), 'Home')!)).toBe('2026-10-11');
    expect(dateKey(moveDate(new Date(2026, 9, 14), 'End')!)).toBe('2026-10-17');
    expect(moveDate(new Date(2026, 9, 14), 'Enter')).toBeNull();
  });
  it('lays months out in whole Sunday-first weeks', () => {
    const feb = monthWeeks(new Date(2026, 1, 1));
    expect(feb.length).toBe(4);
    expect(dateKey(feb[0]![0]!)).toBe('2026-02-01');
    const aug = monthWeeks(new Date(2026, 7, 1));
    expect(aug.length).toBe(6);
    expect(dateKey(aug[0]![0]!)).toBe('2026-07-26');
    expect(dateKey(aug[5]![6]!)).toBe('2026-09-05');
    expect(
      monthWeeks(new Date(2026, 2, 1))
        .flat()
        .every((d) => d.getHours() === 0),
    ).toBe(true);
  });
  it('writes ordinals, including the teens', () => {
    const today = new Date(2000, 0, 1);
    const suffix = (day: number) => longDateLabel(new Date(2026, 0, day), today).split(', ')[1];
    expect([1, 2, 3, 4, 11, 12, 13, 21, 22, 23, 31].map(suffix)).toEqual([
      'January 1st',
      'January 2nd',
      'January 3rd',
      'January 4th',
      'January 11th',
      'January 12th',
      'January 13th',
      'January 21st',
      'January 22nd',
      'January 23rd',
      'January 31st',
    ]);
    expect(longDateLabel(new Date(2026, 9, 13), new Date(2026, 9, 13, 18))).toBe(
      'Today, Tuesday, October 13th, 2026',
    );
  });
});


describe('settings validation', () => {
  it('recognises stored create-field preferences', () => {
    expect(validCreateFields({ agent: [], manual: ['Status'] })).toBe(true);
    expect(validCreateFields({ agent: [] })).toBe(false);
    expect(validCreateFields(null)).toBe(false);
  });
});

describe('search', () => {
  it('matches case-insensitively, with an empty query matching everything', () => {
    expect(matchesQuery('Literature Watch', 'WATCH')).toBe(true);
    expect(matchesQuery('Literature', 'watch')).toBe(false);
    expect(matchesQuery('anything', '')).toBe(true);
  });
});

describe('backend names', () => {
  it('labels supported providers and preserves historical or unknown names', () => {
    expect(
      ['claude', 'codex', 'cursor', 'antigravity', 'other-provider'].map(backendLabel),
    ).toEqual(['Claude Code', 'Codex', 'cursor', 'antigravity', 'other-provider']);
  });
});

