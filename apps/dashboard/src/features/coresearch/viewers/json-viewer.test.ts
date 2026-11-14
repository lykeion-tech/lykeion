import { expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import JsonNode from './JsonNode.vue';
import JsonViewer from './JsonViewer.vue';
import { jsonPathsMatching } from '../json-tree';
import { all, createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';

vi.mock('../CodeBlock.vue', () => ({
  default: {
    props: ['code'],
    render(this: { code: string }) {
      return h('pre', this.code);
    },
  },
}));

it('renders native expandable lists, toggles nodes and preserves the raw view', async () => {
  const root = node('root');
  const app = createTreeApp({
    render: () => h(JsonViewer, { text: '{"outer":{"leaf":"needle"}}' }),
  });
  app.mount(root);
  await nextTick();
  expect(all(root).some((n) => n.props.role === 'tree' || n.props.role === 'treeitem')).toBe(false);
  const toggle = () =>
    all(root).find((n) => n.tag === 'button' && n.props['aria-label'] === 'outer {1}');
  expect(toggle()).toBeTruthy();
  expect(toggle()!.props['aria-expanded']).toBe(false);
  (toggle()!.props.onClick as (event: unknown) => void)({ stopPropagation() {} });
  await nextTick();
  expect(textOf(root)).toContain('needle');
  expect(toggle()!.props['aria-expanded']).toBe(true);
  (toggle()!.props.onClick as (event: unknown) => void)({ stopPropagation() {} });
  await nextTick();
  expect(textOf(root)).not.toContain('needle');
  const raw = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Raw')!;
  (raw.props.onClick as () => void)();
  await nextTick();
  expect(all(root).some((n) => n.tag === 'pre' && textOf(n).includes('needle'))).toBe(true);
  app.unmount();
});

it('opens matching branches and allows collapsing a search result', async () => {
  const value = { outer: { leaf: 'needle' } };
  const open = ref(new Set<string>());
  const suppressed = ref(new Set<string>());
  const matches = jsonPathsMatching(value, 'needle');
  const root = node('root');
  const app = createTreeApp({
    render: () =>
      h('ul', [
        h(JsonNode, {
          name: '$',
          value,
          path: '$',
          open: open.value,
          suppressed: suppressed.value,
          matches,
          onToggle: (path: string) => {
            suppressed.value = new Set([path]);
          },
        }),
      ]),
  });
  app.mount(root);
  await nextTick();
  expect(textOf(root)).toContain('needle');
  const outer = all(root).find((n) => n.tag === 'button' && n.props['aria-label'] === 'outer {1}')!;
  (outer.props.onClick as (event: unknown) => void)({ stopPropagation() {} });
  await nextTick();
  expect(textOf(root)).not.toContain('needle');
  app.unmount();
});
