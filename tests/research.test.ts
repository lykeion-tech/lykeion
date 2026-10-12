import { beforeEach, describe, expect, it, vi } from 'vitest';
import { LocalResearchService } from '../apps/dashboard/src/services/research';
describe('local workspace persistence and scientific boundaries', () => {
  let data: Map<string, string>; let service: LocalResearchService;
  beforeEach(() => {
    data = new Map();
    service = new LocalResearchService({ getItem: k => data.get(k) ?? null, setItem: (k, v) => { data.set(k, v); } });
  });
  const draft = { title: 'Can this result reproduce?', description: '', priority: 'none' as const, projectId: null, assigneeId: null };
  it('starts without invented product records', async () => {
    const state = await service.load();
    expect([state.inquiries, state.projects, state.agents, state.runtimes]).toEqual([[], [], [], []]);
  });
  it('persists only user-created drafts and comments', async () => {
    const item = await service.createInquiry(draft);
    await service.comment(item.id, 'Check the archived inputs.');
    const reopened = new LocalResearchService({ getItem: k => data.get(k) ?? null, setItem: (k,v) => { data.set(k,v); } });
    expect((await reopened.detail(item.id)).activity[0].text).toBe('Check the archived inputs.');
  });
  it('rejects stale edits rather than silently overwriting', async () => {
    const item = await service.createInquiry(draft);
    await service.updateInquiry(item.id, 1, { title: 'Revised question' });
    await expect(service.updateInquiry(item.id, 1, { title: 'Stale revision' })).rejects.toMatchObject({ status: 409 });
  });
  it('cannot advance a draft or produce scientific support locally', async () => {
    const item = await service.createInquiry(draft);
    const receipt = await service.transition(item.id, 1, 'Hypotheses');
    expect(receipt.accepted).toBe(false);
    expect((await service.detail(item.id)).inquiry.stage).toBe('Grounding');
    expect((await service.detail(item.id)).claims).toEqual([]);
  });
  it('preserves corrupt data instead of replacing it with an empty workspace', async () => {
    data.set('lykeion.workspace.v1', '{broken');
    await expect(service.load()).rejects.toThrow('preserved');
    expect(data.get('lykeion.workspace.v1')).toBe('{broken');
  });
});

describe('resource records and HTTP boundary', () => {
  it('stores only explicitly created inactive resource drafts', async () => {
    const data = new Map<string, string>();
    const service = new LocalResearchService({ getItem: key => data.get(key) ?? null, setItem: (key, value) => { data.set(key, value); } });
    expect(await service.listResources('teams')).toEqual([]);
    const agent = await service.createResource('agents', { name: 'Methods reviewer', role: 'Review methods', runtimeId: 'selected-runtime' });
    expect(agent.status).toBe('offline');
    expect(agent.runtimeId).toBe('selected-runtime');
    expect(await service.readResource('agents', agent.id)).toEqual(agent);
    const autopilot = await service.createResource('autopilots', { name: 'Weekly review', instructions: 'Review new evidence', schedule: 'Weekly', agentId: null, outputMode: 'run', projectId: 'selected-project' });
    expect(autopilot.status).toBe('draft');
    expect(autopilot.outputMode).toBe('run');
    expect(autopilot.projectId).toBe('selected-project');
    const team = await service.createResource('teams', { name: 'Review team', description: '', agentIds: [agent.id], leaderAgentId: agent.id });
    expect(team.leaderAgentId).toBe(agent.id);
    await expect(service.createResource('machines', { name: 'Computer', platform: 'macOS' })).rejects.toMatchObject({ status: 503 });
    expect((await service.load()).machines).toEqual([]);
  });
  it('rejects credential-bearing or malformed API URLs', async () => {
    const { HttpResearchService } = await import('../apps/dashboard/src/services/research');
    expect(() => new HttpResearchService('not a URL')).toThrow('valid');
    expect(() => new HttpResearchService('https://user:secret@example.com')).toThrow();
    expect(() => new HttpResearchService('https://example.com?token=x')).toThrow();
  });
  it('rejects a success response with invalid scientific state', async () => {
    const { HttpResearchService } = await import('../apps/dashboard/src/services/research');
    const previous = globalThis.fetch;
    globalThis.fetch = async () => new Response(JSON.stringify({ id: 'x', stage: 'Confirmed' }), { status: 200 });
    try { await expect(new HttpResearchService('http://localhost:8080').createInquiry({ title: 'Question', description: '', priority: 'none', projectId: null, assigneeId: null })).rejects.toMatchObject({ status: 502 }); }
    finally { globalThis.fetch = previous; }
  });
  it('keeps HTTP error payloads safe and handles non-JSON success', async () => {
    const { HttpResearchService } = await import('../apps/dashboard/src/services/research');
    const previous = globalThis.fetch;
    try {
      globalThis.fetch = async () => new Response('null', { status: 500 });
      await expect(new HttpResearchService('http://localhost:8080').load()).rejects.toMatchObject({ status: 500 });
      globalThis.fetch = async () => new Response('<html>proxy</html>', { status: 200 });
      await expect(new HttpResearchService('http://localhost:8080').load()).rejects.toMatchObject({ status: 502 });
    } finally { globalThis.fetch = previous; }
  });
});


