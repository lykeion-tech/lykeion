export interface Doc {
  version: number;
  text: string;
}
export interface DocStep {
  version: number;
  changes: unknown;
  client_id: string;
}
export interface Change {
  id: string;
  workspace_id: string;
  proposal_id: string;
  file_id: string;
  from_pos: number;
  to_pos: number;
  removed_text: string;
  file_action: string;
  status: 'pending' | 'stale' | 'kept' | 'undone';
  decided_by: string | null;
  decided_at: string | null;
  created_at: string;
}
export interface Comment {
  id: string;
  workspace_id: string;
  file_id: string;
  from_pos: number;
  to_pos: number;
  quote: string;
  line: number;
  author_user_id: string;
  body: string;
  mentions: string[];
  resolved_by: string | null;
  resolved_at: string | null;
  created_at: string;
}
export interface Build {
  id: string;
  project_id: string;
  seq: number;
  requested_by: string;
  status: string;
  sources: { file_id: string; path: string; n: number; sha256: string; size: number }[];
  has_pdf: boolean;
  has_log: boolean;
  has_synctex: boolean;
  pages: number | null;
  failure_reason: string | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
}
export interface Version {
  id: string;
  workspace_id: string;
  project_id: string;
  seq: number;
  title: string;
  by_user_id: string | null;
  build_id: string | null;
  files: { file_id: string; path: string; n: number }[];
  created_at: string;
}
export interface KernelSample {
  t: string;
  rss: number;
  cpu: number;
}
export interface MachineSample {
  t: string;
  mem_used: number;
  mem_total: number;
  cpu_used: number;
  cores: number;
  disk_free: number;
  disk_total: number;
}
export interface Compute {
  kernels: {
    chat_id: string;
    kernel: string;
    state: string;
    runtime_id: string;
    device_name: string;
    samples: KernelSample[] | null;
  }[];
  machines: { runtime_id: string; device_name: string; metrics: MachineSample | null }[];
}
export interface Template {
  id: string;
  label: string;
}
export interface Presence {
  user_id: string;
  name: string;
  anchor: number;
  head: number;
}
