import { expect, it } from 'vitest';
import { h } from 'vue';
import { all, createTreeApp, node } from '../../../../../../tests/daemon-tree';
import SpectrumViewer from './SpectrumViewer.vue';

it('draws MGF peaks as separate sticks rather than joining them', () => {
  const app = createTreeApp({
    render: () =>
      h(SpectrumViewer, {
        text: 'BEGIN IONS\n1 3\n2 8\nEND IONS',
        extension: 'mgf',
        partial: false,
      }),
  });
  const root = node('root');
  app.mount(root);
  expect(all(root).some((item) => item.tag === 'polyline')).toBe(false);
  expect(all(root).some((item) => item.tag === 'path' && String(item.props.d).includes('V'))).toBe(
    true,
  );
  app.unmount();
});
