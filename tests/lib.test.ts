import { beforeAll, describe, expect, it } from 'vitest';
import type { ExecutionRun, Inquiry } from '../packages/contracts/src/research';
import { csvCell, toCsv } from '../apps/dashboard/src/lib/csv';
import {
  boardGroups,
  cellPatch,
  dueBucket,
  filterOptions,
  groupByName,
  hierarchyOrder,
  matchesFilters,
  sortTasks,
  tableValue,
  type Directory,
} from '../apps/dashboard/src/lib/tasks';
import {
  dateKey,
  formatDuration,
  longDateLabel,
  monthWeeks,
  moveDate,
  parseDateKey,
} from '../apps/dashboard/src/lib/dates';
import {
  scheduleError,
  scheduleLabel,
  scheduleSummary,
  toggleWeekday,
  type Schedule,
} from '../apps/dashboard/src/lib/schedule';
import {
  connectorConfig,
  connectorFields,
  parseConnectorJson,
} from '../apps/dashboard/src/lib/connectors';
import {
  agentSeconds,
  costLine,
  elapsedSeconds,
  runBar,
  runSeconds,
  totalCost,
} from '../apps/dashboard/src/lib/runs';
import { colorPresets, hexToHsv, hsvToHex } from '../apps/dashboard/src/lib/color';
import {
  labelNameTaken,
  propertyOptions,
  repositoryUrl,
  validCreateFields,
} from '../apps/dashboard/src/lib/settings';
import { matchesQuery } from '../apps/dashboard/src/lib/search';

// A zone with daylight saving, so calendar arithmetic crosses real 23- and 25-hour days.
beforeAll(() => {
  process.env.TZ = 'Europe/Copenhagen';
});

function task(id: string, extra: Partial<Inquiry> = {}): Inquiry {
  return {
    id,
    title: id,
    description: '',
    stage: 'Grounding',
    status: 'active',
    priority: 'none',
    projectId: null,
    assigneeId: null,
    createdAt: '2026-01-01T00:00:00Z',
    revision: 1,
    ...extra,
  };
}
function directory(extra: Partial<Directory> = {}): Directory {
  return {
    workspace: { id: 'w', name: 'W', slug: 'w', taskPrefix: '', researchContext: '' },
    inquiries: [],
    agents: [],
    colleagues: [],
    projects: [],
    ...extra,
  };
}
const ids = (rows: { id: string }[]) => rows.map((x) => x.id);

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

describe('task filters', () => {
  const dir = directory({
    projects: [{ id: 'p', name: 'P', description: '', createdAt: '', status: 'Live' }],
  });
  const rows = [
    task('a', { priority: 'high', labels: ['x'], projectId: 'p' }),
    task('b', { priority: 'low', labels: [] }),
    task('c', { priority: 'high', status: 'blocked' }),
  ];
  const run = (filters: Parameters<typeof matchesFilters>[1]) =>
    ids(rows.filter((t) => matchesFilters(t, filters, dir)));
  it('treats no filters and empty selections as no constraint', () => {
    expect(run({})).toEqual(['a', 'b', 'c']);
    expect(run({ Priority: [] })).toEqual(['a', 'b', 'c']);
  });
  it('ORs values within a category and ANDs categories', () => {
    expect(run({ Priority: ['high', 'low'] })).toEqual(['a', 'b', 'c']);
    expect(run({ Priority: ['high'], Status: ['Blocked'] })).toEqual(['c']);
    expect(run({ Priority: ['low'], Status: ['Blocked'] })).toEqual([]);
  });
  it('matches missing labels, projects and project statuses by their placeholder', () => {
    expect(run({ Label: ['none'] })).toEqual(['b', 'c']);
    expect(run({ Project: ['none'] })).toEqual(['b', 'c']);
    expect(run({ 'Project status': ['Live'] })).toEqual(['a']);
    expect(run({ 'Project status': ['unknown'] })).toEqual(['b', 'c']);
  });
  it('offers each label and project status once', () => {
    const labelled = directory({
      inquiries: [task('a', { labels: ['x', 'y'] }), task('b', { labels: ['x'] })],
      projects: [
        { id: 'p', name: 'P', description: '', createdAt: '', status: 'Live' },
        { id: 'q', name: 'Q', description: '', createdAt: '', status: 'Live' },
        { id: 'r', name: 'R', description: '', createdAt: '' },
      ],
    });
    expect(filterOptions('Label', labelled).map((x) => x.value)).toEqual(['none', 'x', 'y']);
    expect(filterOptions('Project status', labelled).map((x) => x.value)).toEqual([
      'unknown',
      'Live',
    ]);
  });
  it('buckets due dates against local midnight', () => {
    const now = new Date(2026, 5, 10, 15, 30);
    expect(dueBucket(null, now)).toBe('No due date');
    expect(dueBucket(new Date(2026, 5, 9, 23, 59).toISOString(), now)).toBe('Overdue');
    expect(dueBucket(new Date(2026, 5, 10, 0, 0).toISOString(), now)).toBe('Due today');
    expect(dueBucket(new Date(2026, 5, 10, 23, 59).toISOString(), now)).toBe('Due today');
    expect(dueBucket(new Date(2026, 5, 11, 0, 0).toISOString(), now)).toBe('Upcoming');
  });
  it('keeps the whole 25-hour day when daylight saving ends', () => {
    const now = new Date(2026, 9, 25, 9, 0);
    expect(dueBucket(new Date(2026, 9, 25, 23, 30).toISOString(), now)).toBe('Due today');
    expect(dueBucket(new Date(2026, 9, 26, 0, 0).toISOString(), now)).toBe('Upcoming');
  });
});

