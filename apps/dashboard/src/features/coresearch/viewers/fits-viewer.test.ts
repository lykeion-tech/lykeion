import { expect, it } from 'vitest';
import { h, nextTick } from 'vue';
import { all, createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import FitsViewer from './FitsViewer.vue';
function hdu(cards: string[], data: number[]) {
  const header = [...cards, 'END']
    .map((s) => s.padEnd(80, ' '))
    .join('')
    .padEnd(2880, ' ');
  const body = new Uint8Array(data.length ? 2880 : 0);
  body.set(data);
  return new Blob([header, body]);
}
it('opens the first image extension and lets users inspect unsupported HDUs', async () => {
  const primary = hdu(
    [
      'SIMPLE  =                    T',
      'BITPIX  =                    8',
      'NAXIS   =                    0',
    ],
    [],
  );
  const image = hdu(
    [
      "XTENSION= 'IMAGE   '",
      'BITPIX  =                    8',
      'NAXIS   =                    1',
      'NAXIS1  =                    1',
      'PCOUNT  =                    0',
      'GCOUNT  =                    1',
    ],
    [42],
  );
  const app = createTreeApp({ render: () => h(FitsViewer, { blob: new Blob([primary, image]) }) });
  const root = node('root');
  app.mount(root);
  await new Promise((r) => setTimeout(r, 40));
  expect(textOf(root)).toContain('42');
  const select = all(root).find((n) => n.tag === 'select');
  expect(select).toBeTruthy();
  (select!.props.onChange as (event: unknown) => void)({ target: { value: '0' } });
  await nextTick();
  expect(textOf(root)).toContain('unsupported HDU');
  app.unmount();
});
