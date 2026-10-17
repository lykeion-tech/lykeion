import { onScopeDispose, ref, watch, type Ref } from 'vue';
import type { QueryClient, QueryKey } from '@tanstack/vue-query';
import { api } from './client';
import { keys } from './keys';
import type { LykEvent, Workspace } from './types';

// The queries an event makes stale, chosen by the event type's prefix.
// A deleted card's event has no card column (the row is gone); its id rides
// in the payload.
export function invalidationsFor(ws: string, e: LykEvent): QueryKey[] {
  const card = e.card_id ?? (typeof e.payload.card_id === 'string' ? e.payload.card_id : null);
  const own = (...make: ((ws: string, id: string) => QueryKey)[]) =>
    card ? make.map((k) => k(ws, card)) : [];
  const researchIds = [e.payload.previous_research_id, e.payload.research_id].filter(
    (id): id is string => typeof id === 'string',
  );
  switch (e.type.split('.')[0]) {
    case 'card':
      return [
        keys.cards(ws),
        keys.inboxAll(ws),
        ...own(keys.card, keys.events),
        keys.researches(ws),
        ...(researchIds.length
          ? researchIds.map((id) => keys.research(ws, id))
          : [keys.researchDetails(ws)]),
      ];
    case 'research':
      return [
        keys.researches(ws),
        ...(typeof e.payload.research_id === 'string'
          ? [keys.research(ws, e.payload.research_id)]
          : []),
      ];
    case 'agent':
      return [
        keys.agents(ws),
        ...(typeof e.payload.agent_id === 'string' ? [keys.agent(ws, e.payload.agent_id)] : []),
      ];
    case 'record':
      return own(keys.recordsAll);
    case 'comment':
      return own(keys.comments);
    case 'agent_run':
      return [keys.cards(ws), keys.runtimes(ws), ...own(keys.runs, keys.card)];
    case 'inbox':
      return [keys.inboxAll(ws)];
    case 'runtime':
      return [keys.runtimes(ws)];
    case 'member':
      return [keys.members(ws), keys.workspaces, keys.agents(ws), ['ws', ws, 'agent']];
    case 'invitation':
      return [keys.invitations(ws), keys.members(ws)];
    case 'workspace':
      return [keys.workspace(ws), keys.workspaces];
    case 'pack':
    case 'team':
      return [keys.packs(ws)];
    default:
      return [];
  }
}

export interface Socket {
  send(data: string): void;
  close(): void;
  onopen: ((e: unknown) => void) | null;
  onmessage: ((e: { data: unknown }) => void) | null;
  onclose: ((e: unknown) => void) | null;
}

function browserSocket(): Socket {
  const scheme = location.protocol === 'https:' ? 'wss' : 'ws';
  return new WebSocket(`${scheme}://${location.host}/ws`) as unknown as Socket;
}

const workspaceCursor = async (ws: string) =>
  (await api<Workspace>('GET', `/api/workspaces/${ws}`)).event_cursor;

// Keeps one socket open for the shown workspace and turns its events into
// query invalidations. A workspace's first socket starts at its current event
// cursor. A dropped socket reconnects with backoff, resumes from the last event
// it saw and refreshes the whole workspace, since events may have been missed.
// A new workspace closes the old socket and drops the old workspace's cache.
export function useRealtimeSync(
  workspace: Ref<string>,
  client: QueryClient,
  open: () => Socket = browserSocket,
  startCursor: (ws: string) => Promise<number> = workspaceCursor,
) {
  const connected = ref(false);
  const reconnecting = ref(false);
  const lost = ref(false);
  let socket: Socket | null = null;
  let cursor = 0;
  let opened = false;
  let attempts = 0;
  let generation = 0;
  let retry: ReturnType<typeof setTimeout> | undefined;
  let flush: ReturnType<typeof setTimeout> | undefined;
  const pending = new Map<string, QueryKey>();

  async function refresh(list: QueryKey[], current: () => boolean) {
    // Invalidating a first load reuses its pending request. Cancel it first,
    // even when its transport ignores abort, so a late snapshot cannot win.
    await Promise.all(list.map((queryKey) => client.cancelQueries({ queryKey })));
    if (!current()) return;
    for (const queryKey of list) void client.invalidateQueries({ queryKey });
  }

  function schedule(list: QueryKey[], current: () => boolean) {
    if (!list.length) return;
    for (const k of list) pending.set(JSON.stringify(k), k);
    clearTimeout(flush);
    flush = setTimeout(() => {
      flush = undefined;
      const list = [...pending.values()];
      pending.clear();
      if (current()) void refresh(list, current);
    }, 100);
  }

  function connect(ws: string) {
    const s = open();
    socket = s;
    const ownGeneration = generation;
    const current = () => generation === ownGeneration && socket === s && !lost.value;
    s.onopen = () => {
      if (!current()) return;
      attempts = 0;
      connected.value = true;
      reconnecting.value = false;
      s.send(JSON.stringify({ type: 'subscribe', workspace: ws, cursor }));
      void refresh([keys.ws(ws), keys.workspaces], current);
      opened = true;
    };
    s.onmessage = (m) => {
      if (!current()) return;
      let msg: { type?: string; event?: LykEvent };
      try {
        msg = JSON.parse(String(m.data));
      } catch {
        return;
      }
      if (!msg || typeof msg !== 'object') return;
      if (msg.type === 'event' && msg.event) {
        cursor = Math.max(cursor, msg.event.id);
        schedule(invalidationsFor(ws, msg.event), current);
      } else if (msg.type === 'error') {
        lost.value = true;
        stop();
      }
    };
    s.onclose = () => {
      if (!current()) return;
      socket = null;
      connected.value = false;
      reconnecting.value = opened;
      retry = setTimeout(() => connect(ws), Math.min(10_000, 500 * 2 ** attempts++));
    };
  }

  function stop() {
    clearTimeout(retry);
    clearTimeout(flush);
    flush = undefined;
    pending.clear();
    connected.value = false;
    reconnecting.value = false;
    const s = socket;
    socket = null;
    s?.close();
  }

  watch(
    workspace,
    (ws, old) => {
      stop();
      if (old) client.removeQueries({ queryKey: keys.ws(old) });
      cursor = 0;
      opened = false;
      attempts = 0;
      connected.value = reconnecting.value = lost.value = false;
      const current = ++generation;
      if (!ws) return;
      // A workspace the server will not read falls back to 0; its socket is
      // then refused and reported as lost.
      void startCursor(ws)
        .catch(() => 0)
        .then((start) => {
          if (current !== generation) return;
          cursor = start;
          connect(ws);
        });
    },
    { immediate: true, flush: 'sync' },
  );
  onScopeDispose(() => {
    generation++;
    stop();
  });
  return { connected, reconnecting, lost };
}
