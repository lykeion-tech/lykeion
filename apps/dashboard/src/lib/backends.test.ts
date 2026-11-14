import { expect, it } from 'vitest';
import { backends } from './backends';

it('offers exactly the supported backends', () => {
  expect(backends).toEqual([
    { id: 'claude', label: 'Claude Code' },
    { id: 'codex', label: 'Codex' },
  ]);
});
