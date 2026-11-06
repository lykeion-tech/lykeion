import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
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

it('renders NumPy arrays through the shared viewer', async () => {
  const header = "{'descr': '|u1', 'fortran_order': False, 'shape': (2,), }\n";
  const headerBytes = new TextEncoder().encode(header);
  const bytes = new Uint8Array(10 + headerBytes.length + 2);
  bytes.set([0x93, 0x4e, 0x55, 0x4d, 0x50, 0x59, 1, 0, headerBytes.length, 0]);
  bytes.set(headerBytes, 10);
  bytes.set([7, 9], 10 + headerBytes.length);
  const app = createTreeApp({
    render: () => h(ArtifactViewer, { path: 'values.npy', blob: new Blob([bytes]) }),
  });
  const root = node('root');
  app.mount(root);
  await new Promise((r) => setTimeout(r, 30));
  expect(textOf(root)).toContain('Data type');
  expect(textOf(root)).toContain('7');
  expect(all(root).some((n) => n.tag === 'svg')).toBe(true);
  app.unmount();
});

it('does not display an older NumPy file after switching files', async () => {
  const makeFile = (value: number) => {
    const header = new TextEncoder().encode(
      "{'descr': '|u1', 'fortran_order': False, 'shape': (1,), }\n",
    );
    const bytes = new Uint8Array(10 + header.length + 1);
    bytes.set([0x93, 0x4e, 0x55, 0x4d, 0x50, 0x59, 1, 0, header.length, 0]);
    bytes.set(header, 10);
    bytes[10 + header.length] = value;
    return new Blob([bytes]);
  };
  const older = makeFile(17),
    newer = makeFile(42);
  const slice = older.slice.bind(older);
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  vi.spyOn(older, 'slice').mockImplementation((...args) => {
    const part = slice(...args);
    const arrayBuffer = part.arrayBuffer.bind(part);
    vi.spyOn(part, 'arrayBuffer').mockImplementation(async () => {
      await gate;
      return arrayBuffer();
    });
    return part;
  });
  const selected = ref(older);
  const app = createTreeApp({
    render: () => h(ArtifactViewer, { path: 'values.npy', blob: selected.value }),
  });
  const root = node('root');
  app.mount(root);
  selected.value = newer;
  await nextTick();
  await new Promise((r) => setTimeout(r, 30));
  release();
  await new Promise((r) => setTimeout(r, 30));
  expect(textOf(root)).toContain('42');
  expect(textOf(root)).not.toContain('17');
  app.unmount();
});

it('opens Matrix Market files in the shared matrix preview', async () => {
  const blob = new Blob(['%%MatrixMarket matrix coordinate real general\n2 2 1\n1 2 3\n']);
  const app = createTreeApp({ render: () => h(ArtifactViewer, { path: 'sample.mtx', blob }) });
  const root = node('root');
  app.mount(root);
  await new Promise((resolve) => setTimeout(resolve, 30));
  expect(textOf(root)).toContain('2 rows × 2 columns');
  expect(textOf(root)).toContain('3');
  app.unmount();
});
