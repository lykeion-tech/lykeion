import { describe, expect, it } from 'vitest';
import { parseSpectrum, spectrumSticks, spectrumEnvelope, spectrumRatio } from './spectra';

describe('spectral previews', () => {
  it('reads separate MGF scans and their peaks', () => {
    const data = parseSpectrum(
      'BEGIN IONS\nTITLE=first\nPEPMASS=42\n10 5\n12.5 8\nEND IONS\nBEGIN IONS\nTITLE=second\n8 4\nEND IONS',
      'mgf',
      false,
    );
    expect(data?.scans).toEqual([
      {
        name: 'first',
        points: [
          [10, 5],
          [12.5, 8],
        ],
      },
      { name: 'second', points: [[8, 4]] },
    ]);
  });
  it('applies JCAMP scale factors to XY pairs and X++ Y values', () => {
    const points = parseSpectrum(
      '##TITLE=test\n##XFACTOR=2\n##YFACTOR=0.1\n##XYPOINTS=(XY..XY)\n1,10 2,20\n##END=',
      'jdx',
      false,
    );
    expect(points?.scans[0]?.points).toEqual([
      [2, 1],
      [4, 2],
    ]);
    const series = parseSpectrum(
      '##XFACTOR=2\n##YFACTOR=0.5\n##DELTAX=4\n##XYDATA=(X++(Y..Y))\n1 2 4 6\n##END=',
      'dx',
      false,
    );
    expect(series?.scans[0]?.points).toEqual([
      [2, 1],
      [6, 2],
      [10, 3],
    ]);
  });
  it('rejects compressed or incomplete data and previews of partial files', () => {
    expect(parseSpectrum('##XYDATA=(X++(Y..Y))\n1 A2 B3\n##END=', 'jdx', false)).toBeUndefined();
    expect(parseSpectrum('BEGIN IONS\n1 2', 'mgf', false)).toBeUndefined();
    expect(parseSpectrum('##XYPOINTS=(XY..XY)\n1 2\n##END=', 'dx', true)).toBeUndefined();
    expect(
      parseSpectrum('##NPOINTS=2\n##XYPOINTS=(XY..XY)\n1 2\n##END=', 'dx', false),
    ).toBeUndefined();
    expect(
      parseSpectrum(
        '##XYPOINTS=(XY..XY)\n1 2\n##END=\n##XYPOINTS=(XY..XY)\n2 3\n##END=',
        'dx',
        false,
      ),
    ).toBeUndefined();
  });
  it('accepts an optional charge but rejects unsupported peak annotations', () => {
    expect(parseSpectrum('BEGIN IONS\n10 2 2+\nEND IONS', 'mgf', false)?.scans[0]?.points).toEqual([
      [10, 2],
    ]);
    expect(parseSpectrum('BEGIN IONS\n10 2 unsupported\nEND IONS', 'mgf', false)).toBeUndefined();
    expect(parseSpectrum('BEGIN IONS\n10 2 2+ extra\nEND IONS', 'mgf', false)).toBeUndefined();
    expect(parseSpectrum('BEGIN IONS\n10 2 bad=x\nEND IONS', 'mgf', false)).toBeUndefined();
  });
  it('preserves strong peaks when reducing dense spectra', () => {
    const source: [number, number][] = Array.from({ length: 2001 }, (_, i) => [
      i,
      i === 101 ? 100 : 1,
    ]);
    expect(spectrumSticks(source, 1000)).toContainEqual([101, 100]);
  });
  it('scales distinct tiny values without a zero denominator', () => {
    expect(spectrumRatio(2e-323, 1.5e-323, 2e-323)).toBe(1);
    expect(spectrumRatio(0, -Number.MAX_VALUE, Number.MAX_VALUE)).toBe(0.5);
  });
  it('preserves narrow extrema in an ordered sampled curve', () => {
    const points: [number, number][] = Array.from({ length: 2001 }, (_, i) => [
      i,
      i === 101 ? 100 : 1,
    ]);
    const sampled = spectrumEnvelope(points, 500);
    expect(sampled).toContainEqual([101, 100]);
    expect(sampled.every((point, index) => index === 0 || point[0] >= sampled[index - 1]![0])).toBe(
      true,
    );
  });
});
