import { expect, it } from 'vitest';
import { teamDisplayParts } from './team-name';

it('formats generated team names and preserves custom names', () => {
  expect(teamDisplayParts('Grounding Team 6130881c claude')).toEqual({
    label: 'Grounding Team',
    cli: 'claude',
  });
  expect(teamDisplayParts('Hypotheses Team 2a5e3dfe codex')).toEqual({
    label: 'Hypotheses Team',
    cli: 'codex',
  });
  expect(teamDisplayParts('Team 2025')).toEqual({ label: 'Team 2025' });
});
