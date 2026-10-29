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
  'grounding' | 'hypotheses' | 'experimentation' | 'validation' | 'reporting' | 'done';

// One column of a study offered to its owner for a Team rating.
export interface ColumnRating {
  column: Column;
  rating: number | null;
  dismissed: boolean;
}

export interface Card {
  machine?: string | null;
  machine_label?: string;
  machine_notice?: string;
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
  scope_from: Exclude<Column, 'done'>;
  scope_to: Exclude<Column, 'done'>;
  // The Team working each column of the scope, keyed by column.
  teams: Record<string, { pack_id: string; name: string; stage: string }>;
  redos: number;
  off_label: string[];
  attachments: { id: string; name: string; size_bytes: number; content_type: string }[];
  services: { name: string }[];
  predicates: { name: string; text: string }[];
  // Each pre-registered result's binding outcome so far; GET /api/cards/:id only.
  preregistration?: Preregistration[] | null;
}

export interface Preregistration {
  name: string;
  text: string;
  outcome: 'held' | 'missed' | 'unconfirmed';
  round: number;
  run: string;
  values: Record<string, number> | null;
  others: { run: string; values: Record<string, number> | null }[] | null;
  reason: string;
}

export type BlockKind = '' | 'low_score' | 'unconfirmed_results';

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
  machine_name?: string;
  machine_owner_id?: string | null;
  cancel_reason?: string | null;
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
  // The agent the run was started as; null for runs started before agents
  // were recorded, or when that agent was deleted.
  agent_id: string | null;
  agent_name: string | null;
  // What the run was asked to do, without Lykeion's framing; '' when not recorded.
  assignment: string;
  // The latest text of the current attempt, at most 600 characters.
  latest_text: string;
  // When the current attempt last recorded a message; null before its first.
  last_activity_at: string | null;
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
  card_id: string | null;
  autoresearch_run_id: string | null;
  user_id: string;
  kind:
    | 'escalated'
    | 'released'
    | 'autoresearch_card'
    | 'autoresearch_report'
    | 'middle_score'
    | 'blocked'
    | 'preregistered'
    | 'no_tool'
    | 'scoring_unavailable'
    | 'done';
  text: string;
  read_at: string | null;
  resolved_at: string | null;
  created_at: string;
}

export interface RuntimeModel {
  value: string;
  label: string;
  description?: string;
}
export interface Runtime {
  models?: RuntimeModel[] | null;
  visibility?: 'private' | 'workspace';
  shared_with?: string[];
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
  // The machine's platform as the daemon reported it; '' when not reported.
  os: string;
  arch: string;
  kernel: string;
  language: string;
  // Each language base on the machine ('python', 'r', 'julia'): 'ready',
  // 'building' or 'failed: <reason>'. A base not yet tried is absent.
  bases: Record<string, string>;
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
  status: 'planned' | 'in_progress' | 'paused' | 'completed' | 'cancelled';
  priority: 'none' | 'urgent' | 'high' | 'medium' | 'low';
  lead_type: 'member' | 'agent' | null;
  lead_id: string | null;
  created_by: string;
  created_at: string;
  updated_at: string;
}
export interface ResearchDetail extends Research {
  studies: Card[];
}
export interface AgentDefinition {
  built_in_key?: string;
  conversation_starters?: { label: string; prompt: string }[];
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
  // A fixed harness and model, or a model per harness Lykeion picks from.
  agent: {
    harness?: string;
    model?: string;
    models?: Record<string, string>;
    instructions: string;
  };
  skills?: string[];
  connectors?: { name: string; catalogue?: string; url?: string; command?: string[] }[];
}
export interface TeamManifest {
  name: string;
  description: string;
  domain: string;
  stage: string;
  leader: TeamMember;
  auditor: TeamMember;
  members: TeamMember[];
  criteria: string[];
  steps: number;
}

// A Team in the store; added is whether the caller has added it. A shipped
// Team is built in: every workspace has it and it can't be changed.
export interface StoreTeam {
  id: string;
  created_at: string;
  name: string;
  source: 'shipped' | 'uploaded';
  domain: string;
  stage: string;
  manifest: TeamManifest;
  added: boolean;
}

export interface TeamDetail {
  id: string;
  manifest: TeamManifest;
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
  workspace_id: string;
  autoresearch_id: string;
  acting_user_id: string;
  agent_run_id: string | null;
  payload: unknown;
  idempotency_key: string;
  updated_at: string;
  trigger_id: string | null;
  source: string;
  planned_at: string;
  status: string;
  reason: string;
  card_id: string | null;
  report: string;
  created_at: string;
};

