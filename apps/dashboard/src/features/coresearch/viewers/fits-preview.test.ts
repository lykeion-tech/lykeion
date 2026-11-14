import { describe, it, expect } from 'vitest';
import { readFits } from './fits-preview';
function fits(cards: string[], bytes: number[]) {
  const card = (line: string) => line.padEnd(80, ' ');
  const header = new TextEncoder().encode([...cards, 'END'].map(card).join('').padEnd(2880, ' '));
  const body = new Uint8Array(Math.ceil(bytes.length / 2880) * 2880);
  body.set(bytes);
  return new Blob([header, body]);
}
describe('FITS preview', () => {
  it('samples a valid image larger than 32 MiB', async () => {
    const count = 33 * 1024 * 1024;
    const cards = [
      'SIMPLE  =                    T',
      'BITPIX  =                    8',
      'NAXIS   =                    1',
      `NAXIS1  = ${String(count).padStart(20)}`,
      'END',
    ];
    const header = new TextEncoder().encode(
      cards
        .map((card) => card.padEnd(80, ' '))
        .join('')
        .padEnd(2880, ' '),
    );
    const body = new Uint8Array(Math.ceil(count / 2880) * 2880);
    const result = await readFits(new Blob([header, body]));
    expect(result?.shape).toEqual([count]);
    expect(result?.values.length).toBeLessThanOrEqual(100);
    expect(result?.values.every((value) => value === '0')).toBe(true);
  });
  it('decodes big-endian scaled integer images with FITS axis order', async () => {
    const b = fits(
      [
        'SIMPLE  =                    T',
        'BITPIX  =                   16',
        'NAXIS   =                    2',
        'NAXIS1  =                    2',
        'NAXIS2  =                    2',
        'BSCALE  =                  2.0',
        'BZERO   =                 10.0',
      ],
      [0, 1, 0, 2, 0, 3, 0, 4],
    );
    expect(await readFits(b)).toMatchObject({ shape: [2, 2], values: ['12', '14', '16', '18'] });
  });
  it('uses FITS axis 1 as the fastest-changing image coordinate', async () => {
    const b = fits(
      [
        'SIMPLE  =                    T',
        'BITPIX  =                    8',
        'NAXIS   =                    2',
        'NAXIS1  =                    3',
        'NAXIS2  =                    2',
      ],
      [0, 1, 2, 3, 4, 5],
    );
    expect((await readFits(b))?.values).toEqual(['0', '1', '2', '3', '4', '5']);
  });
  it('rejects unsafe integer offset used for unsigned 64-bit images', async () => {
    const b = fits(
      [
        'SIMPLE  =                    T',
        'BITPIX  =                   64',
        'NAXIS   =                    1',
        'NAXIS1  =                    1',
        'BZERO   =  9223372036854775808',
      ],
      [0, 0, 0, 0, 0, 0, 0, 0],
    );
    expect(await readFits(b)).toBeUndefined();
  });
  it('rejects integer scaling whose intermediate product loses precision', async () => {
    const bytes = new Uint8Array(8);
    new DataView(bytes.buffer).setBigInt64(0, 4503599627370497n, false);
    const b = fits(
      [
        'SIMPLE  =                    T',
        'BITPIX  =                   64',
        'NAXIS   =                    1',
        'NAXIS1  =                    1',
        'BSCALE  =                    3',
        'BZERO   =    -9007199254740991',
      ],
      [...bytes],
    );
    expect(await readFits(b)).toBeUndefined();
  });
  it('selects slices on axes above the image plane', async () => {
    const b = fits(
      [
        'SIMPLE  =                    T',
        'BITPIX  =                    8',
        'NAXIS   =                    3',
        'NAXIS1  =                    2',
        'NAXIS2  =                    2',
        'NAXIS3  =                    2',
      ],
      [0, 1, 2, 3, 4, 5, 6, 7],
    );
    expect((await readFits(b, [1]))?.values).toEqual(['4', '5', '6', '7']);
    expect(await readFits(b, [2])).toBeUndefined();
  });
  it('rejects malformed, truncated, unsafe and unsupported headers', async () => {
    expect(await readFits(new Blob(['bad']))).toBeUndefined();
    expect(
      await readFits(
        fits(
          [
            'SIMPLE  =                    T',
            'BITPIX  =                   12',
            'NAXIS   =                    1',
            'NAXIS1  =                    1',
          ],
          [0],
        ),
      ),
    ).toBeUndefined();
    expect(
      await readFits(
        fits(
          [
            'SIMPLE  =                    T',
            'BITPIX  =                   64',
            'NAXIS   =                    1',
            'NAXIS1  =                    1',
          ],
          [127, 255, 255, 255, 255, 255, 255, 255],
        ),
      ),
    ).toBeUndefined();
    expect(
      await readFits(
        fits(
          [
            'SIMPLE  =                    T',
            'BITPIX  =                   16',
            'NAXIS   =                    1',
            'NAXIS1  =                 5000',
          ],
          [0],
        ),
      ),
    ).toBeUndefined();
  });
});

