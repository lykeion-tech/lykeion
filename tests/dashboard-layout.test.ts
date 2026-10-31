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

  it('projects holds its files', () => {
    const moved = [
      ['views/ResearchesView.vue', 'features/projects/ResearchesView.vue'],
      ['components/ProjectDetail.vue', 'features/projects/ProjectDetail.vue'],
    ];
    for (const [from, to] of moved) {
      expect(existsSync(join(src, to)), to).toBe(true);
      expect(existsSync(join(src, from)), from).toBe(false);
    }
  });

  it('autoresearch holds its files', () => {
    const moved = [
      ['views/AutoresearchView.vue', 'features/autoresearch/AutoresearchView.vue'],
      ['components/AutoresearchRunDialog.vue', 'features/autoresearch/AutoresearchRunDialog.vue'],
      ['lib/schedule.ts', 'features/autoresearch/schedule.ts'],
    ];
    for (const [from, to] of moved) {
      expect(existsSync(join(src, to)), to).toBe(true);
      expect(existsSync(join(src, from)), from).toBe(false);
    }
  });

  it('inbox holds its files', () => {
    const moved = [
      ['views/InboxView.vue', 'features/inbox/InboxView.vue'],
      ['views/UpdatesView.vue', 'features/inbox/UpdatesView.vue'],
      ['components/InboxRail.vue', 'features/inbox/InboxRail.vue'],
    ];
    for (const [from, to] of moved) {
      expect(existsSync(join(src, to)), to).toBe(true);
      expect(existsSync(join(src, from)), from).toBe(false);
    }
  });

  it('machines holds its files', () => {
    const moved = [
      ['views/MachinesView.vue', 'features/machines/MachinesView.vue'],
      ['views/machines.css', 'features/machines/machines.css'],
      ['components/AddComputerDialog.vue', 'features/machines/AddComputerDialog.vue'],
      ['components/MachineCommand.vue', 'features/machines/MachineCommand.vue'],
      ['components/MachinePicker.vue', 'features/machines/MachinePicker.vue'],
      ['components/MachineSharing.vue', 'features/machines/MachineSharing.vue'],
      ['components/DaemonSettings.vue', 'features/machines/DaemonSettings.vue'],
      ['lib/machines.ts', 'features/machines/machines.ts'],
    ];
    for (const [from, to] of moved) {
      expect(existsSync(join(src, to)), to).toBe(true);
      expect(existsSync(join(src, from)), from).toBe(false);
    }
  });

  it('teams holds its files', () => {
    const moved = [
      ['views/TeamsView.vue', 'features/teams/TeamsView.vue'],
      ['views/TeamDetail.vue', 'features/teams/TeamDetail.vue'],
      ['views/TeamForm.vue', 'features/teams/TeamForm.vue'],
      ['components/TeamMemberProfile.vue', 'features/teams/TeamMemberProfile.vue'],
      ['components/TeamSkillsPicker.vue', 'features/teams/TeamSkillsPicker.vue'],
      ['components/TeamName.vue', 'features/teams/TeamName.vue'],
    ];
    for (const [from, to] of moved) {
      expect(existsSync(join(src, to)), to).toBe(true);
      expect(existsSync(join(src, from)), from).toBe(false);
    }
  });
});
