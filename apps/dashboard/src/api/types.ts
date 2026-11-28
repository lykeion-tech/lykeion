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
  avatar_url: string;
  id: string;
  email: string;
  name: string;
  google_sub: string | null;
  timezone: string;
  created_at: string;
  career_stage: CareerStage;
  onboarded_at: string | null;
  /** Absent from servers older than personal TypeSafe keys. */
  typesafe_key_set?: boolean;
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

export type Column = 'grounding' | 'hypotheses' | 'experimentation' | 'validation' | 'done';

export type CheckStatus = 'pass' | 'unclear' | 'fail' | 'withdrawn';
export type ValidationStatus = 'supported' | 'refuted' | 'inconclusive' | 'invalid' | 'caveated';
export interface ReviewCheck {
  id: string;
  by: 'server' | 'jev';
  question?: string;
  inverted?: boolean;
  detail?: string;
  p?: number;
  status: CheckStatus;
  warning?: boolean;
  // Report checks: what Jev judged from.
  basis?: 'image' | 'text' | 'script';
}
// A finished study's report: the graph of its argument, built by the server
// from recorded rows only.
export type ReportNodeKind =
  | 'question'
  | 'paper'
  | 'dataset'
  | 'hypothesis'
  | 'experiment'
  | 'output'
  | 'finding'
  | 'objection'
  | 'doubt'
  | 'concept';
export interface ReportFact {
  subject: string;
  relation: string;
  object: string;
  quote: string;
  source_node: string;
  p: number;
}
export interface ReportLink {
  start: number;
  end: number;
  node: string;
  p: number;
}
export interface HypothesisMeasure {
  id: string;
  name: string;
  unit: string;
}
export interface ReportMeasurement {
  record: string;
  name: string;
  value: string;
  field: string;
  measure: HypothesisMeasure;
  output: string;
}
export interface ReportNode {
  id: string;
  kind: ReportNodeKind;
  label: string;
  paper_id?: string;
  // findings: supported|caveated|refuted|inconclusive|invalid; hypotheses:
  // kept|rejected; objections: upheld|not_upheld; papers, datasets and
  // doubts: pass|unclear|fail; otherwise ''.
  status: string;
  reasons: string[];
  quote: string;
  checks: ReviewCheck[];
  rerun?: unknown;
  run_id: string | null;
  agent: string;
  column: string;
  content_type?: string;
  measurements?: ReportMeasurement[];
  facts?: ReportFact[];
  novelty?: 'contradicts' | 'confirms' | 'new';
  // A finding whose hypothesis is an estimate: what was measured and how. Its
  // value is in measurements. Absent on other nodes and from older servers.
  estimate?: ReportEstimate;
}

