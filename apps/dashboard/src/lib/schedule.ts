// Autoresearch schedules: a time of day or an interval inside a daily window, on every day,
// chosen weekdays (0 = Sunday) or one day of the month. A cron expression overrides all of it.
export type Schedule = {
  kind: 'schedule';
  timeMode: 'time' | 'interval';
  time: string;
  interval: number;
  unit: 'minutes' | 'hours';
  windowStart: string;
  windowEnd: string;
  dayMode: 'every' | 'week' | 'month';
  weekdays: number[];
  monthDay: number;
  timezone: string;
  cron: string;
};

export const weekdayAbbreviations = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

export function toggleWeekday(days: number[], day: number) {
  return days.includes(day) ? days.filter((x) => x !== day) : [...days, day].sort();
}

function dayText(s: Schedule, monthDay: string) {
  return s.dayMode === 'every'
    ? 'Every day'
    : s.dayMode === 'week'
      ? s.weekdays.map((x) => weekdayAbbreviations[x]).join(', ')
      : monthDay;
}

// The sentence stored with the autoresearch.
export function scheduleSummary(s: Schedule) {
  if (s.cron) return `Cron ${s.cron} (${s.timezone})`;
  const timing =
    s.timeMode === 'time'
      ? 'At ' + s.time
      : `Every ${s.interval} ${s.unit} between ${s.windowStart} and ${s.windowEnd}`;
  return `${timing} · ${dayText(s, `Day ${s.monthDay} of the month`)} · ${s.timezone}`;
}

// The shorter line shown under the editor; the window only appears when it is not all day.
export function scheduleLabel(s: Schedule) {
  if (s.cron) return s.cron;
  const timing =
    s.timeMode === 'time'
      ? `At ${s.time}`
      : s.unit === 'hours'
        ? s.interval === 1
          ? 'Every hour at :00'
          : `Every ${s.interval} hours`
        : `Every ${s.interval} minutes`;
  const window =
    s.timeMode === 'interval' && (s.windowStart !== '00:00' || s.windowEnd !== '23:00')
      ? ' · ' + s.windowStart + '–' + s.windowEnd
      : '';
  return `${timing} · ${dayText(s, `Day ${s.monthDay}`)}${window}`;
}

export function scheduleError(s: Schedule) {
  return s.dayMode === 'week' && !s.weekdays.length ? 'Choose at least one day of the week.' : '';
}
