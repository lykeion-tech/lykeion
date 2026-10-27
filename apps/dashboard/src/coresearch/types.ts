export type Cli = 'claude' | 'codex';
export interface ProjectRow {
  id: string;
  workspace_id: string;
  name: string;
  python_version: string | null;
  created_by: string;
  created_at: string;
  archived_at: string | null;
  updated_at: string;
  role: 'owner' | 'member';
}
export interface ChatRow {
  id: string;
  workspace_id: string;
  project_id: string;
  owner_id: string;
  mode: string;
  cli: Cli;
  daemon_id: string | null;
  kernel_request: string | null;
  kernel_request_seq: number;
  kernel_state: string;
  kernel_seen_at: string | null;
  created_at: string;
  // Supplied by the chat-list route; absent on chat creation.
  machine_name?: string;
}

export interface MessageRow {
  id: string;
  workspace_id: string;
  chat_id: string;
  kind: 'turn' | 'reply' | 'comment' | 'failure';
  author_user_id: string | null;
  agent_run_id: string | null;
  body: string;
  created_at: string;
  status: string | null;
  failure_reason: string | null;
}
export interface ProjectMember {
  user_id: string;
  role: 'owner' | 'member';
  project_id: string;
  workspace_id: string;
}
export interface FileRow {
  id: string;
  path: string;
  head: { n: number; size: number; sha256: string; created_at: string };
}
export interface CellOutput {
  type: 'stream' | 'text' | 'image' | 'error';
  name?: string;
  text?: string;
  mime?: string;
  data?: string;
  ename?: string;
  evalue?: string;
  traceback?: string[];
}
export interface CellRow {
  id: string;
  workspace_id: string;
  chat_id: string;
  seq: number;
  author_user_id: string | null;
  agent_run_id: string | null;
  kernel: string;
  source: string;
  status: string;
  execution_count: number | null;
  outputs: CellOutput[] | null;
  outputs_blob: string | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  failure_reason: string | null;
  claimed_daemon_id: string | null;
}
export interface SetupRow {
  id: string;
  workspace_id: string;
  project_id: string;
  chat_id: string;
  python: string | null;
  conda: string[];
  status: string;
  log: string;
  requested_by: string;
  created_at: string;
  finished_at: string | null;
  failure_reason: string | null;
  claimed_daemon_id: string | null;
}
export interface EnvironmentRow {
  python_version: string | null;
  setups: SetupRow[];
}