describe('workspace connection ownership', () => {
  it('recovers from an invalid saved endpoint without discarding it', async () => {
    vi.resetModules();
    const stored = new Map([['lykeion.api-url', 'broken://%']]);
    vi.stubGlobal('localStorage', { getItem: (key: string) => stored.get(key) ?? null, setItem: (key: string, value: string) => stored.set(key, value) });
    try {
      const workspace = await import('../apps/dashboard/src/services/workspace');
      expect(workspace.service.value.mode).toBe('local');
      await workspace.refresh();
      expect(workspace.state.error).toContain('invalid');
      expect(workspace.state.data?.inquiries).toEqual([]);
      expect(stored.get('lykeion.api-url')).toBe('broken://%');
    } finally { vi.unstubAllGlobals(); }
  });
  it('ignores a superseded connection even when its transport ignores abort', async () => {
    vi.resetModules();
    const stored = new Map<string, string>();
    vi.stubGlobal('localStorage', { getItem: (key: string) => stored.get(key) ?? null, setItem: (key: string, value: string) => stored.set(key, value) });
    const snapshot = await new LocalResearchService({ getItem: () => null, setItem: () => {} }).load();
    let finishFirst!: (value: Response) => void;
    vi.stubGlobal('fetch', vi.fn((url: string) => url.startsWith('https://first.example') ? new Promise<Response>(resolve => { finishFirst = resolve; }) : Promise.resolve(new Response(JSON.stringify({ ...snapshot, workspace: { ...snapshot.workspace, name: 'Second' } })))));
    try {
      const workspace = await import('../apps/dashboard/src/services/workspace');
      const first = workspace.connect('https://first.example');
      await workspace.connect('https://second.example');
      finishFirst(new Response(JSON.stringify({ ...snapshot, workspace: { ...snapshot.workspace, name: 'First' } })));
      await first;
      expect(workspace.state.data?.workspace.name).toBe('Second');
      expect(workspace.apiUrl.value).toBe('https://second.example');
      expect(stored.get('lykeion.api-url')).toBe('https://second.example');
    } finally { vi.unstubAllGlobals(); }
  });
});

describe('task auxiliary drafts stay separate from execution', () => {
  it('persists valid parent links and rejects missing parents', async () => {
    const stored = new Map<string, string>();
    const service = new LocalResearchService({ getItem: key => stored.get(key) ?? null, setItem: (key, value) => { stored.set(key, value); } });
    const input = { title: 'Parent research', description: '', priority: 'none' as const, projectId: null, assigneeId: null };
    const parent = await service.createInquiry(input);
    const child = await service.createInquiry({ ...input, title: 'Child inquiry', parentId: parent.id });
    expect((await service.detail(child.id)).inquiry.parentId).toBe(parent.id);
    await expect(service.createInquiry({ ...input, parentId: 'missing' })).rejects.toMatchObject({ status: 404 });
    expect((await service.load()).inquiries).toHaveLength(2);
  });
  it('saves a wakeup draft without a run and rejects attempted activation', async () => {
    const stored = new Map<string, string>();
    const service = new LocalResearchService({ getItem: key => stored.get(key) ?? null, setItem: (key, value) => { stored.set(key, value); } });
    const task = await service.createInquiry({ title: 'Wait for evidence', description: '', priority: 'none', projectId: null, assigneeId: null });
    const wakeup = { id: 'draft', condition: 'reply' as const, agentId: null, instructions: 'Review the response', parameters: {}, status: 'draft' as const, createdAt: new Date().toISOString() };
    await service.updateInquiry(task.id, task.revision, { wakeupDrafts: [wakeup] });
    const detail = await service.detail(task.id);
    expect(detail.inquiry.wakeupDrafts?.[0].status).toBe('draft');
    expect(detail.runs).toBeUndefined();
    await expect(service.updateInquiry(task.id, detail.inquiry.revision, { wakeupDrafts: [{ ...wakeup, status: 'active' } as never] })).rejects.toThrow('Invalid wakeup draft');
    expect((await service.detail(task.id)).inquiry.wakeupDrafts?.[0].status).toBe('draft');
  });
});
