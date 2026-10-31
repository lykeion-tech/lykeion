import { describe, expect, it, vi } from 'vitest';
import { effectScope } from 'vue';
import { clampPane, usePaneResize } from '../apps/dashboard/src/lib/resize';
import { runDuration, runTimeline } from '../apps/dashboard/src/lib/run-timeline';
import { invalidationsFor } from '../apps/dashboard/src/api/realtime';
import { keys } from '../apps/dashboard/src/api/keys';
import type { AgentRun, LykEvent } from '../apps/dashboard/src/api/types';
const run = (extra: Partial<AgentRun> = {}): AgentRun => ({
  id: 'r',
  workspace_id: 'w',
  card_id: 'c',
  responsible_user_id: 'u',
  board_column: 'grounding',
  round: 1,
  role: 'scoper',
  harness: 'claude',
  status: 'completed',
  queue_reason: null,
  runtime_id: null,
  dispatched_at: null,
  started_at: '2026-10-15T10:00:00Z',
  completed_at: '2026-10-15T10:01:00Z',
  attempt: 1,
  max_attempts: 3,
  model: '',
  error: '',
  failure_reason: null,
  cost_usd: 0.3,
  created_at: '2026-10-15T09:00:00Z',
  handoff_note: '',
  agent_id: null,
  agent_name: null,
  assignment: '',
  latest_text: '',
  last_activity_at: null,
  ...extra,
});
describe('recorded execution time and costs', () => {
  it('queued hours are excluded from agent time; retried durations stay partial', () => {
    const row = run({ attempt: 2 });
    const t = runTimeline([row], Date.now());
    expect(t.agentMs).toBe(60000);
    expect(t.partial).toBe(true);
    expect(t.total).toBe(0.3);
    expect(
      runDuration(run({ status: 'queued', started_at: null, completed_at: null }), Date.now()),
    ).toBeNull();
  });
  it('a missing latest-attempt timestamp leaves aggregate agent time unknown', () =>
    expect(runTimeline([run({ started_at: null })], Date.now()).agentMs).toBeNull());
  it('unknown completion never becomes a fabricated duration', () =>
    expect(runDuration(run({ completed_at: null }), Date.now())).toBeNull());
  it('steps costs by completion, preserving deterministic equal-time ids', () => {
    const t = runTimeline(
      [run({ id: 'b', cost_usd: 0.2 }), run({ id: 'a', cost_usd: 0.1 })],
      Date.now(),
    );
    expect(t.runs.map((r) => r.id)).toEqual(['a', 'b']);
    expect(t.total).toBeCloseTo(0.3);
    expect(t.path).toContain('L1000,5');
  });
});
describe('resize boundaries', () => {
  it('bounds both desktop and constrained viewport widths without NaN', () => {
    expect(clampPane(600, 240, 480)).toBe(480);
    expect(clampPane(200, 240, 480, 170)).toBe(170);
    expect(clampPane(NaN, 240, 480)).toBe(240);
  });
  it('ignores nonprimary pointers, rolls cancelled previews back and leaves cleanup attached only during drag', () => {
    const listeners = new Map<string, (e: PointerEvent) => void>();
    vi.stubGlobal('window', {
      innerWidth: 1500,
      addEventListener: (n: string, f: (e: PointerEvent) => void) => listeners.set(n, f),
      removeEventListener: (n: string) => listeners.delete(n),
    });
    vi.stubGlobal('document', { body: { style: { userSelect: '' } } });
    vi.stubGlobal('localStorage', { getItem: () => null, setItem: vi.fn() });
    const scope = effectScope();
    const pane = scope.run(() => usePaneResize('width', 224, 200, 360))!;
    pane.pointerdown({
      button: 0,
      isPrimary: false,
      pointerId: 1,
      clientX: 100,
      preventDefault: vi.fn(),
    } as unknown as PointerEvent);
    expect(listeners.size).toBe(0);
    pane.pointerdown({
      button: 0,
      isPrimary: true,
      pointerId: 1,
      clientX: 100,
      preventDefault: vi.fn(),
    } as unknown as PointerEvent);
    listeners.get('pointermove')!({ pointerId: 1, clientX: 160 } as PointerEvent);
    expect(pane.width.value).toBe(284);
    listeners.get('pointercancel')!({ pointerId: 1, type: 'pointercancel' } as PointerEvent);
    expect(pane.width.value).toBe(224);
    expect(listeners.size).toBe(0);
    scope.stop();
    vi.unstubAllGlobals();
  });
});
describe('new event cache fences', () => {
  const ev = (type: string, payload: Record<string, unknown>): LykEvent => ({
    id: 1,
    workspace_id: 'w',
    card_id: 'c',
    type,
    actor_type: 'user',
    actor_id: 'u',
    payload,
    created_at: '',
  });
  it('invalidates both actual previous and current Research detail ids', () => {
    const k = invalidationsFor(
      'w',
      ev('card.properties_updated', { research_id: 'new', previous_research_id: 'old' }),
    );
    expect(k).toContainEqual(keys.research('w', 'old'));
    expect(k).toContainEqual(keys.research('w', 'new'));
    expect(k).toContainEqual(keys.researches('w'));
  });
  it('agent privacy changes refresh definition and collection', () => {
    expect(invalidationsFor('w', ev('agent.updated', { agent_id: 'a' }))).toEqual([
      keys.agents('w'),
      keys.agent('w', 'a'),
    ]);
  });
});

