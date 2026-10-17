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
  pack_name: string;
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
  assignee_type: 'member' | 'agent' | null;
  assignee_id: string | null;
  owner_id: string;
  budget_usd: number;
  deadline: string | null;
  spent_usd: number;
  board_column: Column;
  state: 'working' | 'in_review' | 'ready';
  flag: '' | 'escalated' | 'out_of_budget';
  ending: '' | 'released' | 'stopped';
  end_reason: string;
  rounds: number;
  route_backs: number;
  stale: string[];
  rerun: string;
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
  start_kind: '' | 'question' | 'dataset' | 'claim';
  cap: number; // route_backs counts uses of this cap
  block_kind: BlockKind;
  reopens: number;
}

export type BlockKind =
  '' | 'cap' | 'infra' | 'no_feasible_test' | 'human_run' | 'too_small_to_split' | 'drift';

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

// The body of a record of kind "gate" (server/internal/board/gate.go).
export interface GateCheck {
  id: string;
  judge: 'code' | 'jev' | 'reviewer';
  owner?: string;
  to?: string;
  result: 'pass' | 'fail' | 'attention';
  evidence?: string[];
  model?: string;
  question?: string;
  p?: number;
  choice?: string;
  note?: string;
}
// The body of a record of kind "choice": who answered one gate question.
export interface Choice {
  question: string;
  candidates: Record<string, string>;
  answer?: string;
  p?: Record<string, number>;
  model?: string;
  input_hash: string;
  judge: 'jev' | 'reviewer' | 'fallback';
  independence?: 'different_family' | 'same_family';
  judge_harness?: string;
  judge_model?: string;
}
export interface Gate {
  column: Column;
  round: number;
  input_digest: string;
  checks: GateCheck[];
  verdict: 'pass' | 'fail' | 'cannot_proceed';
  route?: { column: Column; step?: string; reason: string };
  reason?: string;
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
  kind: 'escalated' | 'out_of_budget' | 'released' | 'publish_request' | 'revised' | 'reopened';
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

// A Team (code: pack) in the catalog.
export interface Field {
  name: string;
  description: string;
  start_kinds: string[];
  version: number | string;
  source: string;
}
