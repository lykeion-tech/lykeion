import type {
  Activity,
  Inquiry,
  InquiryDetail,
  Project,
  ResearchService,
  Stage,
  TransitionReceipt,
  Workspace,
  WorkspaceSnapshot,
  ResourceKind,
  ResourceMap,
  ResourceCreateMap,
} from '../../../../packages/contracts/src/research';

import { stages, resourceKinds } from '../../../../packages/contracts/src/research';
import { localStore } from '../storage';

export class ServiceError extends Error {
  constructor(
    message: string,
    public status = 0,
  ) {
    super(message);
  }
}
type Check = (value: unknown) => boolean;
const string: Check = (value) => typeof value === 'string';
const boolean: Check = (value) => typeof value === 'boolean';
const revision: Check = (value) => Number.isInteger(value) && Number(value) >= 1;
const oneOf =
  (...values: readonly string[]): Check =>
  (value) =>
    typeof value === 'string' && values.includes(value);
const optional =
  (check: Check): Check =>
  (value) =>
    value === undefined || check(value);
const nullable =
  (check: Check): Check =>
  (value) =>
    value === null || check(value);
const array =
  (check: Check): Check =>
  (value) =>
    Array.isArray(value) && value.every(check);
const object =
  (fields: Record<string, Check>): Check =>
  (value) =>
    !!value &&
    typeof value === 'object' &&
    !Array.isArray(value) &&
    Object.entries(fields).every(([key, check]) => check((value as Record<string, unknown>)[key]));
const workspaceCheck = object({
  id: string,
  name: string,
  slug: string,
  taskPrefix: string,
  researchContext: string,
});
const nonnegative: Check = (value) =>
  typeof value === 'number' && Number.isFinite(value) && value >= 0;
const stringRecord: Check = (value) =>
  !!value &&
  typeof value === 'object' &&
  !Array.isArray(value) &&
  Object.values(value).every(string);