describe('membership changes responsibility access', () => {
  it.each(['member.removed', 'member.role_changed'])(
    '%s refreshes protected agent definitions and options',
    (type) => {
      const event: LykEvent = {
        id: 1,
        workspace_id: 'w',
        card_id: null,
        type,
        actor_type: 'user',
        actor_id: 'u',
        payload: {},
        created_at: '',
      };
      expect(invalidationsFor('w', event)).toContainEqual(keys.agents('w'));
      expect(invalidationsFor('w', event)).toContainEqual(['ws', 'w', 'agent']);
    },
  );
});

describe('embedded Research study cache refresh', () => {
  it.each(['card.edited', 'card.deleted', 'card.started'])(
    '%s invalidates real workspace detail caches without Research ids',
    async (type) => {
      const { QueryClient } = await import('@tanstack/vue-query');
      const client = new QueryClient();
      const first = keys.research('w', 'first'),
        second = keys.research('w', 'second'),
        foreign = keys.research('other', 'first');
      client.setQueryData(first, {
        studies: [{ id: 'c', title: 'Before', board_column: 'grounding' }],
      });
      client.setQueryData(second, { studies: [] });
      client.setQueryData(foreign, { studies: [{ id: 'foreign' }] });
      const event: LykEvent = {
        id: 1,
        workspace_id: 'w',
        card_id: type === 'card.deleted' ? null : 'c',
        type,
        actor_type: 'system',
        actor_id: null,
        payload: type === 'card.deleted' ? { card_id: 'c' } : {},
        created_at: '',
      };
      await Promise.all(
        invalidationsFor('w', event).map((queryKey) => client.invalidateQueries({ queryKey })),
      );
      expect(client.getQueryState(first)?.isInvalidated).toBe(true);
      expect(client.getQueryState(second)?.isInvalidated).toBe(true);
      expect(client.getQueryState(foreign)?.isInvalidated).toBe(false);
      client.clear();
    },
  );
});

