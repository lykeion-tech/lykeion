import { expect, it } from 'vitest';
import { studyDisplayTitle } from './studies';

it('hides generated suffixes without removing ordinary bracketed titles', () => {
  expect(studyDisplayTitle('Find papers [hd2a73521d221]')).toBe('Find papers');
  expect(studyDisplayTitle('Study [2025]')).toBe('Study [2025]');
  expect(studyDisplayTitle('Study [phase 2]')).toBe('Study [phase 2]');
});
