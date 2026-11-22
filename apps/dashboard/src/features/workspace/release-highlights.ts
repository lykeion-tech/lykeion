import { version } from '../../../../../package.json';

type Highlight = {
  title: string;
  description: string;
  icon: string;
  color: 'blue' | 'green' | 'violet';
  details: [string, string, string];
};

// Each release includes its own reviewed copy and feature illustration.
const releases: Record<string, readonly Highlight[]> = {
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
