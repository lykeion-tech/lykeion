import { expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import SkillFileViewer from './SkillFileViewer.vue';
import { createTreeApp, node, textOf } from '../../../../../../../tests/daemon-tree';

vi.mock('../../../coresearch/viewers/ArtifactViewer.vue', () => ({
  default: { props: ['blob', 'path'], template: '<div>Rich preview: {{ path }}</div>' },
}));

it('uses the shared preview for non-Markdown skill files', async () => {
  const root = node('root');
  const app = createTreeApp({ render: () => h(SkillFileViewer, { path: 'data/results.csv', content: 'x', blob: new Blob(['x']) }) });
  app.mount(root);
  await nextTick();
  expect(textOf(root)).toContain('Rich preview: data/results.csv');
  app.unmount();
});
