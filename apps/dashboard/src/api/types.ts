// The server's JSON, field for field (server/pkg/db/generated/models.go plus
// the fields the list routes add). Times are RFC 3339 strings.

export type Role = 'owner' | 'admin' | 'member';
export const careerStages = [
  'undergraduate',
  'masters',
  'phd',
  'postdoc',
  'research_staff',
  'faculty',
  'industry',
  'other',
] as const;
export type CareerStage = '' | (typeof careerStages)[number];

export interface Me {
  id: string;
  email: string;
  name: string;
  google_sub: string | null;
  timezone: string;
  created_at: string;
  career_stage: CareerStage;
  onboarded_at: string | null;
}

export interface Workspace {
  id: string;
  slug: string;
  name: string;
  created_at: string;
  description: string;
  research_context: string;
  card_prefix: string;
  logo_blob: string;
  display_prefix: string;
  event_cursor: number; // the workspace's latest event id: where a new /ws subscription starts
  role?: Role; // present in GET /api/workspaces
  settings?: Record<string, unknown>; // present in GET /api/workspaces/{id}
}

export type Column =
  'backlog' | 'grounding' | 'hypotheses' | 'experimentation' | 'validation' | 'reporting' | 'done';

export interface Card {
  id: string;
  workspace_id: string;
  number: number;
  title: string;
  question: string;
  research_id: string | null;
  assignee_type: 'member' | 'agent' | 'team' | null;
  assignee_id: string | null;
  owner_id: string;
  deadline: string | null;
  spent_usd: number;
  board_column: Column;
  state: 'working' | 'done' | 'blocked' | 'stopped';
  flag: '' | 'escalated';
  ending: '' | 'released' | 'stopped';
  end_reason: string;
  rounds: number;
  follows_from: string[];
  version: number;
  created_at: string;
  round: number;
  handoff_note: string;
  gating_until: string | null;
  updated_at: string;
  position: number;
  queued: boolean; // GET /api/cards only
  role: string; // GET /api/cards only; '' when no run is queued, dispatched or running
  pack_id: string | null;
  block_kind: BlockKind;
  scope_from: Exclude<Column, 'backlog' | 'done'>;
  scope_to: Exclude<Column, 'backlog' | 'done'>;
  // The Team working each column of the scope, keyed by column.
  teams: Record<string, { pack_id: string; name: string; version: string; stage: string }>;
  redos: number;
  off_label: string[];
}

export type BlockKind = '' | 'low_score';

export interface Comment {
  id: string;
  workspace_id: string;
  card_id: string;
  author_id: string;
  body: string;
  created_at: string;
}

export interface LykEvent {
  id: number;
  workspace_id: string;
  card_id: string | null;
  type: string;
  actor_type: 'user' | 'role' | 'system';
  actor_id: string | null;
  payload: Record<string, unknown>;
  created_at: string;
}

export interface StudyRecord {
  id: string;
  card_id: string;
  board_column: Column;
  round: number;
  kind: string;
  role: string;
  key: string;
  body: Record<string, unknown>;
  blob_key: string;
  agent_run_id: string | null;
  superseded_at: string | null;
  created_at: string;
}

export interface AgentRun {
  id: string;
  workspace_id: string;
  card_id: string;
  responsible_user_id: string;
  board_column: Column;
  round: number;
  role: string;
  harness: string;
  status: 'queued' | 'dispatched' | 'running' | 'completed' | 'failed' | 'cancelled';
  queue_reason: string | null;
  runtime_id: string | null;
  dispatched_at: string | null;
  started_at: string | null;
  completed_at: string | null;
  attempt: number;
  max_attempts: number;
  model: string;
  error: string;
  failure_reason: string | null;
  cost_usd: number;
  created_at: string;
  handoff_note: string;
}

export interface RunMessage {
  agent_run_id: string;
  attempt: number;
  seq: number;
  type: 'text' | 'thinking' | 'tool_use' | 'tool_result' | 'status' | 'log';
  tool: string;
  content: unknown;
  created_at: string;
}

