import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { all, createTreeApp, node, textOf } from '../../../../../../tests/daemon-tree';
import ArtifactViewer from './ArtifactViewer.vue';

it('does not expose medical image metadata in a raw text fallback', async () => {
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:medical');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const app = createTreeApp({
    render: () =>
      h(ArtifactViewer, {
        path: 'scan.dcm',
        blob: new Blob(['PatientName: Private Person']),
      }),
  });
  const root = node('root');
  app.mount(root);
  await new Promise((r) => setTimeout(r, 20));
  expect(textOf(root)).not.toContain('Private Person');
  expect(textOf(root)).toContain('No preview');
  app.unmount();
});

vi.mock('../cowrite/PdfViewer.vue', () => ({
  default: { props: ['src', 'path'], template: '<div>PDF preview</div>' },
}));
afterEach(() => vi.restoreAllMocks());
it('does not show the file MIME type above text previews', async () => {
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:text');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const app = createTreeApp({
    render: () =>
      h(ArtifactViewer, {
        path: 'result.txt',
        blob: new Blob(['result'], { type: 'text/plain' }),
      }),
  });
  const root = node('root');
  app.mount(root);
  await new Promise((r) => setTimeout(r, 20));
  expect(textOf(root)).toContain('result');
  expect(textOf(root)).not.toContain('text/plain');
  app.unmount();
});
it('renders a PDF viewer instead of treating a PDF as text', async () => {
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:pdf');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const app = createTreeApp({
    render: () => h(ArtifactViewer, { path: 'report.pdf', blob: new Blob(['%PDF-1.7']) }),
  });
  const root = node('root');
  app.mount(root);
  await nextTick();
  await nextTick();
  expect(textOf(root)).toContain('PDF preview');
  expect(textOf(root)).not.toContain('%PDF');
  app.unmount();
  expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:pdf');
});
it('shows notebook source and untrusted output without interpreting HTML', async () => {
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:notebook');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const blob = new Blob([
    JSON.stringify({
      cells: [{ cell_type: 'code', source: ['print(2)'], outputs: [{ text: ['2\n'] }] }],
    }),
  ]);
  const app = createTreeApp({ render: () => h(ArtifactViewer, { path: 'demo.ipynb', blob }) });
  const root = node('root');
  app.mount(root);
  await new Promise((r) => setTimeout(r, 20));
  await nextTick();
  expect(all(root).some((n) => n.props['aria-label'] === 'Jupyter notebook')).toBe(true);
  expect(textOf(root)).toContain('2');
  app.unmount();
});
it('offers download for a PDF even when the viewer cannot render it', async () => {
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:pdf-download');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const app = createTreeApp({
    render: () => h(ArtifactViewer, { path: 'report.pdf', blob: new Blob(['%PDF']) }),
  });
  const root = node('root');
  app.mount(root);
  await nextTick();
  await nextTick();
  const link = all(root).find((n) => n.tag === 'a' && n.props.download === 'report.pdf');
  expect(link?.props.href).toBe('blob:pdf-download');
  app.unmount();
});
it('renders a FASTA sequence preview and a VCF table', async () => {
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:scientific');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const fasta = createTreeApp({
    render: () => h(ArtifactViewer, { path: 'sample.fa', blob: new Blob(['>sample\nACGT\n']) }),
  });
  const root = node('root');
  fasta.mount(root);
  await new Promise((r) => setTimeout(r, 20));
  expect(textOf(root)).toContain('4 residues');
  expect(textOf(root)).not.toContain('GC');
  fasta.unmount();

  const vcf = createTreeApp({
    render: () =>
      h(ArtifactViewer, {
        path: 'variants.vcf',
        blob: new Blob(['##fileformat=VCFv4.2\n#CHROM\tPOS\n1\t3\n']),
      }),
  });
  const table = node('root');
  vcf.mount(table);
  await new Promise((r) => setTimeout(r, 20));
  expect(textOf(table)).toContain('Header metadata');
  expect(textOf(table)).toContain('CHROM');
  expect(textOf(table)).toContain('VCFv4.2');
  vcf.unmount();
});
