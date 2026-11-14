// Copied from Lykeion-L2 lib/diff.ts; non-null assertions adapt strict indexing.
/**
 * A line-level unified diff, as `-`/`+`/space-prefixed lines that a
 * `CodeBlock lang="diff"` renders.
 *
 * A real LCS diff, not "all removed then all added", so an edit touching two
 * lines of twenty shows exactly those two.
 *
 * It lives in `lib/` rather than beside either caller because it has two: the
 * transcript's tool-step card, which draws an agent edit's diff, and the
 * artifact pane, which draws the difference between two saved versions.
 */

/**
 * The most lines, per side, a diff is laid out for.
 *
 * The table below is O(n·m) in MEMORY as well as in time — one slot per PAIR
 * of lines — so its cost grows with the product of the two sides rather than
 * with their sum. That is free for the transcript's caller, whose two sides
 * are one edit's `old_string` and `new_string`. It is not free for the
 * artifact pane's, whose two sides are whole saved versions of a file,
 * bounded only by `PREVIEW_MAX_BYTES` (8 MB): two 400 KB text saves — an
 * ordinary `.py`, `.md` or run log — are around 10⁴ lines each, which is 10⁸
 * slots. That is hundreds of megabytes and a second or more of frozen main
 * thread, which is a killed tab rather than a slow diff.
 *
 * 2500 lines a side is what buys that back: 2501² ≈ 6.3M slots, which V8
 * holds as a packed integer array at roughly 25 MB (4 bytes a slot under
 * pointer compression, 50 MB without) and walks in tens of milliseconds.
 * That is the cost in the LARGEST pair this still diffs; every ordinary
 * source file, note or notebook is far below it.
 */
export const DIFF_MAX_LINES = 2500;

/**
 * The same ceiling in bytes, for a caller holding a version's ROW rather than
 * its bytes — the artifact pane's compare picker, which knows each version's
 * `size` and would have to fetch the file to count its lines.
 *
 * A proxy, calibrated at roughly a hundred bytes a line, and deliberately a
 * coarse one: it is here so the picker does not offer, and diff mode does not
 * open on, a target `boundedUnifiedDiff` is then going to refuse. The line
 * ceiling above remains the one that actually bounds the table, because a
 * quarter of a megabyte of one-character lines is 128k lines.
 */
export const DIFF_MAX_BYTES = 256 * 1024;

/** A diff, or the refusal to lay one out — see `boundedUnifiedDiff`. */
export type BoundedDiff =
  { kind: 'diff'; text: string } | { kind: 'too-large'; oldLines: number; newLines: number };

/**
 * A unified diff, or a refusal when either side is over `DIFF_MAX_LINES`.
 *
 * A function of its own rather than a flag on `unifiedDiff`: the transcript's
 * caller diffs one edit's two strings, cannot reach the refusal, and renders
 * the `string` it gets back directly — making it handle an outcome it never
 * receives would buy nothing.
 */
export function boundedUnifiedDiff(oldText: string, newText: string): BoundedDiff {
  const a = oldText.split('\n');
  const b = newText.split('\n');
  if (a.length > DIFF_MAX_LINES || b.length > DIFF_MAX_LINES)
    return { kind: 'too-large', oldLines: a.length, newLines: b.length };
  return { kind: 'diff', text: diffLines(a, b) };
}

export function unifiedDiff(oldText: string, newText: string): string {
  return diffLines(oldText.split('\n'), newText.split('\n'));
}

/** The diff itself, over lines already split — so the bounded entry point
 *  above can count them without splitting twice. */
function diffLines(a: string[], b: string[]): string {
  const n = a.length;
  const m = b.length;
  // Longest common subsequence of lines, bottom-up.
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i]![j] = a[i] === b[j] ? dp[i + 1]![j + 1]! + 1 : Math.max(dp[i + 1]![j]!, dp[i]![j + 1]!);
    }
  }
  const out: string[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push(` ${a[i]}`);
      i++;
      j++;
    } else if (dp[i + 1]![j]! >= dp[i]![j + 1]!) {
      out.push(`-${a[i]}`);
      i++;
    } else {
      out.push(`+${b[j]}`);
      j++;
    }
  }
  while (i < n) out.push(`-${a[i++]}`);
  while (j < m) out.push(`+${b[j++]}`);
  return out.join('\n');
}
