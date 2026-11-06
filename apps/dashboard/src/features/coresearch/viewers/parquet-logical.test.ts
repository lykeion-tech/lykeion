import { expect, it } from 'vitest';
import { logicalColumn } from './parquet-logical';
import type { CompactValue } from './compact-thrift';
const m = (pairs: [number, CompactValue][]) => new Map(pairs);
it('formats exact signed decimals and rejects values beyond declared precision', () => {
  const schema = m([
    [
      10,
      m([
        [
          5,
          m([
            [1, 2n],
            [2, 5n],
          ]),
        ],
      ]),
    ],
  ]);
  const x = logicalColumn(schema, 1, 'INT32');
  expect(x.format(-1n)).toBe('-0.01');
  expect(x.format(0n)).toBe('0.00');
  expect(() => x.format(100000n)).toThrow();
});
it('rejects inconsistent or unsupported logical annotations', () => {
  const cases: [Map<number, CompactValue>, number][] = [
    [
      m([
        [
          10,
          m([
            [
              5,
              m([
                [1, 6n],
                [2, 5n],
              ]),
            ],
          ]),
        ],
      ]),
      1,
    ],
    [
      m([
        [
          10,
          m([
            [
              5,
              m([
                [1, 0n],
                [2, 19n],
              ]),
            ],
          ]),
        ],
      ]),
      2,
    ],
    [
      m([
        [6, 6n],
        [10, m([[1, m([])]])],
      ]),
      1,
    ],
    [
      m([
        [
          10,
          m([
            [
              8,
              m([
                [1, true],
                [2, m([[4, m([])]])],
              ]),
            ],
          ]),
        ],
      ]),
      2,
    ],
    [m([[10, m([[6, m([])]])]]), 2],
  ];
  for (const [schema, type] of cases) expect(() => logicalColumn(schema, type, 'INT')).toThrow();
});
it('rejects invalid time of day while retaining exact integer nanos', () => {
  const s = m([
    [
      10,
      m([
        [
          7,
          m([
            [1, false],
            [2, m([[3, m([])]])],
          ]),
        ],
      ]),
    ],
  ]);
  const x = logicalColumn(s, 2, 'INT64');
  expect(x.format(86399999999999n)).toBe('86399999999999');
  expect(() => x.format(-1n)).toThrow();
  expect(() => x.format(86400000000000n)).toThrow();
});
