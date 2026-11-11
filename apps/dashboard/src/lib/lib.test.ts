import { backendLabel } from './backends';
import { beforeAll, describe, expect, it } from 'vitest';
import { csvCell, toCsv } from './csv';
import {
  dateKey,
  formatDuration,
  longDateLabel,
  monthWeeks,
  moveDate,
  parseDateKey,
} from '../features/studies/dates';
import {
  scheduleCron,
  scheduleError,
  scheduleLabel,
  scheduleSummary,
  toggleWeekday,
  type Schedule,
} from '../features/autoresearch/schedule';
import {
  connectorConfig,
  connectorFields,
  parseConnectorJson,
} from './connectors';
import {
  labelNameTaken,
  propertyOptions,
  repositoryUrl,
  validCreateFields,
} from './settings';
import { matchesQuery, paletteEntries, paletteSelection, rankPaletteEntries } from './search';
import { environmentView } from '../features/studies/environment';
import type {
  AgentRun,
  Card,
  Research,
  Runtime,
  StudyRecord,
} from '../api/types';

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

describe('CSV export', () => {
  it.each(['=SUM(A1)', '+1', '-1', '@cmd', '  =1', '\t=1', '\tplain', '\rx', '\nx'])(
    'neutralises %j as a formula',
    (value) => expect(csvCell(value)).toBe(`"'${value}"`),
  );
  it('leaves ordinary text, inner formula characters and inner line breaks alone', () => {
    expect(csvCell('a=b')).toBe('"a=b"');
    expect(csvCell('line 1\nline 2')).toBe('"line 1\nline 2"');
    expect(csvCell('')).toBe('""');
  });
  it('doubles quotes, including after the formula guard', () => {
    expect(csvCell('say "hi"')).toBe('"say ""hi"""');
    expect(csvCell('="x"')).toBe('"\'=""x"""');
  });
  it('joins cells with commas and rows with CRLF', () => {
    expect(
      toCsv([
        ['Task', 'Labels'],
        ['a, b', '-'],
      ]),
    ).toBe('"Task","Labels"\r\n"a, b","\'-"');
  });
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
  it('summarises time, interval, weekday, month and cron schedules', () => {
    expect(scheduleSummary(base)).toBe('At 09:00 · Every day · Europe/Copenhagen');
    expect(
      scheduleSummary({
        ...base,
        timeMode: 'interval',
        interval: 15,
        unit: 'minutes',
        dayMode: 'week',
        weekdays: [0, 6],
      }),
    ).toBe('Every 15 minutes between 00:00 and 23:00 · Sun, Sat · Europe/Copenhagen');
    expect(scheduleSummary({ ...base, dayMode: 'month', monthDay: 31 })).toBe(
      'At 09:00 · Day 31 of the month · Europe/Copenhagen',
    );
    expect(scheduleSummary({ ...base, cron: '0 9 * * 1' })).toBe(
      'Cron 0 9 * * 1 (Europe/Copenhagen)',
    );
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
  it('formats durations', () => {
    expect(formatDuration(null)).toBe('Not reported');
    expect(formatDuration(0)).toBe('0s');
    expect(formatDuration(59.4)).toBe('59s');
    expect(formatDuration(60)).toBe('1m 0s');
    expect(formatDuration(125)).toBe('2m 5s');
  });
});

describe('connector configuration', () => {
  it('round-trips fields and JSON, keeping keys the fields do not model', () => {
    const text = JSON.stringify({
      type: 'stdio',
      command: 'npx',
      args: ['-y', 'server'],
      env: { TOKEN: 'x' },
      timeout: 30,
    });
    const fields = connectorFields(parseConnectorJson(text));
    expect(fields).toMatchObject({
      transport: 'STDIO',
      args: ['-y', 'server'],
      env: [{ key: 'TOKEN', value: 'x' }],
      extra: { timeout: 30 },
    });
    expect(connectorConfig(fields)).toEqual(JSON.parse(text));
  });
  it('writes HTTP configurations without empty maps', () => {
    expect(
      connectorConfig({
        transport: 'Streamable HTTP',
        command: 'ignored',
        url: 'https://x',
        args: ['a'],
        env: [],
        headers: [],
        extra: {},
      }),
    ).toEqual({ type: 'http', url: 'https://x' });
  });
  it.each([
    ['[]', 'Configuration must be a JSON object.'],
    ['null', 'Configuration must be a JSON object.'],
    ['{"type":"sse"}', 'Transport type must be stdio or http.'],
    ['{"type":"stdio"}', 'A STDIO configuration needs a command string.'],
    ['{"type":"http","url":5}', 'An HTTP configuration needs a URL string.'],
    ['{"type":"stdio","command":"x","args":[1]}', 'Arguments must be an array of strings.'],
    [
      '{"type":"stdio","command":"x","env":{"A":1}}',
      'Headers and environment values must be string maps.',
    ],
    [
      '{"type":"http","url":"u","headers":["a"]}',
      'Headers and environment values must be string maps.',
    ],
  ])('rejects %s', (text, message) => {
    expect(() => parseConnectorJson(text)).toThrow(message);
  });
  it('lets the caller tell broken JSON from a bad shape', () => {
    expect(() => parseConnectorJson('{')).toThrow(SyntaxError);
  });
});

describe('settings validation', () => {
  it('accepts plain HTTPS repository URLs only', () => {
    expect(repositoryUrl('https://github.com/a/b')).toBe('https://github.com/a/b');
    expect(repositoryUrl('HTTPS://GitHub.com/a')).toBe('https://github.com/a');
    for (const bad of [
      'http://x.org/a',
      'https://u:p@x.org',
      'https://x.org/?a=1',
      'https://x.org/#a',
      'x.org',
      '',
    ])
      expect(repositoryUrl(bad)).toBeNull();
  });
  it('trims, dedupes and drops blank options, for select types only', () => {
    expect(propertyOptions('Select', ' a\n\nb\na \n')).toEqual(['a', 'b']);
    expect(propertyOptions('Multi-select', 'x')).toEqual(['x']);
    expect(propertyOptions('Text', 'x')).toEqual([]);
  });
  it('treats label names as unique per scope, ignoring case and spacing', () => {
    const labels = [
      { id: '1', name: 'Bug', description: '', color: '', scope: 'Tasks' as const, updatedAt: '' },
    ];
    expect(labelNameTaken(labels, ' bug ', 'Tasks', '')).toBe(true);
    expect(labelNameTaken(labels, 'bug', 'Tasks', '1')).toBe(false);
    expect(labelNameTaken(labels, 'bug', 'Skills', '')).toBe(false);
  });
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

describe('study environment', () => {
  const runtime = (id: string, daemon: string, bases: Record<string, string>) =>
    ({
      id,
      daemon_id: daemon,
      device_name: 'lab-mac',
      custom_name: daemon === 'd1' ? 'Bench Mac' : null,
      bases,
    }) as Runtime;
  const run = (runtimeId: string | null) => ({ runtime_id: runtimeId }) as AgentRun;
  const record = (language: string, packages: string, at: string, createdAt = at) =>
    ({
      kind: 'run',
      created_at: createdAt,
      body: { language, packages_study: packages, at },
    }) as unknown as StudyRecord;

  it('maps each base state and dedupes one daemon’s runtimes into one machine', () => {
    const bases = {
      python: 'ready',
      r: 'failed: no network',
      sandbox: 'failed: sandbox-exec missing',
    };
    const view = environmentView(
      [run('a'), run('b'), run(null), run('gone')],
      [runtime('a', 'd1', bases), runtime('b', 'd1', bases), runtime('c', 'd2', {})],
      [],
    );
    expect(view.machines).toEqual([
      {
        name: 'Bench Mac',
        bases: [
          { language: 'Python', state: 'Ready', reason: '' },
          { language: 'R', state: 'Failed', reason: 'no network' },
          { language: 'Julia', state: 'Not reported', reason: '' },
        ],
      },
    ]);
  });

  it('shows only language bases when the sandbox is ready or unreported', () => {
    const view = environmentView(
      [run('a'), run('b')],
      [runtime('a', 'd1', { sandbox: 'ready' }), runtime('b', 'd2', {})],
      [],
    );
    expect(view.machines.map((m) => m.bases.map((b) => `${b.language} ${b.state}`))).toEqual([
      ['Python Not reported', 'R Not reported', 'Julia Not reported'],
      ['Python Not reported', 'R Not reported', 'Julia Not reported'],
    ]);
  });

  it('takes each language’s packages from its latest run, skipping empty ones', () => {
    const view = environmentView(
      [],
      [],
      [
        record('python', 'scipy==1.14\n', '2026-10-19T11:00:00Z'),
        record('python', 'numpy==2.0\n', '2026-10-19T10:00:00Z'),
        record('julia', '', '2026-10-19T12:00:00Z'),
      ],
    );
    expect(view.packages).toEqual([
      { language: 'Python', text: 'scipy==1.14\n', at: '2026-10-19T11:00:00Z' },
    ]);
    expect(view.machines).toEqual([]);
  });

  it('marks building and treats a malformed state as not reported', () => {
    const view = environmentView(
      [run('a')],
      [
        runtime('a', 'd1', {
          python: 'building',
          r: 'failed',
          julia: 'failed:no space',
          sandbox: 'building',
        }),
      ],
      [],
    );
    expect(view.machines[0]!.bases.map((b) => [b.state, b.reason])).toEqual([
      ['Building', ''],
      ['Not reported', ''],
      ['Not reported', ''],
    ]);
  });

  it('orders runs of one second by their full creation time, later input winning a tie', () => {
    const at = '2026-10-19T10:00:00Z';
    const packages = (records: StudyRecord[]) =>
      environmentView([], [], records).packages.map((p) => p.text);
    expect(
      packages([
        record('python', 'old\n', at, '2026-10-19T10:00:00.100Z'),
        record('python', 'new\n', at, '2026-10-19T10:00:00.200Z'),
      ]),
    ).toEqual(['new\n']);
    expect(packages([record('python', 'first\n', at), record('python', 'second\n', at)])).toEqual([
      'second\n',
    ]);
    // A creation time that doesn't parse falls back to the run's own time.
    expect(
      packages([
        record('python', 'dated\n', '2026-10-19T11:00:00Z', 'not a date'),
        record('python', 'older\n', at),
      ]),
    ).toEqual(['dated\n']);
  });
});
