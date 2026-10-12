/** Renderer port. The Go API owns acceptance, revisions, and routing. */
export const stages = ['Grounding', 'Hypotheses', 'Experimentation', 'Validation', 'Reporting'] as const;
export type Stage = typeof stages[number];
export type ClaimStatus = 'supported' | 'contested' | 'refuted' | 'inconclusive' | 'inadmissible';
export interface Inquiry {
  id: string; title: string; description: string; stage: Stage;
  status: 'active' | 'blocked' | 'done'; priority: 'none' | 'low' | 'medium' | 'high' | 'urgent';
  projectId: string | null; assigneeId: string | null; createdAt: string; revision: number;
  wakeupDrafts?: WakeupDraft[]; pullRequests?: PullRequestReference[];
  labels?: string[]; startDate?: string | null; dueDate?: string | null; creatorId?: string | null; parentId?: string | null; updatedAt?: string;
}
export interface Project { id: string; name: string; description: string; createdAt: string; status?: string }
export interface Agent { id: string; name: string; role: string; description?: string; instructions?: string; model?: string; access?: 'personal' | 'workspace' | 'specific'; memberIds?: string[]; suggestions?: string[]; conversationStarters?: {label:string;prompt:string}[]; thinking?: string; skillIds?: string[]; disabledSkillIds?: string[]; connectorIds?: string[]; customArgs?: string[]; avatar?: string; ownerId?: string; concurrency?: number; status: 'online' | 'offline' | 'busy'; runtimeId?: string | null }
export interface Runtime { id: string; name: string; machineId?: string; version?: string; provider?: string; status: 'connected' | 'disconnected'; platform: string }
export interface Team { id: string; name: string; description: string; agentIds: string[]; leaderAgentId?: string | null }
export interface Skill { id: string; name: string; description: string; instructions: string }
export interface Connector { id: string; name: string; provider: string; status: 'unconfigured' | 'connected' | 'error' }
export interface Machine { id: string; name: string; platform: string; status: 'connected' | 'disconnected' }
export interface Colleague { id: string; name: string; email: string; role: 'owner' | 'member' | 'viewer' }
export interface AutopilotTriggerSpec { kind: 'schedule'|'webhook'; timeMode?: 'time'|'interval'; time?: string; interval?: number; unit?: 'minutes'|'hours'; windowStart?: string; windowEnd?: string; dayMode?: 'every'|'week'|'month'; weekdays?: number[]; monthDay?: number; timezone?: string; cron?: string; eventFilters?: {key:string;value:string}[] }
export interface Autopilot { triggerSpec?: AutopilotTriggerSpec; subscriberIds?: string[]; id: string; name: string; instructions: string; schedule: string; agentId: string | null; status: 'draft' | 'paused' | 'active'; outputMode?: 'task' | 'run'; projectId?: string | null }
export interface Conversation { id: string; title: string; agentId: string; updatedAt: string }
export interface Notification { id: string; title: string; body: string; read: boolean; createdAt: string; inquiryId: string | null }
export interface ResourceMap {
  agents: Agent; runtimes: Runtime; teams: Team; skills: Skill; connectors: Connector; machines: Machine;
  colleagues: Colleague; autopilots: Autopilot; conversations: Conversation; notifications: Notification;
}
export type ResourceKind = keyof ResourceMap;
export const resourceKinds = ['agents', 'runtimes', 'teams', 'skills', 'connectors', 'machines', 'colleagues', 'autopilots', 'conversations', 'notifications'] as const satisfies readonly ResourceKind[];
export interface ResourceCreateMap {
  agents: Pick<Agent, 'name' | 'role' | 'runtimeId' | 'description' | 'instructions' | 'model' | 'access' | 'memberIds' | 'suggestions' | 'conversationStarters' | 'thinking' | 'skillIds' | 'disabledSkillIds' | 'connectorIds' | 'customArgs' | 'avatar'>; runtimes: Pick<Runtime, 'name' | 'platform'>;
  teams: Pick<Team, 'name' | 'description' | 'agentIds' | 'leaderAgentId'>; skills: Pick<Skill, 'name' | 'description' | 'instructions'>;
  connectors: Pick<Connector, 'name' | 'provider'>; machines: Pick<Machine, 'name' | 'platform'>;
  colleagues: Pick<Colleague, 'name' | 'email' | 'role'>;
  autopilots: Pick<Autopilot, 'name' | 'instructions' | 'schedule' | 'agentId' | 'outputMode' | 'projectId' | 'triggerSpec' | 'subscriberIds'>;
  conversations: Pick<Conversation, 'title' | 'agentId'>;
  notifications: Pick<Notification, 'title' | 'body' | 'inquiryId'>;
}
export type ResourceCollections = { [K in ResourceKind]: ResourceMap[K][] };
export interface Claim { id: string; text: string; status: ClaimStatus; evidenceRefs: string[] }
export interface Activity { id: string; inquiryId: string; text: string; createdAt: string; kind: 'comment' | 'system' }
export interface Workspace { id: string; name: string; slug: string; taskPrefix: string; researchContext: string }
export interface WorkspaceSnapshot extends ResourceCollections {
  workspace: Workspace; inquiries: Inquiry[]; projects: Project[]; agents: Agent[]; runtimes: Runtime[];
}
export interface TransitionReceipt {
  accepted: boolean; inquiry: Inquiry; reasons: string[];
  routing?: { provider: 'jev'; model: string; policyVersion: string; destination: Stage; evidenceRefs: string[] };
}
export type WakeupCondition = 'at-time' | 'interval' | 'reply' | 'field' | 'run-end' | 'subtasks' | 'pull-request' | 'task-status' | 'custom';
export interface WakeupDraft { id: string; condition: WakeupCondition; agentId: string | null; instructions: string; parameters: Record<string, string>; status: 'draft'; createdAt: string }
export interface PullRequestReference { id: string; url: string; status: 'unverified'; createdAt: string }
export interface RunCostPart { kind: 'input' | 'output' | 'cache-read' | 'cache-write'; tokens: number; costUsd?: number }
export interface ExecutionRun { id: string; name: string; agentId: string | null; startedAt: string; endedAt?: string; status: 'running' | 'succeeded' | 'failed' | 'cancelled'; costUsd?: number; model?: string; costParts?: RunCostPart[] }
export interface InquiryDetail { inquiry: Inquiry; activity: Activity[]; claims: Claim[]; runs?: ExecutionRun[] }
export interface ResearchService {
  readonly mode: 'local' | 'http';
  listResources<K extends ResourceKind>(kind: K, signal?: AbortSignal): Promise<ResourceMap[K][]>;
  readResource<K extends ResourceKind>(kind: K, id: string, signal?: AbortSignal): Promise<ResourceMap[K]>;
  createResource<K extends ResourceKind>(kind: K, input: ResourceCreateMap[K]): Promise<ResourceMap[K]>;
  updateResource<K extends ResourceKind>(kind: K, id: string, patch: Partial<ResourceCreateMap[K]>): Promise<ResourceMap[K]>;
  load(signal?: AbortSignal): Promise<WorkspaceSnapshot>;
  createInquiry(input: Pick<Inquiry, 'title' | 'description' | 'priority' | 'projectId' | 'assigneeId' | 'labels' | 'startDate' | 'dueDate' | 'parentId'>): Promise<Inquiry>;
  updateInquiry(id: string, revision: number, patch: Partial<Pick<Inquiry, 'title' | 'description' | 'priority' | 'projectId' | 'assigneeId' | 'labels' | 'startDate' | 'dueDate' | 'wakeupDrafts' | 'pullRequests'>>): Promise<Inquiry>;
  detail(id: string): Promise<InquiryDetail>;
  comment(id: string, text: string): Promise<Activity>;
  transition(id: string, revision: number, destination: Stage): Promise<TransitionReceipt>;
  createProject(input: Pick<Project, 'name' | 'description'>): Promise<Project>;
  updateWorkspace(patch: Partial<Pick<Workspace, 'name' | 'researchContext' | 'taskPrefix'>>): Promise<Workspace>;
}
