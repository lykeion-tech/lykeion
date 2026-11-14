import { readFileSync } from 'node:fs';
import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { all, createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import ChemicalViewer from './ChemicalViewer.vue';
vi.mock('3dmol/build/3Dmol.es6-min.js?raw', () => ({
  default: 'export const createViewer=()=>{};',
}));
afterEach(() => vi.unstubAllGlobals());
it('shows separate SDF molecule cards and changes the single sandboxed viewer selection', async () => {
  vi.stubGlobal('window', { addEventListener: vi.fn(), removeEventListener: vi.fn() });
  const input = readFileSync(
    'apps/dashboard/src/features/coresearch/viewers/fixtures/synthetic-two-molecules.sdf',
    'utf8',
  );
  const root = node('root');
  const app = createTreeApp({
    render: () => h(ChemicalViewer, { text: input, extension: 'sdf', partial: false }),
  });
  app.mount(root);
  expect(textOf(root)).toContain('Synthetic ethanol');
  expect(textOf(root)).toContain('Synthetic cyclohexane');
  expect(all(root).filter((n) => n.tag === 'iframe')).toHaveLength(1);
  const second = all(root).find(
    (n) => n.tag === 'button' && textOf(n).includes('Synthetic cyclohexane'),
  );
  expect(second).toBeDefined();
  (second!.props.onClick as () => void)();
  await nextTick();
  const frame = all(root).find((n) => n.tag === 'iframe');
  expect(frame?.props.srcdoc).toContain('Synthetic cyclohexane');
  expect(frame?.props.sandbox).toBe('allow-scripts');
  app.unmount();
});
it('labels a bounded gallery and rejects incomplete input', async () => {
  vi.stubGlobal('window', { addEventListener: vi.fn(), removeEventListener: vi.fn() });
  const input = readFileSync(
    'apps/dashboard/src/features/coresearch/viewers/fixtures/synthetic-two-molecules.sdf',
    'utf8',
  );
  const root = node('root');
  const app = createTreeApp({
    render: () => h(ChemicalViewer, { text: input.repeat(9), extension: 'sdf', partial: false }),
  });
  app.mount(root);
  expect(all(root).filter((n) => n.tag === 'button')).toHaveLength(16);
  expect(textOf(root)).toContain('first 16 of 18');
  app.unmount();
  const other = node('root');
  const incomplete = createTreeApp({
    render: () => h(ChemicalViewer, { text: input, extension: 'sdf', partial: true }),
  });
  incomplete.mount(other);
  expect(textOf(other)).toContain('Preview unavailable');
  expect(all(other).filter((n) => n.tag === 'iframe')).toHaveLength(0);
  incomplete.unmount();
});
