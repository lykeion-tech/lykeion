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
