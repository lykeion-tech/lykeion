export const colorPresets = [
  '#6b7280',
  '#ef4444',
  '#f97316',
  '#eab308',
  '#22c55e',
  '#14b8a6',
  '#3b82f6',
  '#6366f1',
  '#a855f7',
  '#ec4899',
];

// Hue in degrees, saturation and brightness in percent. Greys have no hue, so `h` is null
// and the caller keeps the hue it had.
export function hexToHsv(value: string) {
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(value.slice(i, i + 2), 16) / 255) as [
    number,
    number,
    number,
  ];
  const max = Math.max(r, g, b),
    min = Math.min(r, g, b),
    delta = max - min;
  return {
    h: delta
      ? ((max === r ? (g - b) / delta : max === g ? (b - r) / delta + 2 : (r - g) / delta + 4) *
          60 +
          360) %
        360
      : null,
    s: max ? (delta / max) * 100 : 0,
    v: max * 100,
  };
}

export function hsvToHex(hue: number, saturation: number, brightness: number) {
  const h = hue / 60,
    c = ((brightness / 100) * saturation) / 100,
    x = c * (1 - Math.abs((h % 2) - 1)),
    m = brightness / 100 - c;
  const rgb =
    h < 1
      ? [c, x, 0]
      : h < 2
        ? [x, c, 0]
        : h < 3
          ? [0, c, x]
          : h < 4
            ? [0, x, c]
            : h < 5
              ? [x, 0, c]
              : [c, 0, x];
  return (
    '#' +
    rgb
      .map((v) =>
        Math.round((v + m) * 255)
          .toString(16)
          .padStart(2, '0'),
      )
      .join('')
  );
}