const wakeupCheck = object({
  id: string,
  condition: oneOf(
    'at-time',
    'interval',
    'reply',
    'field',
    'run-end',
    'subtasks',
    'pull-request',
    'task-status',
    'custom',
  ),
  agentId: nullable(string),
  instructions: string,
  parameters: stringRecord,
  status: oneOf('draft'),
  createdAt: string,
});
const pullRequestCheck = object({
  id: string,
  url: string,
  status: oneOf('unverified'),
  createdAt: string,
});
const timestamp: Check = (value) => typeof value === 'string' && Number.isFinite(Date.parse(value));
const runCheck = object({
  id: string,
  name: string,
  agentId: nullable(string),
  startedAt: timestamp,
  endedAt: optional(timestamp),
  status: oneOf('running', 'succeeded', 'failed', 'cancelled'),
  costUsd: optional(nonnegative),
  model: optional(string),
  costParts: optional(
    array(
      object({
        kind: oneOf('input', 'output', 'cache-read', 'cache-write'),
        tokens: nonnegative,
        costUsd: optional(nonnegative),
      }),
    ),
  ),
});
const inquiryCheck = object({
  id: string,
  title: string,
  description: string,
  stage: oneOf(...stages),
  status: oneOf('active', 'blocked', 'done'),
  priority: oneOf('none', 'low', 'medium', 'high', 'urgent'),
  projectId: nullable(string),
  assigneeId: nullable(string),
  createdAt: string,
  revision,
  wakeupDrafts: optional(array(wakeupCheck)),
  pullRequests: optional(array(pullRequestCheck)),
  labels: optional(array(string)),
  startDate: optional(nullable(string)),
  dueDate: optional(nullable(string)),
  creatorId: optional(nullable(string)),
  parentId: optional(nullable(string)),
  updatedAt: optional(string),
});
const projectCheck = object({
  id: string,
  name: string,
  description: string,
  createdAt: string,
  status: optional(string),
});
const activityCheck = object({
  id: string,
  inquiryId: string,
  text: string,
  createdAt: string,
  kind: oneOf('comment', 'system'),
});
const claimCheck = object({
  id: string,
  text: string,
  status: oneOf('supported', 'contested', 'refuted', 'inconclusive', 'inadmissible'),
  evidenceRefs: array(string),
});
const triggerSpecCheck = object({
  kind: oneOf('schedule', 'webhook'),
  timeMode: optional(oneOf('time', 'interval')),
  time: optional(string),
  interval: optional((value) => Number.isInteger(value) && Number(value) > 0),
  unit: optional(oneOf('minutes', 'hours')),
  windowStart: optional(string),
  windowEnd: optional(string),
  dayMode: optional(oneOf('every', 'week', 'month')),
  weekdays: optional(
    array((value) => Number.isInteger(value) && Number(value) >= 0 && Number(value) <= 6),
  ),
  monthDay: optional(
    (value) => Number.isInteger(value) && Number(value) >= 1 && Number(value) <= 31,
  ),
  timezone: optional(string),
  cron: optional(string),
  eventFilters: optional(array(object({ key: string, value: string }))),
});
const resourceChecks: Record<ResourceKind, Check> = {
  agents: object({
    id: string,
    name: string,
    role: string,
    description: optional(string),
    instructions: optional(string),
    model: optional(string),
    access: optional(oneOf('personal', 'workspace', 'specific')),
    memberIds: optional(array(string)),
    suggestions: optional(array(string)),
    conversationStarters: optional(array(object({ label: string, prompt: string }))),
    thinking: optional(string),
    skillIds: optional(array(string)),
    disabledSkillIds: optional(array(string)),
    connectorIds: optional(array(string)),
    customArgs: optional(array(string)),
    avatar: optional(string),
    ownerId: optional(string),
    concurrency: optional((value) => typeof value === 'number' && Number.isFinite(value)),
    runtimeId: optional(nullable(string)),
    status: oneOf('online', 'offline', 'busy'),
  }),
  runtimes: object({
    id: string,
    name: string,
    machineId: optional(string),
    version: optional(string),
    provider: optional(string),
    platform: string,
    status: oneOf('connected', 'disconnected'),
  }),
  teams: object({
    id: string,
    name: string,
    description: string,
    agentIds: array(string),
    leaderAgentId: optional(nullable(string)),
  }),
  skills: object({ id: string, name: string, description: string, instructions: string }),
  connectors: object({
    id: string,
    name: string,
    provider: string,
    status: oneOf('unconfigured', 'connected', 'error'),
  }),
  machines: object({
    id: string,
    name: string,
    platform: string,
    status: oneOf('connected', 'disconnected'),
  }),
  colleagues: object({
    id: string,
    name: string,
    email: string,
    role: oneOf('owner', 'member', 'viewer'),
  }),
  autopilots: object({
    triggerSpec: optional(triggerSpecCheck),
    subscriberIds: optional(array(string)),
    id: string,
    name: string,
    instructions: string,
    schedule: string,
    agentId: nullable(string),
    status: oneOf('draft', 'paused', 'active'),
    outputMode: optional(oneOf('task', 'run')),
    projectId: optional(nullable(string)),
  }),
  conversations: object({ id: string, title: string, agentId: string, updatedAt: string }),
  notifications: object({
    id: string,
    title: string,
    body: string,
    read: boolean,
    createdAt: string,
    inquiryId: nullable(string),
  }),
};
const snapshotCheck = object({
  workspace: workspaceCheck,
  inquiries: array(inquiryCheck),
  projects: array(projectCheck),
  ...Object.fromEntries(resourceKinds.map((kind) => [kind, array(resourceChecks[kind])])),
});
const detailCheck = object({
  inquiry: inquiryCheck,
  activity: array(activityCheck),
  claims: array(claimCheck),
  runs: optional(array(runCheck)),
});
const routingCheck = object({
  provider: oneOf('jev'),
  model: string,
  policyVersion: string,
  destination: oneOf(...stages),
  evidenceRefs: array(string),
});
const transitionCheck: Check = (value) =>
  object({ accepted: boolean, inquiry: inquiryCheck, reasons: array(string) })(value) &&
  ((value as TransitionReceipt).routing === undefined ||
    routingCheck((value as TransitionReceipt).routing));
