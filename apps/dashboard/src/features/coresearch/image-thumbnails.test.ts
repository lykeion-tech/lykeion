import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import { workspaceId } from '../../api/session';
import StudyFileThumbnail from '../studies/StudyFileThumbnail.vue';
import ExperimentFileCard from './ExperimentFileCard.vue';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it.each(['bmp', 'ico'])(
  'uses the correct browser image MIME and falls back when %s cannot be decoded',
  async (ext) => {
    workspaceId.value = 'w';
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(new Blob(['image']))),
    );
    const create = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test');
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
    const mime = ext === 'bmp' ? 'image/bmp' : 'image/x-icon';
    const card = {
      id: 'f',
      path: `image.${ext}`,
      head: { n: 1, size: 5, sha256: 'hash', created_at: '2026-01-01' },
    };
    for (const view of [
      h(StudyFileThumbnail, { name: card.path, size: 5, url: '/image' }),
      h(ExperimentFileCard, { projectId: 'p', file: card, list: false }),
    ]) {
      const root = node('root');
      const app = createTreeApp({ render: () => view });
      app.mount(root);
      await vi.waitFor(() => expect(all(root).some((n) => n.tag === 'img')).toBe(true));
      expect((create.mock.calls.at(-1)?.[0] as Blob).type).toBe(mime);
      const img = all(root).find((n) => n.tag === 'img')!;
      (img.props.onError as () => void)();
      await nextTick();
      expect(textOf(root).toLowerCase()).toContain(ext);
      expect(all(root).some((n) => n.tag === 'img')).toBe(false);
      app.unmount();
    }
  },
);
