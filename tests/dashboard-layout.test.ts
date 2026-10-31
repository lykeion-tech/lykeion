import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

const src = 'apps/dashboard/src';

describe('the dashboard source layout', () => {
  it('has a features folder and a ui folder', () => {
    expect(existsSync(join(src, 'features'))).toBe(true);
    expect(existsSync(join(src, 'ui'))).toBe(true);
  });

  it('ui holds the shared widgets', () => {
    const moved = [
      ...[
        'Icon.vue', 'Modal.vue', 'SelectPicker.vue', 'EmojiIcon.vue', 'AvatarDisplay.vue',
        'AvatarPicker.vue', 'CommandPalette.vue', 'SidebarFooter.vue', 'RecordedMarkdown.vue',
        'SurfacePane.vue', 'AssignPicker.vue', 'composer-menu.css',
      ].map((f) => [`components/${f}`, `ui/${f}`]),
      ...['CompactSelect.vue', 'TimeZonePicker.vue'].map((f) => [`components/settings/${f}`, `ui/${f}`]),
      ...['directory.css', 'directory-table.css', 'resources.css'].map((f) => [`views/${f}`, `ui/${f}`]),
    ];
    for (const [from, to] of moved) {
      expect(existsSync(join(src, to)), to).toBe(true);
      expect(existsSync(join(src, from)), from).toBe(false);
    }
  });
});
