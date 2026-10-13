// Calendar dates travel as local YYYY-MM-DD keys; Date objects are local midnights.

export function shortDate(value?: string | null) {
  return value
    ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
    : '';
}

export function dateKey(d: Date) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

// Anything that is not a complete key (an empty field included) opens on now.
export function parseDateKey(s: string) {
  const [y, m, d] = s.split('-').map(Number);
  return y && m && d ? new Date(y, m - 1, d) : new Date();
}

// Whole weeks, Sunday first, covering the month that `month` falls in.
export function monthWeeks(month: Date) {
  const first = new Date(month.getFullYear(), month.getMonth(), 1);
  const count =
    Math.ceil(
      (first.getDay() + new Date(first.getFullYear(), first.getMonth() + 1, 0).getDate()) / 7,
    ) * 7;
  const days = Array.from(
    { length: count },
    (_, i) => new Date(first.getFullYear(), first.getMonth(), 1 - first.getDay() + i),
  );
  return Array.from({ length: count / 7 }, (_, i) => days.slice(i * 7, i * 7 + 7));
}

export const weekdayNames = [
  'Sunday',
  'Monday',
  'Tuesday',
  'Wednesday',
  'Thursday',
  'Friday',
  'Saturday',
];

// "Today, Friday, March 13th, 2026"
export function longDateLabel(d: Date, today = new Date()) {
  const n = d.getDate(),
    suffix = n % 100 >= 11 && n % 100 <= 13 ? 'th' : (['th', 'st', 'nd', 'rd'][n % 10] ?? 'th');
  return `${dateKey(d) === dateKey(today) ? 'Today, ' : ''}${weekdayNames[d.getDay()]}, ${d.toLocaleDateString('en-US', { month: 'long' })} ${n}${suffix}, ${d.getFullYear()}`;
}

// Date-grid keyboard movement; null for keys the grid does not handle. Page keys move a
// month and clamp to its last day.
export function moveDate(from: Date, key: string): Date | null {
  const d = new Date(from);
  switch (key) {
    case 'ArrowLeft':
      d.setDate(d.getDate() - 1);
      return d;
    case 'ArrowRight':
      d.setDate(d.getDate() + 1);
      return d;
    case 'ArrowUp':
      d.setDate(d.getDate() - 7);
      return d;
    case 'ArrowDown':
      d.setDate(d.getDate() + 7);
      return d;
    case 'Home':
      d.setDate(d.getDate() - d.getDay());
      return d;
    case 'End':
      d.setDate(d.getDate() + 6 - d.getDay());
      return d;
    case 'PageUp':
    case 'PageDown': {
      const delta = key === 'PageUp' ? -1 : 1;
      const max = new Date(d.getFullYear(), d.getMonth() + delta + 1, 0).getDate();
      return new Date(d.getFullYear(), d.getMonth() + delta, Math.min(d.getDate(), max));
    }
    default:
      return null;
  }
}

export function formatDuration(seconds?: number | null) {
  return seconds === undefined || seconds === null
    ? 'Not reported'
    : seconds < 60
      ? `${Math.round(seconds)}s`
      : `${Math.floor(seconds / 60)}m ${Math.round(seconds % 60)}s`;
}
