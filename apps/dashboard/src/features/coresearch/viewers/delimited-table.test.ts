import { expect, it } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { all, createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import DelimitedTable from './DelimitedTable.vue';
it('sorts sampled rows with accessible direction and resets for a new file', async () => {
  const table = ref({ header: ['value'], rows: [['10'], ['2']], partial: true });
  const app = createTreeApp({ render: () => h(DelimitedTable, { table: table.value }) });
  const root = node('root'); app.mount(root);
  const header = () => all(root).find(n => n.tag === 'th')!;
  const button = () => all(root).find(n => n.tag === 'button')!;
  expect(textOf(root)).toContain('First part only');
  (button().props.onClick as () => void)(); await nextTick();
  expect(header().props['aria-sort']).toBe('ascending');
  expect(all(root).filter(n => n.tag === 'td').map(textOf)).toEqual(['2', '10']);
  (button().props.onClick as () => void)(); await nextTick();
  expect(header().props['aria-sort']).toBe('descending');
  table.value = { header: ['other'], rows: [['z'], ['a']], partial: false }; await nextTick();
  expect(header().props['aria-sort']).toBe('none');
  expect(all(root).filter(n => n.tag === 'td').map(textOf)).toEqual(['z', 'a']);
  app.unmount();
});
it('limits rendered rows and says sorting only covers the shown sample', () => {
  const table = { header: ['x'], rows: Array.from({ length: 1001 }, (_, i) => [String(i)]), partial: false };
  const app = createTreeApp({ render: () => h(DelimitedTable, { table }) }); const root = node('root'); app.mount(root);
  expect(all(root).filter(n => n.tag === 'td')).toHaveLength(1000);
  expect(textOf(root)).toContain('First 1,000 rows only.');
  expect(textOf(root)).toContain('Sorting applies to the displayed rows.'); app.unmount();
});
