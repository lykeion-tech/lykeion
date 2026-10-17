import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest';
import { effectScope, ref, type EffectScope } from 'vue';
import { QueryClient, QueryObserver } from '@tanstack/vue-query';
import { invalidationsFor, useRealtimeSync, type Socket } from '../apps/dashboard/src/api/realtime';
import { keys } from '../apps/dashboard/src/api/keys';
import type { LykEvent } from '../apps/dashboard/src/api/types';

const ev = (type: string, card: string | null = 'c1', id = 1): LykEvent => ({
  id,
  workspace_id: 'w1',
  card_id: card,
  type,
  actor_type: 'system',
  actor_id: null,
  payload: {},
  created_at: '2026-10-15T10:00:00Z',
});
const has = (type: string, key: readonly unknown[], card: string | null = 'c1') =>
  expect(invalidationsFor('w1', ev(type, card))).toContainEqual(key);

describe('event to query map', () => {
  it('card events refresh the board, the card, its event lines and the Inbox', () => {
    for (const type of ['card.created', 'card.steered', 'card.escalated', 'card.deleted']) {
      has(type, keys.cards('w1'));
      has(type, keys.card('w1', 'c1'));
      has(type, keys.events('w1', 'c1'));
      has(type, keys.inboxAll('w1'));
    }
  });
  it('reopening, revising or moving a study to another Team version refreshes it', () => {
    for (const type of ['card.reopened', 'card.revised', 'card.pack_moved']) {
      has(type, keys.card('w1', 'c1'));
      has(type, keys.cards('w1'));
    }
  });
  it('an uploaded or approved Team refreshes the Teams list', () => {
    for (const type of ['pack.uploaded', 'pack.approved']) has(type, ['ws', 'w1', 'packs'], null);
  });
  it('a deleted card, named only in the payload, still refreshes its own queries', () => {
    const deleted = { ...ev('card.deleted', null), payload: { card_id: 'c9' } };
    expect(invalidationsFor('w1', deleted)).toContainEqual(keys.card('w1', 'c9'));
    expect(invalidationsFor('w1', deleted)).toContainEqual(keys.cards('w1'));
  });
  it("a new record refreshes that card's records", () => {
    has('record.created', keys.recordsAll('w1', 'c1'));
  });
  it("a new comment refreshes that card's comments", () => {
    has('comment.created', keys.comments('w1', 'c1'));
  });
  it("agent-run events refresh the card's runs, the card and the board", () => {
    for (const type of [
      'agent_run.queued',
      'agent_run.dispatched',
      'agent_run.started',
      'agent_run.completed',
      'agent_run.cancelled',
      'agent_run.failed',
    ]) {
      has(type, keys.runtimes('w1'));
      has(type, keys.runs('w1', 'c1'));
      has(type, keys.card('w1', 'c1'));
      has(type, keys.cards('w1'));
    }
  });
  it('inbox events refresh both Inbox lists', () => {
    has('inbox.read', keys.inboxAll('w1'), null);
    has('inbox.resolved', keys.inboxAll('w1'), null);
  });
  it('runtime events refresh the runtimes', () => {
    has('runtime.online', keys.runtimes('w1'), null);
    has('runtime.updated', keys.runtimes('w1'), null);
  });
  it('member events refresh the members and the workspace list', () => {
    has('member.role_changed', keys.members('w1'), null);
    has('member.removed', keys.workspaces, null);
  });
  it('invitation events refresh the invitations and the members', () => {
    has('invitation.created', keys.invitations('w1'), null);
    has('invitation.accepted', keys.members('w1'), null);
  });
  it('workspace events refresh the workspace and the list', () => {
    has('workspace.updated', keys.workspace('w1'), null);
    has('workspace.renamed', keys.workspaces, null);
  });
  it('blob events and unknown events refresh nothing', () => {
    expect(invalidationsFor('w1', ev('blob.created'))).toEqual([]);
    expect(invalidationsFor('w1', ev('something.else'))).toEqual([]);
  });
});

class FakeSocket implements Socket {
  sent: string[] = [];
  closed = false;
  onopen: ((e: unknown) => void) | null = null;
  onmessage: ((e: { data: unknown }) => void) | null = null;
  onclose: ((e: unknown) => void) | null = null;
  send(data: string) {
    this.sent.push(data);
  }
  close() {
    this.closed = true;
    this.onclose?.({});
  }
  event(e: LykEvent) {
    this.onmessage?.({ data: JSON.stringify({ type: 'event', event: e }) });
  }
}