describe('computer setup shell arguments', () => {
  it.each(['unix', 'windows'] as const)(
    'quotes an IPv6 origin and strips non-origin input for %s',
    async (platform) => {
      const { computerStartCommands } = await import('../apps/dashboard/src/features/machines/machines');
      const commands = computerStartCommands(
        'http://person:secret@[::1]:8080/path?token=private#fragment',
        platform,
      );
      expect(commands).toContain("login --server 'http://[::1]:8080' --email you@lab.org");
      expect(commands).not.toMatch(/person|secret|private|fragment|path/);
    },
  );
  it('escapes apostrophes for each shell without allowing argument injection', async () => {
    const { computerStartCommands } = await import('../apps/dashboard/src/features/machines/machines');
    expect(computerStartCommands("https://lab'host.example/path", 'unix')).toContain(
      "'https://lab'\"'\"'host.example'",
    );
    expect(computerStartCommands("https://lab'host.example/path", 'windows')).toContain(
      "'https://lab''host.example'",
    );
  });
});

describe('agent connector validation', () => {
  it('agent connector removal returns an unchanged structured list without mutating input', async () => {
    const { removeAgentConnector } = await import('../apps/dashboard/src/lib/agent-connectors');
    const servers = [
      { name: 'lab-search', url: 'https://tools.lab.org/mcp' },
      { name: 'index', url: 'http://index.lab.org/mcp' },
    ];
    const nextServers = removeAgentConnector(servers, 'index');
    expect(nextServers).toEqual([{ name: 'lab-search', url: 'https://tools.lab.org/mcp' }]);
    expect(servers).toHaveLength(2);
    expect(removeAgentConnector(servers, 'missing')).not.toBe(servers);
  });
  it('agent connector validation applies the exact owning name, count and shape rules', async () => {
    const { validateAgentConnectors } = await import('../apps/dashboard/src/lib/agent-connectors');
    const server = (name: string) => ({ name, url: 'https://tools.lab.org/mcp' });
    expect(validateAgentConnectors([])).toBeUndefined();
    expect(validateAgentConnectors([server('a' + 'b'.repeat(27))])).toBeUndefined();
    expect(
      validateAgentConnectors(Array.from({ length: 8 }, (_, n) => server('server-' + n))),
    ).toBeUndefined();
    for (const name of [
      'sources',
      'lyk',
      'lyk-lab',
      '',
      'Upper',
      '1lab',
      'lab space',
      'a' + 'b'.repeat(28),
    ])
      expect(validateAgentConnectors([server(name)]), name).toBeTruthy();
    expect(validateAgentConnectors([server('lab'), server('lab')])).toBeTruthy();
    expect(
      validateAgentConnectors(Array.from({ length: 9 }, (_, n) => server('server-' + n))),
    ).toBeTruthy();
    expect(
      validateAgentConnectors([{ ...server('lab'), headers: {} } as { name: string; url: string }]),
    ).toBeTruthy();
  });
  it('agent connector URLs preserve http(s) and owning parser limits without browser normalization', async () => {
    const { validateAgentConnectors } = await import('../apps/dashboard/src/lib/agent-connectors');
    for (const url of [
      'http://localhost:8080/mcp',
      'http://tools.lab.org/mcp',
      'https://tools.lab.org/mcp',
      'http://[::1]/mcp',
      'http://[fe80::1%25en0]/mcp',
      'http://tools.lab.org:99999/mcp',
      'http://tools.lab.org/%FF',
      'https://tools.lab.org/' + 'a'.repeat(2026),
    ])
      expect(validateAgentConnectors([{ name: 'lab', url }]), url).toBeUndefined();
    for (const url of [
      '',
      'ftp://lab.org',
      'https:lab.org',
      'https:///mcp',
      'https://user:pass@lab.org/mcp',
      'https://lab.org/?',
      'https://lab.org/#',
      'https://lab.org/with space',
      'https://lab.org/é',
      'https://lab.org/\n',
      'https://lab.org/%xx',
      'http://lab.org:no/mcp',
      'http://[invalid]/mcp',
      'https://lab.org/%',
      'https://lab.org/' + 'a'.repeat(2033),
    ])
      expect(validateAgentConnectors([{ name: 'lab', url }]), url).toBeTruthy();
  });
});