export interface ReportEstimate {
  unit: string;
  method: string;
  reported?: { value: number; source: string; key: string };
}
export interface ReportEdge {
  from: string;
  to: string;
  kind:
    | 'tests'
    | 'grounds'
    | 'tested_by'
    | 'produced'
    | 'supports'
    | 'challenges'
    | 'contradicts'
    | 'uses'
    | 'is_a'
    | 'part_of'
    | 'associated_with'
    | 'increases'
    | 'decreases'
    | 'measured_by'
    | 'mentioned_in';
}
export interface ReportHandIn {
  text: string;
  agent_name: string;
  run_id: string;
  at: string;
}
export interface ReportSteering {
  kind: 'comment' | 'decision';
  author: string;
  text: string;
  at: string;
}
export interface StudyReport {
  card_id: string;
  number: number;
  title: string;
  question: string;
  stages: string[];
  finished_at: string;
  hand_in: ReportHandIn | null;
  supplementary_validation?: ReportHandIn;
  replay?: {
    steps: { of: string; exit_status: number; stderr?: string }[] | null;
    failure: string | null;
  };
  nodes: ReportNode[];
  edges: ReportEdge[];
  steering: ReportSteering[];
  links: ReportLink[];
  extras: 'pending' | 'ready' | 'none';
  checks: 'pending' | 'ready' | 'unavailable' | 'none';
  machine: string;
}
export interface ReviewItem {
  key: string;
  kind: string;
  label: string;
  identity?: string;
  status: CheckStatus | ValidationStatus;
  carried?: boolean;
  measure?: HypothesisMeasure;
  value?: string;
  output?: string;
  checks: ReviewCheck[];
  quote: string;
  cites: string[];
}
export interface ReviewAttempt {
  legacy_timeline?: boolean;
  jev_pass_id?: string;
  jev_started_at?: string;
  column: Column;
  attempt: number;
  round: number;
  at: string;
  items: ReviewItem[];
  missed?: ReviewItem[];
  recall?: { query: string; hits: number; screened: number };
  weakest?: { key: string; p: number };
  objector?: { ran: boolean; reason?: string; same_family: boolean };
  decision: 'pass' | 'carry' | 'redo' | 'block' | 'ask' | 'route_back';
}
export interface ReviewColumn {
  column: Column;
  attempts: ReviewAttempt[];
  mode?: string;
  statuses?: Record<string, unknown>[];
  objections?: Record<string, unknown>[];
  rerun?: Record<string, unknown> | null;
  route_back?: Record<string, unknown> | null;
  same_family?: boolean;
  objector?: { ran: boolean; reason?: string; same_family: boolean } | null;
}

export interface StudyProposalContent {
  title?: string;
  priority?: string;
  description: string;
  stage: Exclude<Column, 'done'>;
  assignee: { type: 'agent'; agent_id: string } | { type: 'team'; pack_id: string };
  project_id: string | null;
}

export interface StudyProposal extends Omit<StudyProposalContent, 'assignee'> {
  assignee: { type: 'agent'; agent_id: string } | { type: 'team'; pack_id?: string };
  id: string;
  workspace_id: string;
  owner_id: string;
  revision: number;
  updated_at: string;
  created_at: string;
}

export interface AgentStudyWork {
  card_id: string;
  active_role: string;
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
  start_date?: string | null;
  priority?: 'none' | 'urgent' | 'high' | 'medium' | 'low';
  spent_usd: number;
  board_column: Column;
  legacy_column?: Column | '';
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
  agent_running?: boolean; // GET /api/cards only; at least one run is running
  running_clis?: { board_column: string; harness: string }[] | null;
  role: string; // GET /api/cards only; '' when no run is queued, dispatched or running
  waiting?: string; // GET /api/cards only; as AgentRun.waiting for the card's waiting run
  pack_id: string | null;
  workflow?: string | null;
  workflow_release?: string | null;
  block_kind: BlockKind;
  // Why a study held outside a low score waits for its owner; empty or absent otherwise.
  hold_text?: string;
  scope_from: Exclude<Column, 'done'>;
  scope_to: Exclude<Column, 'done'>;
  // The Team working each column of the scope, keyed by column.
  teams: Record<string, { pack_id: string; name: string; stage: string }>;
  redos: number;
  off_label: string[];
  attachments: {
    id: string;
    name: string;
    size_bytes: number;
    content_type: string;
    record_key?: string;
  }[];
  services: { name: string }[];
  // A finished study that raised a report; only the single-study payload carries it.
  has_report?: boolean;
  // What every Grounding source must meet, as the researcher wrote them.
}

export type BlockKind = '' | 'low_score' | 'decision' | 'parked';

export interface Comment {
  id: string;
  workspace_id: string;
  card_id: string;
  author_id: string;
  body: string;
  attachments?: { id: string; name: string; content_type: string; size_bytes: number }[];
  created_at: string;
  reply_run_id?: string | null;
  steer_attempt?: number | null;
  cancelled?: boolean;
  // Which run's work the comment reached and how; null while it waits.
  delivery: CommentDelivery | null;
}