it('finds an IMAGE extension after an empty primary HDU and applies its scaling', async () => {
  const primary = fits(
    [
      'SIMPLE  =                    T',
      'BITPIX  =                    8',
      'NAXIS   =                    0',
    ],
    [],
  );
  const image = fits(
    [
      "XTENSION= 'IMAGE   '",
      'BITPIX  =                   16',
      'NAXIS   =                    2',
      'NAXIS1  =                    2',
      'NAXIS2  =                    1',
      'PCOUNT  =                    0',
      'GCOUNT  =                    1',
      'BSCALE  =                  2.0',
      'BZERO   =                 10.0',
      "EXTNAME = 'SCI     '",
    ],
    [0, 1, 0, 2],
  );
  const file = new Blob([primary, image]);
  const { listFitsHdus } = await import('./fits-preview');
  expect((await listFitsHdus(file))?.map((h) => h.supported)).toEqual([false, true]);
  expect((await readFits(file, undefined, 1))?.values).toEqual(['12', '14']);
  expect(await readFits(file, undefined, 0)).toBeUndefined();
});
it('does not decode table HDUs and rejects truncated extensions', async () => {
  const primary = fits(
    [
      'SIMPLE  =                    T',
      'BITPIX  =                    8',
      'NAXIS   =                    0',
    ],
    [],
  );
  const table = fits(
    [
      "XTENSION= 'BINTABLE'",
      'BITPIX  =                    8',
      'NAXIS   =                    2',
      'NAXIS1  =                    2',
      'NAXIS2  =                    1',
      'PCOUNT  =                    0',
      'GCOUNT  =                    1',
    ],
    [1, 2],
  );
  const { listFitsHdus } = await import('./fits-preview');
  expect((await listFitsHdus(new Blob([primary, table])))?.[1]?.supported).toBe(false);
  expect(await readFits(new Blob([primary, table]), undefined, 1)).toBeUndefined();
  expect(await listFitsHdus(new Blob([primary, table.slice(0, 2880)]))).toBeUndefined();
});

it('skips a table without interpreting it and reads a later image extension', async () => {
  const primary = fits(
    [
      'SIMPLE  =                    T',
      'BITPIX  =                    8',
      'NAXIS   =                    0',
    ],
    [],
  );
  const table = fits(
    [
      "XTENSION= 'BINTABLE'",
      'BITPIX  =                    8',
      'NAXIS   =                    2',
      'NAXIS1  =                    2',
      'NAXIS2  =                    1',
      'PCOUNT  =                    0',
      'GCOUNT  =                    1',
    ],
    [1, 2],
  );
  const image = fits(
    [
      "XTENSION= 'IMAGE   '",
      'BITPIX  =                   16',
      'NAXIS   =                    1',
      'NAXIS1  =                    2',
      'PCOUNT  =                    0',
      'GCOUNT  =                    1',
      'BLANK   =                   -1',
    ],
    [255, 255, 0, 7],
  );
  const file = new Blob([primary, table, image]);
  const { listFitsHdus } = await import('./fits-preview');
  expect((await listFitsHdus(file))?.map((h) => h.supported)).toEqual([false, false, true]);
  expect((await readFits(file, undefined, 2))?.values).toEqual(['blank', '7']);
});
it('rejects malformed table layouts before seeking a later HDU', async () => {
  const primary = fits(
    [
      'SIMPLE  =                    T',
      'BITPIX  =                    8',
      'NAXIS   =                    0',
    ],
    [],
  );
  const table = (extra: string[] = []) =>
    fits(
      [
        "XTENSION= 'BINTABLE'",
        'BITPIX  =                    8',
        'NAXIS   =                    2',
        'NAXIS1  =                    2',
        'NAXIS2  =                    1',
        'PCOUNT  =                    0',
        'GCOUNT  =                    1',
        ...extra,
      ],
      [1, 2],
    );
  const { listFitsHdus } = await import('./fits-preview');
  expect(
    await listFitsHdus(new Blob([primary, table(['THEAP   =                    3'])])),
  ).toBeUndefined();
  const invalidBitpix = fits(
    [
      "XTENSION= 'BINTABLE'",
      'BITPIX  =                   16',
      'NAXIS   =                    2',
      'NAXIS1  =                    1',
      'NAXIS2  =                    1',
      'PCOUNT  =                    0',
      'GCOUNT  =                    1',
    ],
    [1, 2],
  );
  expect(await listFitsHdus(new Blob([primary, invalidBitpix]))).toBeUndefined();
  const invalidNaxis = fits(
    [
      "XTENSION= 'BINTABLE'",
      'BITPIX  =                    8',
      'NAXIS   =                    1',
      'NAXIS1  =                    2',
      'PCOUNT  =                    0',
      'GCOUNT  =                    1',
    ],
    [1, 2],
  );
  expect(await listFitsHdus(new Blob([primary, invalidNaxis]))).toBeUndefined();
});
it('accepts a binary table with a correctly bounded heap', async () => {
  const primary = fits(
    [
      'SIMPLE  =                    T',
      'BITPIX  =                    8',
      'NAXIS   =                    0',
    ],
    [],
  );
  const table = fits(
    [
      "XTENSION= 'BINTABLE'",
      'BITPIX  =                    8',
      'NAXIS   =                    2',
      'NAXIS1  =                    2',
      'NAXIS2  =                    1',
      'PCOUNT  =                    4',
      'GCOUNT  =                    1',
      'THEAP   =                    4',
    ],
    [1, 2, 0, 0, 3, 4],
  );
  const { listFitsHdus } = await import('./fits-preview');
  expect((await listFitsHdus(new Blob([primary, table])))?.[1]?.supported).toBe(false);
});
it('rejects nonzero PCOUNT in an ASCII table', async () => {
  const primary = fits(
    [
      'SIMPLE  =                    T',
      'BITPIX  =                    8',
      'NAXIS   =                    0',
    ],
    [],
  );
  const table = fits(
    [
      "XTENSION= 'TABLE   '",
      'BITPIX  =                    8',
      'NAXIS   =                    2',
      'NAXIS1  =                    2',
      'NAXIS2  =                    1',
      'PCOUNT  =                    1',
      'GCOUNT  =                    1',
    ],
    [1, 2, 3],
  );
  const { listFitsHdus } = await import('./fits-preview');
  expect(await listFitsHdus(new Blob([primary, table]))).toBeUndefined();
});