describe('task sorting and grouping', () => {
  it('sorts by priority rank, not alphabetically, and reverses when descending', () => {
    const rows = [
      task('l', { priority: 'low' }),
      task('u', { priority: 'urgent' }),
      task('m', { priority: 'medium' }),
    ];
    expect(ids(sortTasks(rows, 'Priority', false))).toEqual(['u', 'm', 'l']);
    expect(ids(sortTasks(rows, 'Priority', true))).toEqual(['l', 'm', 'u']);
    expect(ids(rows)).toEqual(['l', 'u', 'm']);
  });
  it('puts tasks without a due date last and falls back to creation for updates', () => {
    const rows = [
      task('none'),
      task('late', { dueDate: '2026-09-01' }),
      task('soon', { dueDate: '2026-02-01' }),
    ];
    expect(ids(sortTasks(rows, 'Due date', false))).toEqual(['soon', 'late', 'none']);
    const updated = [
      task('old', { createdAt: '2026-01-01', updatedAt: '2026-05-01' }),
      task('new', { createdAt: '2026-03-01' }),
    ];
    expect(ids(sortTasks(updated, 'Updated date', false))).toEqual(['new', 'old']);
  });
  it('shows the Done column only once a task is done, and blocked tasks under Blocked', () => {
    const open = [task('a'), task('b', { status: 'blocked' })];
    const groups = boardGroups(open, 'Status', directory());
    expect(groups.map((g) => g.name)).not.toContain('Done');
    expect(ids(groups.find((g) => g.name === 'Blocked')!.tasks)).toEqual(['b']);
    expect(
      boardGroups([...open, task('c', { status: 'done' })], 'Status', directory()).at(-1)!.name,
    ).toBe('Done');
  });
  it('keeps same-named assignees as separate board columns, keyed by id', () => {
    const dir = directory({
      agents: [{ id: 'a1', name: 'Ada', role: '', status: 'online' }],
      colleagues: [{ id: 'c1', name: 'Ada', email: '', role: 'member' }],
    });
    const groups = boardGroups([task('t', { assigneeId: 'c1' }), task('u')], 'Assignee', dir);
    expect(groups.map((g) => [g.id, ids(g.tasks)])).toEqual([
      ['none', ['u']],
      ['a1', []],
      ['c1', ['t']],
    ]);
  });
  it('merges swimlanes that share a display name, in first-seen order', () => {
    const lanes = groupByName([task('a'), task('b'), task('c'), task('d')], (t) =>
      t.id === 'b' ? '' : 'Ada',
    );
    expect(lanes.map((l) => [l.name, ids(l.tasks)])).toEqual([
      ['Ada', ['a', 'c', 'd']],
      ['', ['b']],
    ]);
    expect(groupByName([], () => 'x')).toEqual([]);
  });
  it('orders sub-tasks under parents, roots orphans and survives cycles', () => {
    const rows = [
      task('child', { parentId: 'root' }),
      task('root'),
      task('orphan', { parentId: 'filtered-out' }),
      task('x', { parentId: 'y' }),
      task('y', { parentId: 'x' }),
    ];
    expect(ids(hierarchyOrder(rows))).toEqual(['root', 'child', 'orphan', 'x', 'y']);
  });
});

