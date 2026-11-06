import { expect, it } from 'vitest';
import { h, nextTick } from 'vue';
import { all, createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import JsonLinesViewer from './JsonLinesViewer.vue';

it('searches the sample, sorts numeric columns, and plots numeric pairs', async () => {
  const root = node('root');
  const app = createTreeApp({
    render: () =>
      h(JsonLinesViewer, {
        text: '{"name":"beta","x":10,"y":3}\n{"name":"alpha","x":2,"y":4}\n',
        partial: false,
      }),
  });
  app.mount(root);
  const search = all(root).find((n) => n.props['aria-label'] === 'Search records')!;
  expect(search).toBeTruthy();
  (search.props.onInput as (event: unknown) => void)({ target: { value: 'alpha' } });
  await nextTick();
  expect(textOf(root)).not.toContain('beta');
  (search.props.onInput as (event: unknown) => void)({ target: { value: '' } });
  await nextTick();
  const sort = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'x')!;
  (sort.props.onClick as () => void)();
  await nextTick();
  const rows = all(root).filter((n) => n.tag === 'tr');
  expect(textOf(rows[1])).toContain('alpha');
  expect(all(root).some((n) => n.props['aria-label'] === 'Scatter plot')).toBe(true);
  app.unmount();
});

it('does not graph numeric strings and sorts a mixed column consistently as text', async () => {
  const root = node('root');
  const app = createTreeApp({
    render: () =>
      h(JsonLinesViewer, {
        text: '{"x":2,"y":"3"}\n{"x":"10","y":"4"}\n{"x":1,"y":"5"}\n',
        partial: false,
      }),
  });
  app.mount(root);
  expect(all(root).some((n) => n.props['aria-label'] === 'Scatter plot')).toBe(false);
  const sort = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'x')!;
  (sort.props.onClick as () => void)();
  await nextTick();
  const rows = all(root).filter((n) => n.tag === 'tr');
  expect(rows.slice(1).map(textOf)).toEqual(['15', '104', '23']);
  app.unmount();
});

it('reports how many sampled records are plotted', () => {
  const root = node('root');
  const app = createTreeApp({
    render: () => h(JsonLinesViewer, { text: '{"x":1,"y":2}\n{"x":3}\n', partial: false }),
  });
  app.mount(root);
  expect(textOf(root)).toContain('1 of 2 records plotted');
  app.unmount();
});
