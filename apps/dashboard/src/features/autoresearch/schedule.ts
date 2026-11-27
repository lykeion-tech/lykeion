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

const hour = (t: string) => Number(t.split(':')[0]);

// The five-field cron the server runs, in the schedule's timezone. An interval's daily window
// becomes an hour range; a cron typed by hand is passed through.
export function scheduleCron(s: Schedule) {
  if (s.cron.trim()) return s.cron.trim();
  const dom = s.dayMode === 'month' ? String(s.monthDay) : '*';
  const dow = s.dayMode === 'week' ? s.weekdays.join(',') : '*';
  const tail = `${dom} * ${dow}`;
  if (s.timeMode === 'time') {
    const [h, m] = s.time.split(':').map(Number);
    return `${m} ${h} ${tail}`;
  }
  const allDay = s.windowStart === '00:00' && s.windowEnd === '23:00';
  const hours = allDay ? '*' : `${hour(s.windowStart)}-${hour(s.windowEnd)}`;
  if (s.unit === 'minutes') return `*/${s.interval} ${hours} ${tail}`;
  return `0 ${hours}/${s.interval} ${tail}`;
}

export function scheduleError(s: Schedule) {
  return s.dayMode === 'week' && !s.weekdays.length ? 'Choose at least one day of the week.' : '';
}