function checked<T>(value: unknown, check: Check): T {
  if (!check(value))
    throw new ServiceError(
      'The control plane returned an invalid response. No workspace data was changed.',
      502,
    );
  return value as T;
}
function emptyResources() {
  return {
    agents: [],
    runtimes: [],
    teams: [],
    skills: [],
    connectors: [],
    machines: [],
    colleagues: [],
    autopilots: [],
    conversations: [],
    notifications: [],
  };
}
const storageKey = 'lykeion.workspace.v1';
interface LocalState extends WorkspaceSnapshot {
  activity: Activity[];
}
function initial(): LocalState {
  return {
    workspace: {
      id: 'local',
      name: 'Lykeion',
      slug: 'local',
      taskPrefix: 'LYK',
      researchContext: '',
    },
    inquiries: [],
    projects: [],
    ...emptyResources(),
    activity: [],
  };
}
export class LocalResearchService implements ResearchService {
  readonly mode = 'local' as const;
  constructor(private storage: Pick<Storage, 'getItem' | 'setItem'> = localStore) {}
  private read(): LocalState {
    let raw: string | null;
    try {
      raw = this.storage.getItem(storageKey);
    } catch {
      throw new ServiceError('Local storage is unavailable. No data was changed.');
    }
    if (!raw) return initial();
    try {
      const state = { ...emptyResources(), ...JSON.parse(raw) } as LocalState;
      if (!snapshotCheck(state) || !array(activityCheck)(state.activity)) throw new Error();
      return state;
    } catch {
      throw new ServiceError(
        'The saved workspace could not be read. Your stored data has been preserved.',
      );
    }
  }
  private save(state: LocalState) {
    try {
      this.storage.setItem(storageKey, JSON.stringify(state));
    } catch {
      throw new ServiceError(
        'Could not save this change. Local storage may be full or unavailable.',
      );
    }
  }
  async load() {
    return this.read();
  }
  async listResources<K extends ResourceKind>(kind: K): Promise<ResourceMap[K][]> {
    return this.read()[kind] as ResourceMap[K][];
  }
  async readResource<K extends ResourceKind>(kind: K, id: string): Promise<ResourceMap[K]> {
    const record = (await this.listResources(kind)).find((item) => item.id === id);
    if (!record) throw new ServiceError('Record not found.', 404);
    return record;
  }
  async createResource<K extends ResourceKind>(
    kind: K,
    input: ResourceCreateMap[K],
  ): Promise<ResourceMap[K]> {
    if (['runtimes', 'machines', 'colleagues', 'conversations', 'notifications'].includes(kind))
      throw new ServiceError(
        'Connect the control plane to perform this action. Local drafts cannot create connections, membership, or agent activity.',
        503,
      );
    const named = input as { name?: string };
    if (!named.name?.trim()) throw new ServiceError('A name is required.');
    const state = this.read();
    const status =
      kind === 'agents'
        ? { status: 'offline' }
        : kind === 'connectors'
          ? { status: 'unconfigured' }
          : kind === 'autopilots'
            ? { status: 'draft' }
            : {};
    const record = checked<ResourceMap[K]>(
      { ...input, name: named.name.trim(), id: crypto.randomUUID(), ...status },
      resourceChecks[kind],
    );
    (state[kind] as ResourceMap[K][]).push(record);
    this.save(state);
    return record;
  }
  async updateResource<K extends ResourceKind>(
    kind: K,
    id: string,
    patch: Partial<ResourceCreateMap[K]>,
  ): Promise<ResourceMap[K]> {
    if (['runtimes', 'machines', 'colleagues', 'conversations', 'notifications'].includes(kind))
      throw new ServiceError('This resource is managed by the control plane.', 503);
    const state = this.read();
    const collection = state[kind] as ResourceMap[K][];
    const index = collection.findIndex((item) => item.id === id);
    if (index < 0) throw new ServiceError('Record not found.', 404);
    if ('name' in patch && (typeof patch.name !== 'string' || !patch.name.trim()))
      throw new ServiceError('A name is required.');
    const record = checked<ResourceMap[K]>(
      { ...collection[index], ...patch, id },
      resourceChecks[kind],
    );
    collection[index] = record;
    this.save(state);
    return record;
  }
  async createInquiry(
    input: Pick<
      Inquiry,
      | 'title'
      | 'description'
      | 'priority'
      | 'projectId'
      | 'assigneeId'
      | 'labels'
      | 'startDate'
      | 'dueDate'
      | 'parentId'
    >,
  ) {
    if (!input.title.trim()) throw new ServiceError('A task title is required.');
    const state = this.read();
    if (input.parentId && !state.inquiries.some((item) => item.id === input.parentId))
      throw new ServiceError('Parent task not found.', 404);
    const inquiry: Inquiry = {
      ...input,
      title: input.title.trim(),
      id: crypto.randomUUID(),
      stage: 'Grounding',
      status: 'active',
      createdAt: new Date().toISOString(),
      revision: 1,
    };
    state.inquiries.push(inquiry);
    this.save(state);
    return inquiry;
  }
  async updateInquiry(
    id: string,
    revision: number,
    patch: Partial<
      Pick<
        Inquiry,
        | 'title'
        | 'description'
        | 'priority'
        | 'projectId'
        | 'assigneeId'
        | 'labels'
        | 'startDate'
        | 'dueDate'
        | 'wakeupDrafts'
        | 'pullRequests'
      >
    >,
  ) {
    const state = this.read();
    const item = state.inquiries.find((x) => x.id === id);
    if (!item) throw new ServiceError('Task not found.', 404);
    if (item.revision !== revision)
      throw new ServiceError('This task changed. Reload before saving.', 409);
    if (patch.title !== undefined && !patch.title.trim())
      throw new ServiceError('A task title is required.');
    if (patch.wakeupDrafts !== undefined && !array(wakeupCheck)(patch.wakeupDrafts))
      throw new ServiceError('Invalid wakeup draft.');
    if (patch.pullRequests !== undefined && !array(pullRequestCheck)(patch.pullRequests))
      throw new ServiceError('Invalid pull request reference.');
    Object.assign(item, patch, { revision: revision + 1 });
    this.save(state);
    return item;
  }
  async detail(id: string): Promise<InquiryDetail> {
    const state = this.read();
    const inquiry = state.inquiries.find((x) => x.id === id);
    if (!inquiry) throw new ServiceError('Task not found.', 404);
    return { inquiry, activity: state.activity.filter((x) => x.inquiryId === id), claims: [] };
  }
  async comment(id: string, text: string) {
    if (!text.trim()) throw new ServiceError('Write a comment first.');
    const state = this.read();
    if (!state.inquiries.some((x) => x.id === id)) throw new ServiceError('Task not found.', 404);
    const event: Activity = {
      id: crypto.randomUUID(),
      inquiryId: id,
      text: text.trim(),
      createdAt: new Date().toISOString(),
      kind: 'comment',
    };
    state.activity.push(event);
    this.save(state);
    return event;
  }
  async transition(id: string, _revision: number, _destination: Stage): Promise<TransitionReceipt> {
    const { inquiry } = await this.detail(id);
    return {
      accepted: false,
      inquiry,
      reasons: [
        'Connect the Go control plane to request a scientific gate review. Local drafts cannot advance scientific stages.',
      ],
    };
  }
  async createProject(input: Pick<Project, 'name' | 'description'>) {
    if (!input.name.trim()) throw new ServiceError('A project name is required.');
    const state = this.read();
    const project = {
      ...input,
      name: input.name.trim(),
      id: crypto.randomUUID(),
      createdAt: new Date().toISOString(),
    };
    state.projects.push(project);
    this.save(state);
    return project;
  }
  async updateWorkspace(
    patch: Partial<Pick<Workspace, 'name' | 'researchContext' | 'taskPrefix'>>,
  ) {
    if (patch.name !== undefined && !patch.name.trim())
      throw new ServiceError('A workspace name is required.');
    const state = this.read();
    Object.assign(state.workspace, patch);
    this.save(state);
    return state.workspace;
  }
}

