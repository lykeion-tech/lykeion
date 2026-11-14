import { expect, it } from 'vitest';
import { parseJsonLines, plotFraction } from './json-lines';

it('collects columns across rows and represents nested values as JSON', () => {
  expect(parseJsonLines('{"a":1,"nested":{"x":2}}\n{"b":true}\n', false)).toMatchObject({
    columns: ['a', 'nested', 'b'],
    rows: [
      ['1', '{"x":2}', ''],
      ['', '', 'true'],
    ],
    partial: false,
  });
});
it('drops an incomplete final line when input is truncated', () => {
  expect(parseJsonLines('{"a":1}\n{"a":', true)).toMatchObject({ rows: [['1']], partial: true });
});
it('reports malformed complete lines and limits sampled rows', () => {
  expect(parseJsonLines('{"a":1}\ninvalid\n', false)).toMatchObject({
    error: 'Invalid JSON on line 2.',
  });
  const result = parseJsonLines(
    Array.from({ length: 101 }, (_, i) => JSON.stringify({ i })).join('\n'),
    false,
  );
  expect(result.rows).toHaveLength(100);
  expect(result.partial).toBe(true);
});

it('keeps empty objects visible as records', () => {
  expect(parseJsonLines('{}\n{}\n', false)).toMatchObject({
    columns: ['Value'],
    rows: [['{}'], ['{}']],
  });
});

it('keeps JSON numbers distinct from numeric strings for plotting and sorting', () => {
  expect(parseJsonLines('{"x":3}\n{"x":"4"}\n{"x":null}\n', false).numbers).toEqual([
    [3],
    [null],
    [null],
  ]);
});

it('flags numbers outside precise range and preserves overflow as a visible value', () => {
  const result = parseJsonLines('{"a":9007199254740993,"b":1e400,"c":{"n":1e400}}\n', false);
  expect(result.precisionWarning).toBe(true);
  expect(result.rows[0][1]).toBe('Infinity');
  expect(result.rows[0][2]).toContain('Infinity');
  expect(result.numbers[0]).toEqual([null, null, null]);
});

it('warns when a nonzero JSON number underflows to zero', () => {
  expect(parseJsonLines('{"x":1e-400}\n', false).precisionWarning).toBe(true);
  expect(parseJsonLines('{"x":"1e-400"}\n', false).precisionWarning).toBe(false);
});

it('reports deeply nested input as an unsupported preview limit', () => {
  const nested = '['.repeat(3000) + '0' + ']'.repeat(3000);
  expect(parseJsonLines(`${nested}\n`, false).error).toContain('too deeply nested');
});

it('plots distinct smallest subnormal and extreme values distinctly', () => {
  expect(plotFraction(0, 0, 5e-324)).toBe(0);
  expect(plotFraction(5e-324, 0, 5e-324)).toBe(1);
  expect(plotFraction(1e308, -1e308, 1e308)).toBe(1);
});
