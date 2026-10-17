// Query keys. Everything a workspace owns sits under ['ws', id], so switching
// workspace can drop it in one call, and everything a card owns sits under
// ['ws', id, 'card', cardId].
export const keys = {
  me: ['me'] as const,
  workspaces: ['workspaces'] as const,
  tokens: ['tokens'] as const,
  ws: (ws: string) => ['ws', ws] as const,
  workspace: (ws: string) => ['ws', ws, 'workspace'] as const,
  cards: (ws: string) => ['ws', ws, 'cards'] as const,
  card: (ws: string, id: string) => ['ws', ws, 'card', id, 'detail'] as const,
  recordsAll: (ws: string, id: string) => ['ws', ws, 'card', id, 'records'] as const,
  records: (ws: string, id: string, column: string) =>
    ['ws', ws, 'card', id, 'records', column] as const,
  gates: (ws: string, id: string) => ['ws', ws, 'card', id, 'gates'] as const,
  comments: (ws: string, id: string) => ['ws', ws, 'card', id, 'comments'] as const,
  events: (ws: string, id: string) => ['ws', ws, 'card', id, 'events'] as const,
  runs: (ws: string, id: string) => ['ws', ws, 'card', id, 'runs'] as const,
  runMessages: (ws: string, run: string, attempt: number) =>
    ['ws', ws, 'run', run, 'messages', attempt] as const,
  inboxAll: (ws: string) => ['ws', ws, 'inbox'] as const,
  inbox: (ws: string, archived: boolean) =>
    ['ws', ws, 'inbox', archived ? 'archived' : 'open'] as const,
  runtimes: (ws: string) => ['ws', ws, 'runtimes'] as const,
  researches: (ws: string) => ['ws', ws, 'researches'] as const,
  researchDetails: (ws: string) => ['ws', ws, 'research'] as const,
  research: (ws: string, id: string) => ['ws', ws, 'research', id] as const,
  agents: (ws: string) => ['ws', ws, 'agents'] as const,
  agent: (ws: string, id: string) => ['ws', ws, 'agent', id] as const,
  members: (ws: string) => ['ws', ws, 'members'] as const,
  invitations: (ws: string) => ['ws', ws, 'invitations'] as const,
  packs: (ws: string) => ['ws', ws, 'packs'] as const,
};