export interface CommentDelivery {
  how: 'running_turn' | 'member_result' | 'next_run';
  role: string;
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
  // role for a study's stage runs; report for the run that builds its report.
  kind?: string;
  harness: string;
  status: 'queued' | 'dispatched' | 'running' | 'completed' | 'failed' | 'cancelled';
  queue_reason: string | null;
  runtime_id: string | null;
  dispatched_at: string | null;
  started_at: string | null;
  first_started_at?: string | null;
  legacy_unverified_started?: boolean;
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
  // The answer of the run's latest hand-in, as Markdown; '' before one.
  answer: string;
  // The accepted hand-in for this attempt, when available.
  hand_in?: Record<string, unknown> | null;
  hand_in_id?: string;
  hand_in_message_seq?: number;
  // When the current attempt last recorded a message; null before its first.
  last_activity_at: string | null;
  // How each earlier attempt ended and on which machine. GET /api/cards/:id/runs only.
  attempt_ends?: AttemptEnd[];
  // "Waiting for <machine>: <why>" while the run waits on a machine its owner
  // must fix; '' otherwise. GET /api/cards/:id/runs only.
  waiting?: string;
  requires_output_attestation?: boolean;
}

export interface AttemptEnd {
  attempt: number;
  failure_reason: string;
  machine: string;
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

export interface DecisionItem {
  id: string;
  workspace_id: string;
  card_id: string | null;
  autoresearch_run_id: string | null;
  user_id: string;
  kind:
    | 'manual_decision'
    | 'agent_decision'
    | 'blocked'
    | 'no_tool'
    | 'scoring_unavailable'
    | 'waiting_for_machine'
    | 'machine_offline'
    | 'needs_decision'
    | 'study_report'
    | 'autoresearch_failed'
    | 'done';
  text: string;
  // What the item asks for, for a needs_decision; null on other kinds.
  cause?: string | null;
  read_at: string | null;
  resolved_at: string | null;
  created_at: string;
  options: DecisionOption[] | null;
  question_set?: DecisionQuestionSet | null;
  answers?: DecisionQuestionAnswer[] | null;
  default_option: string;
  status: 'open' | 'decided' | 'expired' | 'dismissed' | string;
  expires_at: string | null;
  // Set once a reminder went out; manual notes remain open after their deadline.
  reminded_at: string | null;
  chosen_option: string;
  guidance: string;
  decided_by: string | null;
  decided_at: string | null;
  raised_by: string;
  from_member: string;
  decision_kind: string;
  decide_by: '' | 'today' | 'this_week' | 'whenever' | 'date';
  decide_by_date: string | null;
  snoozed_until: string | null;
  kept: boolean;
  archived_at: string | null;
  can_answer: boolean;
  title?: string;
  context?: string;
  deadline?: string | null;
  can_edit?: boolean;
  can_resolve?: boolean;
  can_dismiss?: boolean;
}

export interface DecisionOption {
  id: string;
  label: string;
  consequence: string;
}

export interface DecisionQuestion {
  id: string;
  prompt: string;
  help_text: string | null;
  required: boolean;
  mode: 'single' | 'multiple' | 'text';
  options: DecisionOption[];
  allow_other: boolean;
}
export interface DecisionQuestionSet {
  questions: DecisionQuestion[];
}
export interface DecisionQuestionAnswer {
  question_id: string;
  option_ids: string[];
  text: string | null;
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
  unhealthy_reason?: string | null;
  last_seen_at: string;
  max_concurrent: number | null; // null: no limit of its own; its daemon's slots decide
  created_at: string;
  active_runs: number; // dispatched or running on this runtime
  cli_version: string; // as the daemon reported it; '' when unknown
  // The machine's platform as the daemon reported it; '' when not reported.
  os: string;
  arch: string;
  kernel: string;
  language: string;
  // Each language base on the machine ('python', 'r', 'julia'): 'ready',
  // 'building' or 'failed: <reason>'. Older daemon reports may omit a base.
  bases: Record<string, string>;
}

export interface Member {
  career_stage?: CareerStage;
  avatar_url: string;
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
  start_date?: string | null;
  due_date?: string | null;
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
  report_template?: string;
  default_report_template?: string;
  avatar_url: string;
  built_in_key?: string;
  conversation_starters?: { label: string; prompt: string }[];
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  instructions: string;
  harness: 'claude' | 'codex';
  model: string;
  stage: '' | 'grounding' | 'hypotheses' | 'experimentation' | 'validation';
  laboratory: '' | 'built-in';
  runtime_id: string | null;
  visibility: 'workspace' | 'private';
  owner_id: string;
  archived_at: string | null;
  // A copy of an agent made to work inside a Team.
  team_member?: boolean;
  source?: 'built_in' | 'custom';
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
  avatar_url: string;
  agent_clis?: ('claude' | 'codex')[];
  leader_cli?: 'claude' | 'codex';
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
  visibility: 'private' | 'workspace';
  uploaded_by: string | null;
  avatar_url: string;
  id: string;
  manifest: TeamManifest;
  my_agents?: Record<string, AgentDefinition>;
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
  keyless?: boolean;
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

export type WorkflowSummary = {
  slug: string;
  name: string;
  domain: string;
  field: string;
  subfield: string;
  description: string;
  tags: string[];
  authors: { name: string; organization: string }[];
  experience: string;
  expert_hours: number;
  agent_budget_sec: number;
  cpus: number;
  memory_mb: number;
};
export type WorkflowDetail = WorkflowSummary & {
  files: { path: string; size: number; embedded: boolean }[];
  readme: string;
  instruction: string;
};

export interface LabChat {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
}

export interface LabRef {
  study_id?: string;
  ref: string;
  title: string;
  via?: string;
  facts?: LabRef[];
}

export interface LabStatement {
  kind: 'fact' | 'lesson' | 'gap';
  text: string;
  cites: LabRef[];
}

export interface LabMessage {
  id: string;
  kind: 'question' | 'answer' | 'failure';
  body: string;
  statements: LabStatement[];
  evidence: LabRef[];
  created_at: string;
}

export interface LabChartAgent {
  id: string;
  name: string;
  emoji: string;
  role: 'leader' | 'member' | 'auditor';
  source: 'laboratory' | 'researcher';
  status: 'idle' | 'working' | 'needs_you';
  /** When the current status began. */
  since: string | null;
  studies: { id: string; title: string }[];
  machine: string | null;
  cli: 'claude' | 'codex' | null;
  /** "the researchers", "<Stage> leader", or "nobody: checks <Stage> independently". */
  reports_to: string;
}
export interface LabChartRoom {
  stage: 'grounding' | 'hypotheses' | 'experimentation' | 'validation';
  team: string;
  agents: LabChartAgent[];
}
export interface LabChartPerson {
  user_id: string;
  name: string;
  role: string;
  open_decisions: number;
}
export interface LabChart {
  rooms: LabChartRoom[];
  people: LabChartPerson[];
  ticker: { at: string; text: string } | null;
}
export interface Laboratory {
  id: string;
  name: string;
  rooms: {
    stage: LabChartRoom['stage'];
    team: string;
    agents: { name: string; role: 'leader' | 'member' | 'auditor' }[];
  }[];
}

// GET /api/slack. Unknown fields are tolerated; a disconnected workspace omits status and team.
export type SlackStatus = {
  connected: boolean;
  status?: 'active' | 'revoked';
  team_name?: string;
  drafting_agent_id?: string | null;
  my_binding: { slack_user_id: string } | null;
  visibility_note?: string;
};

export interface GitHubSettings {
  configured: boolean;
  auto_link: boolean;
  connections: {
    installation_id: number;
    account_login: string;
    connected_by: string;
    status: string;
    import_state: string;
  }[];
}

export interface StudyPullRequest {
  id: string;
  repo_owner: string;
  repo_name: string;
  number: number;
  html_url: string;
  title: string;
  state: string;
  author_login: string;
  additions: number;
  deletions: number;
  changed_files: number;
  ci_state?: string | null;
  ci_passed: number;
  ci_failing?: string[] | null;
  mergeability?: string | null;
  snapshot_stale?: boolean;
}