describe('useRealtimeSync', () => {
  let sockets: FakeSocket[];
  let client: QueryClient;
  let invalidate: MockInstance<QueryClient['invalidateQueries']>;
  const workspace = ref('w1');
  let scope: EffectScope;
  let sync: ReturnType<typeof useRealtimeSync>;
  // The start cursor arrives asynchronously; let its promise settle.
  const settle = () => vi.advanceTimersByTimeAsync(0);
  beforeEach(async () => {
    vi.useFakeTimers();
    sockets = [];
    scope = effectScope();
    client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
    invalidate = vi.spyOn(client, 'invalidateQueries');
    workspace.value = 'w1';
    sync = scope.run(() =>
      useRealtimeSync(
        workspace,
        client,
        () => {
          const s = new FakeSocket();
          sockets.push(s);
          return s;
        },
        async (ws) => (ws === 'w1' ? 40 : 7),
      ),
    )!;
    await settle();
  });
  afterEach(() => {
    scope.stop();
    client.clear();
    vi.useRealTimers();
  });
  const keysInvalidated = () =>
    invalidate.mock.calls.map((c) => (c[0] as { queryKey: unknown }).queryKey);

  it("subscribes from the workspace's event cursor, then debounces invalidations", async () => {
    sockets[0]!.onopen!({});
    expect(JSON.parse(sockets[0]!.sent[0]!)).toEqual({
      type: 'subscribe',
      workspace: 'w1',
      cursor: 40,
    });
    expect(sync.connected.value).toBe(true);
    await settle();
    invalidate.mockClear();
    sockets[0]!.event(ev('comment.created', 'c1', 7));
    sockets[0]!.event(ev('comment.created', 'c1', 8));
    expect(invalidate).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(100);
    expect(keysInvalidated()).toEqual([keys.comments('w1', 'c1')]);
  });

  it('resumes from the last event after a drop and refreshes everything of the workspace', async () => {
    sockets[0]!.onopen!({});
    sockets[0]!.event(ev('card.created', 'c1', 41));
    sockets[0]!.onclose!({});
    expect(sync.reconnecting.value).toBe(true);
    vi.advanceTimersByTime(10_000);
    expect(sockets).toHaveLength(2);
    sockets[1]!.onopen!({});
    expect(JSON.parse(sockets[1]!.sent[0]!).cursor).toBe(41);
    expect(sync.reconnecting.value).toBe(false);
    await settle();
    expect(keysInvalidated()).toContainEqual(keys.ws('w1'));
    expect(keysInvalidated()).toContainEqual(keys.workspaces);
  });

  it('a workspace switch closes the old socket and drops the old cache', async () => {
    client.setQueryData(keys.cards('w1'), []);
    sockets[0]!.onopen!({});
    workspace.value = 'w2';
    expect(sockets[0]!.closed).toBe(true);
    expect(client.getQueryData(keys.cards('w1'))).toBeUndefined();
    await settle();
    expect(sockets).toHaveLength(2);
    sockets[1]!.onopen!({});
    expect(JSON.parse(sockets[1]!.sent[0]!)).toEqual({
      type: 'subscribe',
      workspace: 'w2',
      cursor: 7,
    });
    vi.advanceTimersByTime(60_000);
    expect(sockets).toHaveLength(2);
  });

  it('a refused workspace is reported as lost and not retried', () => {
    sockets[0]!.onopen!({});
    sockets[0]!.onmessage!({ data: '{"type":"error","error":"workspace not found"}' });
    expect(sync.lost.value).toBe(true);
    vi.advanceTimersByTime(60_000);
    expect(sockets).toHaveLength(1);
  });

  it('refreshes on first open without waiting for a ready frame or any event', async () => {
    client.setQueryData(keys.cards('w1'), ['old']);
    sockets[0]!.onopen!({});
    await settle();
    expect(client.getQueryState(keys.cards('w1'))?.isInvalidated).toBe(true);
    expect(keysInvalidated()).toEqual([keys.ws('w1'), keys.workspaces]);
  });

  it.each([{ queryKey: keys.cards('w1') }, { queryKey: keys.workspaces }])(
    'cancels a pending pre-cursor snapshot for $queryKey before refreshing',
    async ({ queryKey }) => {
      const stale = deferred<string[]>();
      const fresh = deferred<string[]>();
      let requests = 0;
      const observer = new QueryObserver(client, {
        queryKey,
        queryFn: () => (++requests === 1 ? stale.promise : fresh.promise),
      });
      const unsubscribe = observer.subscribe(() => {});
      scope.stop();
      scope = effectScope();
      sockets = [];
      const cursor = deferred<number>();
      scope.run(() =>
        useRealtimeSync(
          workspace,
          client,
          () => {
            const socket = new FakeSocket();
            sockets.push(socket);
            return socket;
          },
          () => {
            expect(requests).toBe(1);
            return cursor.promise;
          },
        ),
      );
      try {
        expect(requests).toBe(1);
        expect(client.getQueryData(queryKey)).toBeUndefined();
        cursor.resolve(40);
        await settle();
        // This request predates the cursor read and is still unresolved on open.
        sockets[0]!.onopen!({});
        await settle();
        expect(requests).toBe(2);
        fresh.resolve(['committed before cursor 40']);
        await settle();
        expect(client.getQueryData(queryKey)).toEqual(['committed before cursor 40']);
        stale.resolve(['snapshot from before cursor 40']);
        await settle();
        expect(client.getQueryData(queryKey)).toEqual(['committed before cursor 40']);
      } finally {
        unsubscribe();
      }
    },
  );

  it('refreshes every workspace resource and the list after reconnect without replay', async () => {
    const scoped = [
      keys.workspace('w1'),
      keys.cards('w1'),
      keys.card('w1', 'c1'),
      keys.records('w1', 'c1', 'grounding'),
      keys.comments('w1', 'c1'),
      keys.events('w1', 'c1'),
      keys.runs('w1', 'c1'),
      keys.runMessages('w1', 'r1', 1),
      keys.inbox('w1', false),
      keys.inbox('w1', true),
      keys.runtimes('w1'),
      keys.members('w1'),
      keys.invitations('w1'),
      keys.workspaces,
    ];
    sockets[0]!.onopen!({});
    await settle();
    scoped.forEach((key) => client.setQueryData(key, ['old']));
    client.setQueryData(keys.cards('w2'), ['other']);
    invalidate.mockClear();
    sockets[0]!.onclose!({});
    await vi.advanceTimersByTimeAsync(500);
    sockets[1]!.onopen!({});
    await settle();
    expect(keysInvalidated()).toEqual([keys.ws('w1'), keys.workspaces]);
    scoped.forEach((key) => expect(client.getQueryState(key)?.isInvalidated).toBe(true));
    expect(client.getQueryState(keys.cards('w2'))?.isInvalidated).toBe(false);
  });

  it('waits 100 ms after the last mapped event before flushing each key once', async () => {
    sockets[0]!.onopen!({});
    await settle();
    invalidate.mockClear();
    sockets[0]!.event(ev('comment.created', 'c1', 41));
    await vi.advanceTimersByTimeAsync(70);
    sockets[0]!.event(ev('comment.created', 'c1', 42));
    await vi.advanceTimersByTimeAsync(30);
    expect(invalidate).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(69);
    expect(invalidate).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(keysInvalidated()).toEqual([keys.comments('w1', 'c1')]);
  });

  it('does not postpone a mapped refresh when repeated unmapped events arrive', async () => {
    sockets[0]!.onopen!({});
    await settle();
    invalidate.mockClear();
    sockets[0]!.event(ev('comment.created', 'c1', 41));
    await vi.advanceTimersByTimeAsync(30);
    sockets[0]!.event(ev('blob.created', null, 42));
    await vi.advanceTimersByTimeAsync(30);
    sockets[0]!.event(ev('something.else', null, 43));
    await vi.advanceTimersByTimeAsync(30);
    sockets[0]!.event(ev('blob.created', null, 44));
    await vi.advanceTimersByTimeAsync(9);
    expect(invalidate).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(keysInvalidated()).toEqual([keys.comments('w1', 'c1')]);
    sockets[0]!.onclose!({});
    await vi.advanceTimersByTimeAsync(500);
    sockets[1]!.onopen!({});
    expect(JSON.parse(sockets[1]!.sent[0]!).cursor).toBe(44);
  });

  it('ignores obsolete socket callbacks after a workspace switch', async () => {
    sockets[0]!.onopen!({});
    await settle();
    const old = sockets[0]!;
    workspace.value = 'w2';
    await settle();
    sockets[1]!.onopen!({});
    await settle();
    invalidate.mockClear();
    old.onopen!({});
    old.event(ev('comment.created', 'c1', 900));
    old.onmessage!({ data: '{"type":"error","error":"workspace not found"}' });
    old.onclose!({});
    await vi.advanceTimersByTimeAsync(100);
    expect(sync.connected.value).toBe(true);
    expect(sync.lost.value).toBe(false);
    expect(invalidate).not.toHaveBeenCalled();
    expect(client.getQueryData(keys.comments('w1', 'c1'))).toBeUndefined();
    sockets[1]!.onclose!({});
    await vi.advanceTimersByTimeAsync(500);
    sockets[2]!.onopen!({});
    expect(JSON.parse(sockets[2]!.sent[0]!).cursor).toBe(7);
  });

  it('ignores callbacks from a dropped socket after reconnecting in the same workspace', async () => {
    sockets[0]!.onopen!({});
    sockets[0]!.onclose!({});
    await vi.advanceTimersByTimeAsync(500);
    sockets[1]!.onopen!({});
    await settle();
    invalidate.mockClear();
    sockets[0]!.event(ev('comment.created', 'c1', 900));
    sockets[0]!.onmessage!({ data: '{"type":"error","error":"workspace not found"}' });
    await vi.advanceTimersByTimeAsync(100);
    expect(invalidate).not.toHaveBeenCalled();
    expect(sync.lost.value).toBe(false);
    sockets[1]!.onclose!({});
    await vi.advanceTimersByTimeAsync(500);
    sockets[2]!.onopen!({});
    expect(JSON.parse(sockets[2]!.sent[0]!).cursor).toBe(40);
  });

  it('drops pending event invalidations when the scope is disposed', async () => {
    sockets[0]!.onopen!({});
    await settle();
    invalidate.mockClear();
    sockets[0]!.event(ev('comment.created', 'c1', 41));
    scope.stop();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(invalidate).not.toHaveBeenCalled();
    expect(sockets[0]!.closed).toBe(true);
    expect(sockets).toHaveLength(1);
    expect(sync.connected.value).toBe(false);
  });

  it('does not finish an old socket refresh after a workspace switch', async () => {
    const cancelled = deferred<void>();
    const cancel = client.cancelQueries.bind(client);
    vi.spyOn(client, 'cancelQueries').mockImplementation(async (filters) => {
      await cancel(filters);
      await cancelled.promise;
    });
    sockets[0]!.onopen!({});
    workspace.value = 'w2';
    await settle();
    cancelled.resolve();
    await settle();
    expect(invalidate).not.toHaveBeenCalled();
  });

  it('ignores malformed and non-event envelopes', async () => {
    sockets[0]!.onopen!({});
    await settle();
    invalidate.mockClear();
    for (const data of ['{', 'null', '[]', '{"type":"ready"}', '{"type":"event"}']) {
      sockets[0]!.onmessage!({ data });
    }
    await vi.advanceTimersByTimeAsync(100);
    expect(invalidate).not.toHaveBeenCalled();
    expect(sync.connected.value).toBe(true);
  });
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe('delayed cursor reads', () => {
  it('ignores a late cursor after switching and after disposal', async () => {
    const scope = effectScope();
    const workspace = ref('w1');
    const first = deferred<number>();
    const second = deferred<number>();
    const third = deferred<number>();
    const sockets: FakeSocket[] = [];
    const client = new QueryClient();
    try {
      scope.run(() =>
        useRealtimeSync(
          workspace,
          client,
          () => {
            const socket = new FakeSocket();
            sockets.push(socket);
            return socket;
          },
          (ws) => (ws === 'w1' ? first.promise : ws === 'w2' ? second.promise : third.promise),
        ),
      );
      workspace.value = 'w2';
      second.resolve(7);
      await vi.waitFor(() => expect(sockets).toHaveLength(1));
      first.resolve(900);
      await Promise.resolve();
      await Promise.resolve();
      sockets[0]!.onopen!({});
      expect(JSON.parse(sockets[0]!.sent[0]!)).toEqual({
        type: 'subscribe',
        workspace: 'w2',
        cursor: 7,
      });
      workspace.value = 'w3';
      scope.stop();
      third.resolve(13);
      await Promise.resolve();
      await Promise.resolve();
      expect(sockets).toHaveLength(1);
    } finally {
      scope.stop();
      client.clear();
    }
  });
});
