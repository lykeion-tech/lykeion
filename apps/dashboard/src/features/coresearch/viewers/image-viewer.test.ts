import { expect, it } from 'vitest';
import { h, nextTick } from 'vue';
import ImageViewer from './ImageViewer.vue';
import source from './ImageViewer.vue?raw';
import { all, createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';

it('provides image sizing and visible keyboard focus outside the Coresearch shell', () => {
  expect(source).toMatch(/<style scoped>/);
  expect(source).toMatch(/\.image-stage\s*\{[^}]*overflow:\s*auto/s);
  expect(source).toMatch(/\.image-el\s*\{[^}]*max-width:\s*100%/s);
  expect(source).toMatch(/\.image-el\.is-zoomed\s*\{[^}]*max-width:\s*none/s);
  expect(source).toMatch(/\.seg-btn:focus-visible\s*\{[^}]*outline:/s);
});

it('fits, zooms and returns to fit with native buttons', async () => {
  const root = node('root');
  const app = createTreeApp({
    render: () => h(ImageViewer, { src: '/image.png', path: 'image.png' }),
  });
  app.mount(root);
  await nextTick();
  const image = all(root).find((n) => n.tag === 'img')!;
  Object.assign(image, { clientWidth: 200 });
  (image.props.onLoad as (e: unknown) => void)({
    currentTarget: { naturalWidth: 400, naturalHeight: 200 },
  });
  await nextTick();
  const button = (label: string) =>
    all(root).find((n) => n.tag === 'button' && textOf(n).trim() === label)!;
  (button('Zoom in').props.onClick as () => void)();
  await nextTick();
  expect(image.props.style).toMatchObject({ width: '300px', height: '150px' });
  (button('Fit').props.onClick as () => void)();
  await nextTick();
  expect(image.props.style).toBeUndefined();
  app.unmount();
});
