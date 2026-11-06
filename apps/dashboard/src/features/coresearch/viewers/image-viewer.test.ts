import { expect, it } from 'vitest';
import { h, nextTick } from 'vue';
import { all, createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import ImageViewer from './ImageViewer.vue';

it('offers download when the browser cannot decode an image', async () => {
  const root = node('root');
  const app = createTreeApp({
    render: () => h(ImageViewer, { path: 'image.ico', src: 'blob:test' }),
  });
  app.mount(root);
  const img = all(root).find((n) => n.tag === 'img')!;
  expect(img.props.onError).toBeTypeOf('function');
  (img.props.onError as () => void)();
  await nextTick();
  expect(textOf(root)).toContain('Image preview unavailable');
  expect(
    all(root).some(
      (n) => n.tag === 'a' && n.props.href === 'blob:test' && n.props.download === 'image.ico',
    ),
  ).toBe(true);
  app.unmount();
});
