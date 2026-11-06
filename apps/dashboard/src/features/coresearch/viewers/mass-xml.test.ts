import { describe, it, expect } from 'vitest';
import { readMassXml } from './mass-xml';
function b64(values: number[], little: boolean) {
  const bytes = new Uint8Array(values.length * 4);
  const d = new DataView(bytes.buffer);
  values.forEach((v, i) => d.setFloat32(i * 4, v, little));
  return btoa(String.fromCharCode(...bytes));
}
const mzxml = (payload: string, count = 2) =>
  `<mzXML><msRun><scan num="1" peaksCount="${count}" retentionTime="PT2S"><peaks precision="32" byteOrder="network" compressionType="none">${payload}</peaks></scan></msRun></mzXML>`;
describe('mass spectrometry XML', () => {
  it('reads mzXML network-order paired spectra', async () => {
    const p = await readMassXml(mzxml(b64([100, 5, 200, 10], false)), 'mzxml');
    expect(p?.spectra[0]?.peaks).toEqual([
      { mz: 100, intensity: 5 },
      { mz: 200, intensity: 10 },
    ]);
    expect(p?.spectra[0]?.tic).toBe(15);
  });
  it('reads uncompressed mzML little-endian binary arrays', async () => {
    const array = (term: string, data: number[]) =>
      `<binaryDataArray><cvParam accession="${term}"/><cvParam accession="MS:1000521"/><cvParam accession="MS:1000576"/><binary>${b64(data, true)}</binary></binaryDataArray>`;
    const xml = `<mzML><run><spectrumList count="1"><spectrum id="scan=1" defaultArrayLength="2"><binaryDataArrayList count="2">${array('MS:1000514', [100, 200])}${array('MS:1000515', [5, 10])}</binaryDataArrayList></spectrum></spectrumList></run></mzML>`;
    expect((await readMassXml(xml, 'mzml'))?.spectra[0]?.peaks[1]).toEqual({
      mz: 200,
      intensity: 10,
    });
  });
  it('preserves actual endpoints when sampling peaks', async () => {
    const data = Array.from({ length: 500 }, (_, i) => [i, i === 0 || i === 499 ? 0 : 10]).flat();
    const p = await readMassXml(mzxml(b64(data, false), 500), 'mzxml');
    expect(p?.spectra[0]).toMatchObject({ minMz: 0, maxMz: 499 });
  });
  it('rejects mismatched declared spectrum counts and conflicting unknown CV terms', async () => {
    const data = b64([100, 5, 200, 10], false);
    expect(
      await readMassXml(mzxml(data).replace('<msRun>', '<msRun scanCount=\"2\">'), 'mzxml'),
    ).toBeUndefined();
    const arr = (term: string) =>
      `<binaryDataArray><cvParam accession=\"${term}\"/><cvParam accession=\"MS:1000521\"/><cvParam accession=\"MS:1000576\"/><cvParam accession=\"MS:9999999\" name=\"benign\"/><binary>${b64([1, 2], true)}</binary></binaryDataArray>`;
    const xml = `<mzML><spectrumList count=\"1\"><spectrum defaultArrayLength=\"2\">${arr('MS:1000514')}${arr('MS:1000515')}</spectrum></spectrumList></mzML>`;
    expect(await readMassXml(xml, 'mzml')).toBeUndefined();
  });
  it('rejects mismatched counts, unknown encodings and entities', async () => {
    expect(await readMassXml(mzxml(b64([100, 5], false)), 'mzxml')).toBeUndefined();
    expect(
      await readMassXml(
        mzxml(b64([100, 5, 200, 10], false)).replace(
          'compressionType="none"',
          'compressionType="zlib"',
        ),
        'mzxml',
      ),
    ).toBeUndefined();
    expect(
      await readMassXml(
        '<!DOCTYPE mzXML [<!ENTITY x SYSTEM "file:///etc/passwd">]><mzXML>&x;</mzXML>',
        'mzxml',
      ),
    ).toBeUndefined();
  });
});

it('scales small intensities by their actual maximum', async () => {
  const { spectrumPlot } = await import('./mass-xml');
  expect(
    spectrumPlot(
      [
        { mz: 1, intensity: 1e-300 },
        { mz: 2, intensity: 2e-300 },
      ],
      1,
      2,
    ).map((p) => p.y),
  ).toEqual([50, 10]);
  expect(spectrumPlot([{ mz: 1, intensity: 0 }], 1, 1)[0]?.y).toBe(90);
});