describe('task table cells', () => {
  const dir = directory({
    inquiries: [
      task('p'),
      task('k1', { parentId: 'p', status: 'done' }),
      task('k2', { parentId: 'p' }),
    ],
  });
  it('derives identifiers, sub-task progress and placeholders', () => {
    expect(tableValue(dir.inquiries[1]!, 'Identifier', dir)).toBe('LYK-2');
    expect(tableValue(dir.inquiries[0]!, 'Sub-task progress', dir)).toBe('1/2');
    expect(tableValue(dir.inquiries[1]!, 'Sub-task progress', dir)).toBe('—');
    expect(tableValue(dir.inquiries[0]!, 'Labels', dir)).toBe('Empty');
    expect(tableValue(dir.inquiries[0]!, 'Created by', dir)).toBe('Not recorded');
    expect(tableValue(dir.inquiries[0]!, 'Due date', dir)).toBe('Due date');
  });
  it('turns edited text into a patch, clearing empty fields', () => {
    expect(cellPatch('Labels', ' a, ,b ,')).toEqual({ labels: ['a', 'b'] });
    expect(cellPatch('Assignee', '')).toEqual({ assigneeId: null });
    expect(cellPatch('Start date', '')).toEqual({ startDate: null });
    expect(cellPatch('Due date', '2026-01-02')).toEqual({ dueDate: '2026-01-02' });
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

describe('run totals', () => {
  const run = (id: string, start: number, end?: number, costUsd?: number): ExecutionRun => ({
    id,
    name: id,
    agentId: null,
    status: 'succeeded',
    startedAt: new Date(start * 1000).toISOString(),
    endedAt: end === undefined ? undefined : new Date(end * 1000).toISOString(),
    costUsd,
  });
  it('reports totals only when every run reports them', () => {
    const runs = [run('a', 0, 10, 1), run('b', 5, 20, 2)];
    expect(totalCost(runs)).toBe(3);
    expect(agentSeconds(runs)).toBe(25);
    expect(elapsedSeconds(runs)).toBe(20);
    expect(totalCost([run('a', 0, 10, 1), run('b', 5, 20)])).toBeNull();
    expect(agentSeconds([run('a', 0, 10), run('b', 5)])).toBeNull();
    expect(elapsedSeconds([])).toBeNull();
    expect(runSeconds(run('a', 10, 5))).toBe(0);
  });
  it('draws the cumulative cost as steps scaled to the chart', () => {
    expect(costLine([run('a', 0, 10)])).toBe('');
    expect(costLine([run('a', 0, 10, 1), run('b', 10, 20, 1)])).toBe(
      '0,150 0,150 0,80 425,80 425,10 850,10',
    );
  });
  it('keeps instant runs visible on the timeline', () => {
    const runs = [run('a', 0, 0), run('b', 0, 100)];
    expect(runBar(runs[0]!, runs)).toEqual({ x: 0, width: 2 });
    expect(runBar(runs[1]!, runs)).toEqual({ x: 0, width: 850 });
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

describe('colours', () => {
  it('converts between hex and HSV', () => {
    expect(hexToHsv('#ff0000')).toEqual({ h: 0, s: 100, v: 100 });
    expect(hexToHsv('#0000FF').h).toBe(240);
    expect(hexToHsv('#808080').h).toBeNull();
    expect(hexToHsv('#000000')).toEqual({ h: null, s: 0, v: 0 });
    for (const hex of colorPresets) {
      const { h, s, v } = hexToHsv(hex);
      expect(hsvToHex(h ?? 0, s, v)).toBe(hex);
    }
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
