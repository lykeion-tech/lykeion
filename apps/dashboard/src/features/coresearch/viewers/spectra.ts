type Point = [number, number];
type Scan = { name: string; points: Point[] };
const numeric = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/;
const limit = 50_000;

export function spectrumRatio(value: number, lo: number, hi: number): number {
  if (lo === hi) return 0.5;
  const range = hi - lo;
  return Number.isFinite(range) ? (value - lo) / range : (value / 2 - lo / 2) / (hi / 2 - lo / 2);
}

export function spectrumEnvelope(points: Point[], bins: number): Point[] {
  if (points.length <= bins) return points;
  const indices = new Set<number>([0, points.length - 1]);
  for (let bin = 0; bin < bins; bin++) {
    const start = Math.floor((bin * points.length) / bins);
    const end = Math.floor(((bin + 1) * points.length) / bins);
    let min = start,
      max = start;
    for (let i = start + 1; i < end; i++) {
      if (points[i]![1] < points[min]![1]) min = i;
      if (points[i]![1] > points[max]![1]) max = i;
    }
    indices.add(min);
    indices.add(max);
  }
  return [...indices].sort((a, b) => a - b).map((index) => points[index]!);
}

function numbers(line: string): number[] | undefined {
  const values = line.trim().split(/[\s,;]+/);
  if (!values.length || values.some((v) => !numeric.test(v))) return;
  const parsed = values.map(Number);
  return parsed.every(Number.isFinite) ? parsed : undefined;
}

function mgf(lines: string[]): Scan[] | undefined {
  const scans: Scan[] = [];
  let scan: Scan | undefined;
  let total = 0;
  for (const raw of lines) {
    const line = raw.trim();
    if (!line || /^[#!;]/.test(line)) continue;
    if (line === 'BEGIN IONS') {
      if (scan || scans.length >= 100) return;
      scan = { name: `Scan ${scans.length + 1}`, points: [] };
    } else if (line === 'END IONS') {
      if (!scan?.points.length) return;
      scans.push(scan);
      scan = undefined;
    } else if (line.includes('=')) {
      if (!/^[A-Za-z][A-Za-z0-9_]*=/.test(line)) return;
      if (scan && /^TITLE=/i.test(line))
        scan.name = line.slice(line.indexOf('=') + 1).slice(0, 200);
    } else {
      if (!scan) return;
      const values = line.split(/\s+/);
      if (
        values.length < 2 ||
        values.length > 3 ||
        !numeric.test(values[0]!) ||
        !numeric.test(values[1]!)
      )
        return;
      if (values[2] && !/^(?:\d+[+-]?|[+-]\d+)$/.test(values[2])) return;
      const x = Number(values[0]),
        y = Number(values[1]);
      if (!Number.isFinite(x) || !Number.isFinite(y) || ++total > limit) return;
      scan.points.push([x, y]);
    }
  }
  return !scan && scans.length ? scans : undefined;
}

export function spectrumSticks(points: Point[], bins: number): Point[] {
  if (points.length <= bins) return points;
  let lo = Infinity,
    hi = -Infinity;
  for (const [x] of points) {
    lo = Math.min(lo, x);
    hi = Math.max(hi, x);
  }
  const groups = new Map<number, { low: Point; high: Point }>();
  for (const point of points) {
    const ratio = spectrumRatio(point[0], lo, hi);
    const index = Math.min(bins - 1, Math.floor(ratio * bins));
    const group = groups.get(index);
    if (!group) groups.set(index, { low: point, high: point });
    else {
      if (point[1] < group.low[1]) group.low = point;
      if (point[1] > group.high[1]) group.high = point;
    }
  }
  return [...groups.values()].flatMap(({ low, high }) => (low === high ? [low] : [low, high]));
}

function jcamp(lines: string[]): Scan[] | undefined {
  let name = 'Spectrum',
    xFactor = 1,
    yFactor = 1,
    delta: number | undefined;
  let count: number | undefined;
  let mode: 'pairs' | 'increment' | undefined,
    ended = false;
  const points: Point[] = [];
  for (const raw of lines) {
    const line = raw.trim();
    if (!line || line.startsWith('$$')) continue;
    if (ended) return;
    if (line.startsWith('##')) {
      const match = /^##([^=]+)=(.*)$/.exec(line);
      if (!match) return;
      const key = match[1]!
          .trim()
          .toUpperCase()
          .replace(/[\s_-]/g, ''),
        value = match[2]!.trim();
      if (key === 'TITLE') name = value.slice(0, 200);
      if (key === 'NPOINTS') {
        count = Number(value);
        if (!/^\d+$/.test(value) || !Number.isSafeInteger(count) || count > limit) return;
      }
      if (key === 'XFACTOR' || key === 'YFACTOR' || key === 'DELTAX') {
        if (!numeric.test(value) || !Number.isFinite(Number(value))) return;
        if (key === 'XFACTOR') xFactor = Number(value);
        else if (key === 'YFACTOR') yFactor = Number(value);
        else delta = Number(value);
      }
      if (key === 'XYPOINTS' || key === 'PEAKTABLE' || key === 'XYDATA') {
        if (mode) return;
        const format = value.toUpperCase().replace(/\s/g, '');
        if (format === '(XY..XY)') mode = 'pairs';
        else if (format === '(X++(Y..Y))') mode = 'increment';
        else return;
      } else if (key === 'END') {
        ended = true;
        continue;
      } else if (mode) return;
      continue;
    }
    if (!mode) return;
    const values = numbers(line);
    if (!values || values.length < 2 || (mode === 'pairs' && values.length % 2)) return;
    if (mode === 'increment' && values.length > 2 && delta === undefined) return;
    if (mode === 'pairs') {
      for (let i = 0; i < values.length; i += 2)
        points.push([values[i]! * xFactor, values[i + 1]! * yFactor]);
    } else {
      for (let i = 1; i < values.length; i++)
        points.push([values[0]! * xFactor + (i - 1) * (delta ?? 0), values[i]! * yFactor]);
    }
    if (points.length > limit) return;
  }
  return ended &&
    points.length &&
    (count === undefined || count === points.length) &&
    points.every(([x, y]) => Number.isFinite(x) && Number.isFinite(y))
    ? [{ name, points }]
    : undefined;
}

export function parseSpectrum(
  text: string,
  extension: string,
  partial: boolean,
): { scans: Scan[] } | undefined {
  if (partial || text.length > 1_048_576) return;
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  const scans =
    extension === 'mgf' ? mgf(lines) : ['jdx', 'dx'].includes(extension) ? jcamp(lines) : undefined;
  return scans ? { scans } : undefined;
}
