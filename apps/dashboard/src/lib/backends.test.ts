import { expect, it } from 'vitest';
import { createSSRApp } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { backends } from './backends';
import BackendDetailsDialog from '../components/BackendDetailsDialog.vue';

it('offers exactly the supported backends', () => {
  expect(backends).toEqual([
    { id: 'claude', label: 'Claude Code' },
    { id: 'codex', label: 'Codex' },
  ]);
});
it('renders backend details without the removed private profile', async () => {
  for (const harness of ['claude', 'codex', 'cursor', 'antigravity']) {
    const html = await renderToString(createSSRApp(BackendDetailsDialog, { harness }));
    expect(html).not.toContain('Private only');
  }
});
