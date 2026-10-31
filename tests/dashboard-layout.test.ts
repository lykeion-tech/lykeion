import { existsSync, readFileSync, readdirSync } from 'node:fs';
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

  it('agents holds its files', () => {
    const moved = [
      ['views/AgentsView.vue', 'features/agents/AgentsView.vue'],
      ['views/AgentDetail.vue', 'features/agents/AgentDetail.vue'],
      ['views/agent-detail.css', 'features/agents/agent-detail.css'],
      ['components/AgentConnectors.vue', 'features/agents/AgentConnectors.vue'],
      ['components/AgentConnectorDialog.vue', 'features/agents/AgentConnectorDialog.vue'],
      ['components/BackendDetailsDialog.vue', 'features/agents/BackendDetailsDialog.vue'],
      ['lib/agent-connectors.ts', 'features/agents/agent-connectors.ts'],
    ];
    for (const [from, to] of moved) {
      expect(existsSync(join(src, to)), to).toBe(true);
      expect(existsSync(join(src, from)), from).toBe(false);
    }
  });

  it('runs holds its files', () => {
    const moved = [
      ['components/AgentRunCard.vue', 'features/runs/AgentRunCard.vue'],
      ['components/AgentRunDialog.vue', 'features/runs/AgentRunDialog.vue'],
      ['components/ExecutionLog.vue', 'features/runs/ExecutionLog.vue'],
      ['components/HighlightedSource.vue', 'features/runs/HighlightedSource.vue'],
      ['components/RunActivity.vue', 'features/runs/RunActivity.vue'],
      ['components/RunStatus.vue', 'features/runs/RunStatus.vue'],
      ['components/RunStep.vue', 'features/runs/RunStep.vue'],
      ['components/RunStepCode.vue', 'features/runs/RunStepCode.vue'],
      ['components/RunTimeline.vue', 'features/runs/RunTimeline.vue'],
      ['components/RunTurn.vue', 'features/runs/RunTurn.vue'],
      ['lib/run-timeline.ts', 'features/runs/run-timeline.ts'],
      ['lib/run-transcript.ts', 'features/runs/run-transcript.ts'],
      ['lib/tool-step-title.ts', 'features/runs/tool-step-title.ts'],
    ];
    for (const [from, to] of moved) {
      expect(existsSync(join(src, to)), to).toBe(true);
      expect(existsSync(join(src, from)), from).toBe(false);
    }
  });

  it('studies holds its files', () => {
    const moved = [
      ['views/TaskBoard.vue', 'features/studies/StudyBoard.vue'],
      ['views/TaskDetail.vue', 'features/studies/StudyDetail.vue'],
      ['views/CreateTask.vue', 'features/studies/CreateStudy.vue'],
      ['components/ColumnRating.vue', 'features/studies/ColumnRating.vue'],
      ['components/StudyOutputs.vue', 'features/studies/StudyOutputs.vue'],
      ['components/StudyEnvironment.vue', 'features/studies/StudyEnvironment.vue'],
      ['components/ComposerCalendar.vue', 'features/studies/ComposerCalendar.vue'],
      ['lib/environment.ts', 'features/studies/environment.ts'],
      ['lib/dates.ts', 'features/studies/dates.ts'],
    ];
    for (const [from, to] of moved) {
      expect(existsSync(join(src, to)), to).toBe(true);
      expect(existsSync(join(src, from)), from).toBe(false);
    }
  });

  it('coresearch holds its files', () => {
    const moved = [
      ['coresearch/l2.css', 'features/coresearch/l2.css'],
      ['coresearch/cowrite/Editor.vue', 'features/coresearch/cowrite/Editor.vue'],
      ['views/CoresearchList.vue', 'features/coresearch/CoresearchList.vue'],
      ['views/CoresearchProject.vue', 'features/coresearch/CoresearchProject.vue'],
      ['components/Aristotle.vue', 'features/coresearch/Aristotle.vue'],
      ['lib/element-width.ts', 'features/coresearch/element-width.ts'],
    ];
    for (const [from, to] of moved) {
      expect(existsSync(join(src, to)), to).toBe(true);
      expect(existsSync(join(src, from)), from).toBe(false);
    }
  });

  it('workspace holds settings and the library', () => {
    const moved = [
      ['components/WorkspaceTabs.vue', 'features/workspace/WorkspaceTabs.vue'],
      ['views/SettingsView.vue', 'features/workspace/SettingsView.vue'],
      ['views/SignInView.vue', 'features/workspace/SignInView.vue'],
      ['views/AcceptInvite.vue', 'features/workspace/AcceptInvite.vue'],
      ['views/OnboardingView.vue', 'features/workspace/OnboardingView.vue'],
      ['views/ColleaguesView.vue', 'features/workspace/ColleaguesView.vue'],
      ['components/settings/AnalyticsPanel.vue', 'features/workspace/settings/AnalyticsPanel.vue'],
      ['components/settings/StoragePanel.vue', 'features/workspace/settings/StoragePanel.vue'],
      ['components/settings/TokenCreationForm.vue', 'features/workspace/settings/TokenCreationForm.vue'],
      ['components/settings/library/SkillPanel.vue', 'features/workspace/settings/library/SkillPanel.vue'],
    ];
    for (const [from, to] of moved) {
      expect(existsSync(join(src, to)), to).toBe(true);
      expect(existsSync(join(src, from)), from).toBe(false);
    }
  });

  it('has no components or views folder', () => {
    expect(existsSync(join(src, 'components'))).toBe(false);
    expect(existsSync(join(src, 'views'))).toBe(false);
  });

  it('dashboard unit tests live with their code', () => {
    const left = readdirSync('tests').filter((f) => f.endsWith('.test.ts')).sort();
    expect(left).toEqual(['dashboard-layout.test.ts', 'desktop-daemon.test.ts', 'mock-layer.test.ts']);
  });

  it('study files carry the study name', () => {
    const renamed = [
      ['features/studies/TaskBoard.vue', 'features/studies/StudyBoard.vue'],
      ['features/studies/TaskDetail.vue', 'features/studies/StudyDetail.vue'],
      ['features/studies/CreateTask.vue', 'features/studies/CreateStudy.vue'],
      ['state/task-composer.ts', 'state/study-composer.ts'],
    ];
    for (const [from, to] of renamed) {
      expect(existsSync(join(src, to)), to).toBe(true);
      expect(existsSync(join(src, from)), from).toBe(false);
    }
  });

  it('keeps the stored keys', () => {
    expect(readFileSync(join(src, 'state/pins.ts'), 'utf8')).toContain("'lykeion.pinned-tasks'");
    expect(readFileSync(join(src, 'state/preferences.ts'), 'utf8')).toContain('taskOpen');
  });
});
