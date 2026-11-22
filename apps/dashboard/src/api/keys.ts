// Query keys. Everything a workspace owns sits under ['ws', id], so switching
// workspace can drop it in one call, and everything a card owns sits under
// ['ws', id, 'card', cardId].
export const keys = {
  workflows: ['workflows'] as const,
  workflow: (slug: string) => ['workflows', slug] as const,
  cowriteDoc: (ws: string, project: string, file: string) =>
    ['ws', ws, 'coresearch', project, 'cowrite', 'doc', file] as const,
  cowriteChanges: (ws: string, project: string, file: string) =>
    ['ws', ws, 'coresearch', project, 'cowrite', 'changes', file] as const,
  cowriteComments: (ws: string, project: string, file: string) =>
    ['ws', ws, 'coresearch', project, 'cowrite', 'comments', file] as const,
  cowritePresence: (ws: string, project: string, file: string) =>
    ['ws', ws, 'coresearch', project, 'cowrite', 'presence', file] as const,
  cowriteBuilds: (ws: string, project: string) =>
    ['ws', ws, 'coresearch', project, 'cowrite', 'builds'] as const,
  cowriteVersions: (ws: string, project: string) =>
    ['ws', ws, 'coresearch', project, 'cowrite', 'versions'] as const,
  cowriteCompute: (ws: string, project: string) =>
    ['ws', ws, 'coresearch', project, 'cowrite', 'compute'] as const,
  cowriteTemplates: (ws: string, project: string) =>
    ['ws', ws, 'coresearch', project, 'cowrite', 'templates'] as const,
  cowriteDocSteps: (ws: string, project: string, file: string, after: number) =>
    ['ws', ws, 'coresearch', project, 'cowrite', 'doc-steps', file, after] as const,
  coresearchConversations: (ws: string) => ['ws', ws, 'coresearch-conversations'] as const,
  coresearchProjects: (ws: string) => ['ws', ws, 'coresearch-projects'] as const,
  coresearchProject: (ws: string, id: string) => ['ws', ws, 'coresearch', id, 'detail'] as const,
  coresearchChats: (ws: string, id: string) => ['ws', ws, 'coresearch', id, 'chats'] as const,
  coresearchMessages: (ws: string, id: string, chat: string) =>
    ['ws', ws, 'coresearch', id, 'messages', chat] as const,
  coresearchMembers: (ws: string, id: string) => ['ws', ws, 'coresearch', id, 'members'] as const,
  coresearchSteps: (ws: string, id: string, chat: string, run: string) =>
    ['ws', ws, 'coresearch', id, 'steps', chat, run] as const,
  coresearchCells: (ws: string, id: string, chat: string) =>
    ['ws', ws, 'coresearch', id, 'cells', chat] as const,
  coresearchCellOutputs: (ws: string, id: string, chat: string, cell: string) =>
    ['ws', ws, 'coresearch', id, 'cell-outputs', chat, cell] as const,
  coresearchFiles: (ws: string, id: string) => ['ws', ws, 'coresearch', id, 'files'] as const,
  coresearchVersions: (ws: string, id: string, file: string) =>
    ['ws', ws, 'coresearch', id, 'versions', file] as const,
  coresearchContent: (ws: string, id: string, file: string, n: number) =>
    ['ws', ws, 'coresearch', id, 'content', file, n] as const,
  coresearchRun: (ws: string, id: string, key: string) =>
    ['ws', ws, 'coresearch', id, 'run', key] as const,
  coresearchProducingTurn: (ws: string, id: string, run: string) =>
    ['ws', ws, 'coresearch', id, 'producing-turn', run] as const,
  coresearchRuns: (ws: string, id: string) => ['ws', ws, 'coresearch', id, 'runs'] as const,
  coresearchEnvironment: (ws: string, id: string) =>
    ['ws', ws, 'coresearch', id, 'environment'] as const,
  me: ['me'] as const,
  researchProfiles: ['research-profile'] as const,
  researchProfile: (account: string) => ['research-profile', account] as const,
  workspaces: ['workspaces'] as const,
  tokens: ['tokens'] as const,
  storage: (ws: string) => ['ws', ws, 'storage'] as const,
  analytics: (ws: string) => ['ws', ws, 'analytics'] as const,
  ws: (ws: string) => ['ws', ws] as const,
  workspace: (ws: string) => ['ws', ws, 'workspace'] as const,
  cards: (ws: string) => ['ws', ws, 'cards'] as const,
  card: (ws: string, id: string) => ['ws', ws, 'card', id, 'detail'] as const,
  recordsAll: (ws: string, id: string) => ['ws', ws, 'card', id, 'records'] as const,
  studyReport: (ws: string, cardId: string) => ['ws', ws, 'card', cardId, 'report'] as const,
  records: (ws: string, id: string, column: string) =>
    ['ws', ws, 'card', id, 'records', column] as const,
  comments: (ws: string, id: string) => ['ws', ws, 'card', id, 'comments'] as const,
  events: (ws: string, id: string) => ['ws', ws, 'card', id, 'events'] as const,
  runs: (ws: string, id: string) => ['ws', ws, 'card', id, 'runs'] as const,
  runMessages: (ws: string, run: string, attempt: number) =>
    ['ws', ws, 'run', run, 'messages', attempt] as const,
  decisionsAll: (ws: string) => ['ws', ws, 'decisions'] as const,
  decisions: (ws: string, history: boolean) =>
    ['ws', ws, 'decisions', history ? 'history' : 'open'] as const,
  decisionCount: (ws: string) => ['ws', ws, 'decision-count'] as const,
  runtimes: (ws: string) => ['ws', ws, 'runtimes'] as const,
  researches: (ws: string) => ['ws', ws, 'researches'] as const,
  researchDetails: (ws: string) => ['ws', ws, 'research'] as const,
  research: (ws: string, id: string) => ['ws', ws, 'research', id] as const,
  agents: (ws: string) => ['ws', ws, 'agents'] as const,
  labChart: (ws: string) => ['lab-chart', ws] as const,
  laboratories: ['laboratories'] as const,
  agent: (ws: string, id: string) => ['ws', ws, 'agent', id] as const,
  agentWorkAll: (ws: string) => ['ws', ws, 'agent-work'] as const,
  agentWork: (ws: string, id: string) => ['ws', ws, 'agent-work', id] as const,
  members: (ws: string) => ['ws', ws, 'members'] as const,
  colleagueProfiles: (ws: string) => ['ws', ws, 'research-profile'] as const,
  colleagueProfile: (ws: string, member: string, viewer: string) =>
    ['ws', ws, 'research-profile', member, viewer] as const,
  invitations: (ws: string) => ['ws', ws, 'invitations'] as const,
  teams: (ws: string, domain = '', stage = '') =>
    ['ws', ws, 'packs', 'store', domain, stage] as const,
  team: (ws: string, id: string) => ['ws', ws, 'packs', 'team', id] as const,
  packs: (ws: string) => ['ws', ws, 'packs'] as const,
  skills: (ws: string) => ['ws', ws, 'skills'] as const,
  skill: (ws: string, id: string) => ['ws', ws, 'skill', id] as const,
  skillFile: (ws: string, id: string, revision: number, path: string) =>
    ['ws', ws, 'skill', id, 'files', revision, path] as const,
  github: (ws: string) => ['ws', ws, 'github'] as const,
  studyPullRequests: (ws: string, card: string) =>
    ['ws', ws, 'cards', card, 'pull-requests'] as const,
  slack: (ws: string) => ['ws', ws, 'slack'] as const,
  connectors: (ws: string) => ['ws', ws, 'connectors'] as const,
  openAlex: (ws: string) => ['ws', ws, 'connectors', 'openalex'] as const,
  workspaceMCP: (ws: string) => ['ws', ws, 'mcp-servers'] as const,
  workspaceMCPItem: (ws: string, id: string) => ['ws', ws, 'mcp-server', id] as const,
  runtimeSkillRequest: (ws: string, runtime: string, id: string) =>
    ['ws', ws, 'runtime', runtime, 'skill-request', id] as const,
  autoresearchRun: (ws: string, id: string) => ['ws', ws, 'autoresearch-run', id] as const,
  autoresearches: (ws: string) => ['ws', ws, 'autoresearch'] as const,
  autoresearch: (ws: string, id: string) => ['ws', ws, 'autoresearch', id] as const,
  autoresearchRuns: (ws: string, id: string) => ['ws', ws, 'autoresearch', id, 'runs'] as const,
};
