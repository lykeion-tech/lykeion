import { expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import PdfViewer from '../cowrite/PdfViewer.vue';
const state = vi.hoisted(() => ({ getPage: vi.fn(), destroy: vi.fn() }));
vi.mock('../cowrite/lib/pdf-render', () => ({ loadPdfjs: async () => ({ getDocument: () => ({ promise: Promise.resolve({ numPages: 301, getPage: state.getPage }), destroy: state.destroy }) }) }));
it('rejects large PDF previews before reading page metadata and disposes the task', async () => {
  const app = createTreeApp({ render: () => h(PdfViewer, { src: 'blob:test', path: 'big.pdf', maxPages: 300 }) });
  const root = node('root'); app.mount(root);
  await new Promise((r) => setTimeout(r, 20)); await nextTick();
  expect(textOf(root)).toContain('more than 300 pages');
  expect(state.getPage).not.toHaveBeenCalled();
  expect(state.destroy).toHaveBeenCalled();
  app.unmount();
});
