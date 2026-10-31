import { describe, expect, it } from 'vitest';
import { agentDisplayName, agentNameWithoutCli } from './agent-name';

describe('agentDisplayName', () => {
  it('capitalizes names and hides generated team identifiers', () => {
    expect(agentDisplayName('leader · Grounding Team 2f28fc35 claude')).toBe(
      'Leader · Grounding Team claude',
    );
    expect(agentDisplayName('groundingteam-verifier-claude-2f28fc35')).toBe(
      'Groundingteam-verifier-claude',
    );
  });
  it('preserves meaningful numbers and user capitalization', () => {
    expect(agentDisplayName('agent 007')).toBe('Agent 007');
    expect(agentDisplayName('DNA analyst')).toBe('DNA analyst');
  });
});

it('replaces only the matching generated CLI label', () => {
  expect(agentNameWithoutCli('leader · Grounding Team 2f28fc35 claude', 'claude')).toBe(
    'Leader · Grounding Team',
  );
  expect(agentNameWithoutCli('groundingteam-verifier-claude-2f28fc35', 'claude')).toBe(
    'Groundingteam-verifier',
  );
  expect(agentNameWithoutCli('Claude', 'claude')).toBe('Claude');
});