export interface InboxItem {
  id: string;
  workspace_id: string;
  card_id: string;
  user_id: string;
  kind:
    | 'escalated'
    | 'released'
    | 'autoresearch_card'
    | 'autoresearch_report'
    | 'middle_score'
    | 'blocked'
    | 'no_tool'
    | 'scoring_unavailable'
    | 'done';
  text: string;
  read_at: string | null;
  resolved_at: string | null;
  created_at: string;
}

export interface Runtime {
  id: string;
  workspace_id: string;
  owner_id: string;
  daemon_id: string;
  harness: string;
  device_name: string;
  custom_name: string | null;
  status: 'online' | 'offline';
  last_seen_at: string;
  max_concurrent: number;
  created_at: string;
  active_runs: number; // dispatched or running on this runtime
  cli_version: string; // as the daemon reported it; '' when unknown
}

export interface Member {
  user_id: string;
  role: Role;
  created_at: string;
  email: string;
  name: string;
}

export interface Invitation {
  id: string;
  email: string;
  role: Exclude<Role, 'owner'>;
  expires_at: string;
  created_at: string;
}

export interface Token {
  id: string;
  prefix: string;
  name: string;
  expires_at: string;
  revoked_at: string | null;
  created_at: string;
}
export interface NewToken {
  id: string;
  token: string;
  prefix: string;
  name: string;
  expires_at: string;
}

export interface Research {
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}
export interface ResearchDetail extends Research {
  studies: Card[];
}
export interface AgentDefinition {
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  instructions: string;
  harness: 'claude' | 'codex';
  model: string;
  visibility: 'workspace' | 'private';
  owner_id: string;
  archived_at: string | null;
  created_at: string;
  updated_at: string;
}

// What a Team's manifest holds.
export interface TeamMember {
  name: string;
  role: string;
  agent: { harness: string; model: string; instructions: string };
  skills?: string[];
  connectors?: { name: string; catalogue?: string; url?: string; command?: string[] }[];
}
export interface TeamManifest {
  name: string;
  version: string;
  description: string;
  domain: string;
  stage: string;
  leader: TeamMember;
  auditor: TeamMember;
  members: TeamMember[];
  criteria: string[];
  steps: number;
}

// A Team in the store; added is whether the caller has added it.
export interface StoreTeam {
  id: string;
  name: string;
  version: string;
  source: 'shipped' | 'uploaded';
  domain: string;
  stage: string;
  manifest: TeamManifest;
  added: boolean;
}

// One Team with every version the caller may see, oldest first.
export interface TeamDetail {
  id: string;
  manifest: TeamManifest;
  versions: PackVersion[];
}

// One version of a Team the caller may see.
export interface PackVersion {
  id: string;
  name: string;
  version: string;
  sha256: string;
  source: 'shipped' | 'uploaded';
  uploaded_by: string | null;
  created_at: string;
  approvals_by_me: boolean;
  approved_at: string | null;
  pinned_studies: number;
}

export type Autoresearch = {
  id: string;
  owner_id: string;
  title: string;
  description: string;
  execution_mode: 'create_card' | 'run_only';
  title_template: string;
  status: 'active' | 'paused';
  pause_reason: string;
  created_at: string;
  assignee_type: 'agent' | 'team' | null;
  assignee_id: string | null;
  scope_from: Column;
  scope_to: Column;
};
export type AutoresearchTrigger = {
  id: string;
  kind: 'schedule' | 'webhook';
  cron: string;
  tz: string;
  next_run_at: string | null;
  provider: 'generic' | 'github';
  enabled: boolean;
  signed: boolean;
  created_at: string;
};
export type AutoresearchDetail = Autoresearch & {
  triggers: AutoresearchTrigger[];
  collaborators: string[];
  subscribers: string[];
};
export type AutoresearchRun = {
  id: string;
  trigger_id: string | null;
  source: string;
  planned_at: string;
  status: string;
  reason: string;
  card_id: string | null;
  report: string;
  created_at: string;
};
