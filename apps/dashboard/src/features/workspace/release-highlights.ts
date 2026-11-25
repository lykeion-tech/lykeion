import { version } from '../../../../../package.json';
import { readText, writeText } from '../../storage';

type Highlight = {
  title: string;
  description: string;
  icon: string;
  color: 'blue' | 'green' | 'violet';
  details: [string, string, string];
};

// Each release includes its own reviewed copy and feature illustration.
const releases: Record<string, readonly Highlight[]> = {
  '0.1.13': [
    {
      title: 'Turn a Coresearch chat into a study',
      description:
        'After you’ve worked through a research question, Aristotle can offer a study draft. You can ask for one yourself, then review the brief, stage, assignee and project before starting.',
      icon: 'chat',
      color: 'blue',
      details: ['Coresearch discussion', 'Study draft', 'Review and start'],
    },
    {
      title: 'Create and organize To-dos',
      description:
        'Choose To-do in New study to save work for later. Use the card’s menu to add it to a project, or open the card when you’re ready to start.',
      icon: 'check',
      color: 'green',
      details: ['New study', 'To-do', 'Add to project'],
    },
    {
      title: 'Filter chats in a project',
      description:
        'Open a project’s Coresearches tab to find Experiment or Cowrite chats. The Filter menu also lets you view archived chats.',
      icon: 'folder',
      color: 'violet',
      details: ['Project Coresearches', 'Experiment and Cowrite', 'Active or archived'],
    },
    {
      title: 'Find public sources with web search',
      description:
        'Claude Code and Codex can use built-in web search to discover public sources. When agents cite a paper or dataset in a study, Lykeion records it through Sources.',
      icon: 'search',
      color: 'blue',
      details: ['Your agent', 'Public web search', 'Recorded study evidence'],
    },
  ],
  '0.1.12': [
    {
      title: 'Review a study without a TypeSafe key',
      description:
        'Lykeion records the server checks before asking you to approve. In Decisions, see what ran and when. If the checks are more than 24 hours old, Lykeion refreshes them in the background.',
      icon: 'check',
      color: 'blue',
      details: ['Stage evidence', 'Server checks', 'Owner decision'],
    },
    {
      title: 'Replay with saved inputs',
      description:
        'Validation restores the helper files and inputs saved under outputs/ by the run that produced the cited script. The replay recomputes its recorded measurements.',
      icon: 'play',
      color: 'green',
      details: ['Saved inputs', 'Validation replay', 'Recomputed results'],
    },
    {
      title: 'Find archived chats in your sidebar',
      description:
        'Expand Archived in Coresearch to reopen an Experiment or Cowrite chat. You can unarchive it from its menu and keep its project link.',
      icon: 'archive',
      color: 'violet',
      details: ['Coresearch', 'Archived chats', 'Open or restore'],
    },
  ],
  '0.1.11': [
    {
      title: 'Lykeion is coming to your phone',
      description:
        'Plan with Aristotle, start studies and follow their runs from your phone. We’ll tell you here when you can install the app.',
      icon: 'chat',
      color: 'blue',
      details: ['Plan', 'Lykeion mobile', 'Follow runs'],
    },
  ],
  '0.1.10': [
    {
      title: 'Share a chat with a project',
      description:
        'Add an Experiment or Cowrite chat to a project from its menu. Anyone who can view the project can read the conversation and download its saved artifacts from the project’s Coresearches tab.',
      icon: 'folder',
      color: 'blue',
      details: ['Your chat', 'Add to project', 'Coresearches tab'],
    },
    {
      title: 'Start with Aristotle',
      description:
        'Lykeion now opens in Coresearch. Machines, Slack and GitHub share one place: Settings → Connections.',
      icon: 'plug',
      color: 'violet',
      details: ['Coresearch', 'Settings', 'Connections'],
    },
  ],
  '0.1.9': [
    {
      title: 'Ask Aristotle from Slack',
      description:
        'Message Aristotle directly or @mention it in a channel. It answers in the thread, runs on your own machine, and cites the same evidence you’d see in Lykeion.',
      icon: 'chat',
      color: 'blue',
      details: ['Slack message', 'Aristotle', 'Answer in thread'],
    },
    {
      title: 'See the code behind a study',
      description:
        'Connect GitHub and pull requests that name a study, like LYK-12, appear on it with their checks and merge status. Lykeion only reads GitHub, and merging never moves a study.',
      icon: 'code',
      color: 'green',
      details: ['Pull request', 'LYK-12', 'CI · Merge status'],
    },
    {
      title: 'Research profiles',
      description:
        'Share your research profile with a workspace. When you plan a study, Aristotle can suggest colleagues whose background fits the work.',
      icon: 'user',
      color: 'violet',
      details: ['Your profile', 'Research directory', 'Colleague suggestions'],
    },
  ],
  '0.1.8': [
    {
      title: 'Inspect the replay behind a study',
      description:
        'Open a Study Report to inspect the automatic replay. Validation uses the original inputs and preserves where each piece of evidence came from.',
      icon: 'play',
      color: 'blue',
      details: ['Original inputs', 'Automatic replay', 'Validation'],
    },
    {
      title: 'Keep evidence files distinct',
      description:
        'Outputs with the same filename stay separate, so you can trace each file back to the run that produced it.',
      icon: 'file',
      color: 'green',
      details: ['First run', 'Same filename', 'Second run'],
    },
    {
      title: 'See how agents are set up',
      description:
        'Read the full default reporting instructions in Agent Settings and see team assignments in an agent’s work history.',
      icon: 'team',
      color: 'violet',
      details: ['Agent Settings', 'Reporting instructions', 'Team history'],
    },
  ],
};

export const releaseHighlights = releases[version];

export function isNewerRelease(current: string, previous: string): boolean {
  const stable = /^\d+\.\d+\.\d+$/;
  if (!stable.test(current) || !stable.test(previous)) return false;
  const prior = previous.split('.').map(Number);
  const difference = current
    .split('.')
    .map((part, index) => Number(part) - prior[index])
    .find((part) => part !== 0);
  return (difference ?? 0) > 0;
}

const seenKey = (account: string) => `lykeion.releaseHighlights:${account}`;

// Shown once per account and device: when this version has cards and the
// account last dismissed an older version, or none. Unreadable storage shows
// nothing rather than showing on every visit.
export function shouldShowRelease(account: string): boolean {
  const previous = readText(seenKey(account));
  return (
    Boolean(releaseHighlights?.length) &&
    (previous === null || (previous !== undefined && isNewerRelease(version, previous)))
  );
}

// Records this version as seen: on dismissal, and when an account finishes
// onboarding, so new accounts start without a "What's new" for a release
// they never used the previous version of.
export function markReleaseSeen(account: string) {
  writeText(seenKey(account), version);
}