export class HttpResearchService implements ResearchService {
  readonly mode = 'http' as const;
  constructor(private baseUrl: string) {
    let url: URL;
    try {
      url = new URL(baseUrl);
    } catch {
      throw new ServiceError('Enter a valid HTTP(S) API URL.');
    }
    if (
      !['http:', 'https:'].includes(url.protocol) ||
      url.username ||
      url.password ||
      url.search ||
      url.hash
    )
      throw new ServiceError('Use an HTTP(S) API URL without embedded credentials.');
    this.baseUrl = url.href.replace(/\/$/, '');
  }
  private async request<T>(
    path: string,
    check: Check,
    method = 'GET',
    body?: unknown,
    signal?: AbortSignal,
  ): Promise<T> {
    let response: Response;
    try {
      response = await fetch(`${this.baseUrl}/v1${path}`, {
        method,
        credentials: 'include',
        headers:
          body === undefined
            ? {}
            : { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal,
      });
    } catch (error) {
      if (error instanceof Error && error.name === 'AbortError') throw error;
      throw new ServiceError('Cannot reach the control plane. Check your API connection.');
    }
    if (!response.ok) {
      const error = (await response.json().catch(() => ({}))) as { message?: string };
      throw new ServiceError(
        typeof error?.message === 'string'
          ? error.message
          : `The request failed (${response.status}).`,
        response.status,
      );
    }
    let data: unknown;
    try {
      data = await response.json();
    } catch {
      throw new ServiceError('The control plane returned unreadable JSON.', 502);
    }
    return checked<T>(data, check);
  }
  listResources<K extends ResourceKind>(kind: K, signal?: AbortSignal) {
    return this.request<ResourceMap[K][]>(
      `/resources/${kind}`,
      array(resourceChecks[kind]),
      'GET',
      undefined,
      signal,
    );
  }
  readResource<K extends ResourceKind>(kind: K, id: string, signal?: AbortSignal) {
    return this.request<ResourceMap[K]>(
      `/resources/${kind}/${encodeURIComponent(id)}`,
      resourceChecks[kind],
      'GET',
      undefined,
      signal,
    );
  }
  createResource<K extends ResourceKind>(kind: K, input: ResourceCreateMap[K]) {
    return this.request<ResourceMap[K]>(`/resources/${kind}`, resourceChecks[kind], 'POST', input);
  }
  updateResource<K extends ResourceKind>(
    kind: K,
    id: string,
    patch: Partial<ResourceCreateMap[K]>,
  ) {
    return this.request<ResourceMap[K]>(
      `/resources/${kind}/${encodeURIComponent(id)}`,
      resourceChecks[kind],
      'PATCH',
      patch,
    );
  }
  load(signal?: AbortSignal) {
    return this.request<WorkspaceSnapshot>('/workspace', snapshotCheck, 'GET', undefined, signal);
  }
  createInquiry(
    input: Pick<
      Inquiry,
      | 'title'
      | 'description'
      | 'priority'
      | 'projectId'
      | 'assigneeId'
      | 'labels'
      | 'startDate'
      | 'dueDate'
      | 'parentId'
    >,
  ) {
    return this.request<Inquiry>('/inquiries', inquiryCheck, 'POST', input);
  }
  updateInquiry(
    id: string,
    revision: number,
    patch: Partial<
      Pick<
        Inquiry,
        | 'title'
        | 'description'
        | 'priority'
        | 'projectId'
        | 'assigneeId'
        | 'labels'
        | 'startDate'
        | 'dueDate'
        | 'wakeupDrafts'
        | 'pullRequests'
      >
    >,
  ) {
    return this.request<Inquiry>(`/inquiries/${encodeURIComponent(id)}`, inquiryCheck, 'PATCH', {
      revision,
      ...patch,
    });
  }
  detail(id: string) {
    return this.request<InquiryDetail>(`/inquiries/${encodeURIComponent(id)}`, detailCheck);
  }
  comment(id: string, text: string) {
    return this.request<Activity>(
      `/inquiries/${encodeURIComponent(id)}/comments`,
      activityCheck,
      'POST',
      { text },
    );
  }
  transition(id: string, revision: number, destination: Stage) {
    return this.request<TransitionReceipt>(
      `/inquiries/${encodeURIComponent(id)}/transitions`,
      transitionCheck,
      'POST',
      { revision, destination },
    );
  }
  createProject(input: Pick<Project, 'name' | 'description'>) {
    return this.request<Project>('/projects', projectCheck, 'POST', input);
  }
  updateWorkspace(patch: Partial<Pick<Workspace, 'name' | 'researchContext' | 'taskPrefix'>>) {
    return this.request<Workspace>('/workspace', workspaceCheck, 'PATCH', patch);
  }
}
