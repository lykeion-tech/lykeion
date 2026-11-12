import { expect, it } from 'vitest';
import { updateErrorMessage } from '../apps/dashboard/src/features/workspace/update-messages';

it('gives fixed guidance and never displays an unknown helper string', () => {
  expect(updateErrorMessage('network')).toContain('connection');
  expect(updateErrorMessage('invalid_metadata')).toContain('verified');
  expect(updateErrorMessage('cache_full')).toContain('support');
  expect(updateErrorMessage('https://example.test/?token=secret')).toBe(updateErrorMessage('generic'));
  expect(updateErrorMessage('__proto__')).toBe(updateErrorMessage('generic'));
});
