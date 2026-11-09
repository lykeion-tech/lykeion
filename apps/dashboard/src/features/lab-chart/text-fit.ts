// Largest font size not above height*fill whose measured text width fits within width*0.92
// (pass 0.94 for chalkboards via `margin`).
export function fitFontSize(
  _text: string,
  measure: (size: number) => number,
  width: number,
  height: number,
  fill: number,
  margin = 0.92,
): number {
  const max = height * fill
  if (measure(max) <= width * margin) return max
  const w1 = measure(1)
  return w1 > 0 ? Math.min(max, (width * margin) / w1) : max
}
