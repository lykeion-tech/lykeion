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
