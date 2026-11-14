import { expect, it } from 'vitest';
import { orderSlides } from './office-order';
it('follows the slide relationships in presentation order and rejects external targets', () => {
  expect(
    orderSlides(
      ['r2', 'r1'],
      [
        { id: 'r1', target: 'slides/slide1.xml' },
        { id: 'r2', target: 'slides/slide2.xml' },
      ],
    ),
  ).toEqual(['ppt/slides/slide2.xml', 'ppt/slides/slide1.xml']);
  expect(
    orderSlides(['r1'], [{ id: 'r1', target: 'https://evil.example/slide1.xml' }]),
  ).toBeUndefined();
  expect(orderSlides(['r1'], [{ id: 'r1', target: '../slide1.xml' }])).toBeUndefined();
  expect(
    orderSlides(['r1'], [{ id: 'r1', target: 'slides/slide1.xml', external: true }]),
  ).toBeUndefined();
  expect(
    orderSlides(
      ['r1'],
      [
        { id: 'r1', target: 'slides/slide1.xml' },
        { id: 'r1', target: 'slides/slide2.xml' },
      ],
    ),
  ).toBeUndefined();
});
it('rejects duplicated slide references and targets', () => {
  const r1 = { id: 'r1', target: 'slides/slide1.xml' };
  expect(orderSlides(['r1', 'r1'], [r1])).toBeUndefined();
  expect(
    orderSlides(['r1', 'r2'], [r1, { id: 'r2', target: 'slides/slide1.xml' }]),
  ).toBeUndefined();
});