export type AutoresearchRunDetail = {
  autoresearch_id: string;
  autoresearch_title: string;
  execution_mode: 'create_card' | 'run_only';
  run: AutoresearchRun;
};

export type SkillSource = { kind: string; location: string; runtime_id?: string };
export interface SkillSummary {
  policy_enabled: boolean;
  policy_revision: number;
  id: string;
  name: string;
  domain: string;
  description: string;
  origin: 'bundled' | 'imported';
  revision: number;
  created_by: string | null;
  source: SkillSource;
  contains_scripts: boolean;
  editable: boolean;
  licence?: string;
}
export type SkillFileMeta = { path: string; size: number; sha256: string; binary: boolean };
export interface SkillDetail extends SkillSummary {
  content: string;
  files: SkillFileMeta[];
}
export interface ConnectorSummary {
  policy_enabled: boolean;
  name: string;
  domain: string;
  purpose: string;
  tier: string;
  terms: string;
  enabled: boolean;
  reason?: string;
  key_set: boolean;
  takes_key: boolean;
  licence_confirmed: boolean;
  revision: number;
}
export interface WorkspaceMcpSummary {
  policy_enabled: boolean;
  policy_revision: number;
  id: string;
  name: string;
  transport: 'url' | 'command';
  revision: number;
  created_by: string;
  created_at: string;
  updated_at: string;
}
export type RuntimeSkillInventory = {
  key: string;
  name: string;
  description: string;
  source_path: string;
  provider: 'claude' | 'codex';
  root: 'provider' | 'universal';
  file_count: number;
};
export type RuntimeSkillOutcome =
  | { key: string; status: 'imported'; skill_id: string }
  | { key: string; status: 'conflict' | 'failed'; error: string };
export type RuntimeSkillOperation = 'list' | 'import';
export interface RuntimeSkillRequest {
  workspace_id: string;
  id: string;
  runtime_id: string;
  owner_id: string;
  daemon_id: string;
  operation: RuntimeSkillOperation;
  keys: string[];
  status: 'pending' | 'running' | 'completed' | 'failed' | 'timeout';
  acquisition_id: string | null;
  expires_at: string;
  acquired_at: string | null;
  completed_at: string | null;
  result: { skills?: RuntimeSkillInventory[]; items?: RuntimeSkillOutcome[] };
  result_blob_key: string;
  error: string;
  created_at: string;
}

export interface StorageCategory {
  key: 'workspaces' | 'environments' | 'state';
  path: string;
  bytes: number;
  complete: boolean;
  error?: string;
  filesystem_id?: string;
}
export interface StorageMachine {
  daemon_id: string;
  name: string;
  online: boolean;
  last_seen_at: string;
  snapshot: null | {
    measured_at: string;
    received_at: string;
    categories: StorageCategory[];
    filesystems: { id: string; available_bytes: number; paths: string[] }[];
  };
}
export interface StorageReport {
  machines: StorageMachine[];
}
export type MetricCoverage = 'complete' | 'partial' | 'missing';
export interface AnalyticsMetrics {
  includes_estimates: boolean;
  input_tokens: number;
  ordinary_input_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  output_tokens: number;
  cost_usd: number;
  cost_reported_usd: number;
  cost_estimated_usd: number;
  runtime_seconds: number;
  studies: number;
  coverage: {
    tokens: MetricCoverage;
    cost: MetricCoverage;
    time: MetricCoverage;
    studies: MetricCoverage;
    missing_usage_attempts: number;
    missing_timing_attempts: number;
  };
}
export interface AnalyticsReport {
  timezone: string;
  scope: string;
  association_basis: 'current';
  range: string;
  cadence: string;
  start: string;
  end: string;
  now: string;
  summary: AnalyticsMetrics;
  buckets: (AnalyticsMetrics & { start: string; end: string })[];
  agents: (AnalyticsMetrics & {
    agent_id: string | null;
    name: string;
    roles: string[];
    harnesses: string[];
  })[];
  unassigned: Omit<AnalyticsMetrics, 'coverage' | 'runtime_seconds' | 'studies'> & {
    attempts: number;
  };
}

export interface AffectedMachineRun {
  run_id: string;
  card_id: string;
  study_title: string;
  colleague: string;
}
export interface MachineColleagues {
  runs: {
    run_id: string;
    colleague: string;
    study_title: string;
    harness: string;
    started_at: string | null;
    status: string;
    input_tokens: number;
    output_tokens: number;
    cost_usd: number;
  }[];
  totals: {
    colleague: string;
    last_7_days: { tokens: number; cost_usd: number };
    last_30_days: { tokens: number; cost_usd: number };
  }[];
}
