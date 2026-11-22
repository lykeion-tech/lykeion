import { onScopeDispose, ref, watch, type Ref } from 'vue';
import type { QueryClient, QueryKey } from '@tanstack/vue-query';
import { verifying } from './auth-loss';
import { api } from './client';
import { keys } from './keys';
import type { LykEvent, Workspace } from './types';

// The queries an event makes stale, chosen by the event type's prefix.
// A deleted card's event has no card column (the row is gone); its id rides
// in the payload.
export function invalidationsFor(ws: string, e: LykEvent): QueryKey[] {
  if (e.workspace_id !== ws) return [];
  const keysFor = queriesFor(ws, e);
  // The Lab Chart shows runs, agents, machines, decisions and people.
  return /^(agent_run|agent|runtime|laboratory|decision|inbox|member)\./.test(e.type) &&
    e.type !== 'agent_run.usage_updated' &&
    e.type !== 'agent_run.settled'
    ? [...keysFor, keys.labChart(ws)]
    : keysFor;
}

function queriesFor(ws: string, e: LykEvent): QueryKey[] {
  const card = e.card_id ?? (typeof e.payload.card_id === 'string' ? e.payload.card_id : null);
  const own = (...make: ((ws: string, id: string) => QueryKey)[]) =>
    card ? make.map((k) => k(ws, card)) : [];
  const researchIds = [e.payload.previous_research_id, e.payload.research_id].filter(
    (id): id is string => typeof id === 'string',
  );
  const parent = typeof e.payload.autoresearch_id === 'string' ? e.payload.autoresearch_id : '';
  switch (e.type.split('.')[0]) {
    case 'lab':
      return [['ws', ws, 'lab']];
    case 'coresearch': {
      const project = e.payload.project_id;
      if (typeof project !== 'string') return [];
      // Events carry identifiers only; all data is read through membership routes.
      // Document, comment and build events are frequent and touch one file or
      // the build list, so they leave every other viewer's queries alone.
      const file = e.payload.file_id;
      const cowrite = ['ws', ws, 'coresearch', project, 'cowrite'];
      if (e.type === 'coresearch.doc.stepped' && typeof file === 'string')
        return [
          keys.cowriteDoc(ws, project, file),
          keys.cowriteChanges(ws, project, file),
          keys.cowriteComments(ws, project, file),
          [...cowrite, 'doc-steps', file],
        ];
      if (e.type === 'coresearch.comment.changed' && typeof file === 'string')
        return [keys.cowriteComments(ws, project, file)];
      if (e.type === 'coresearch.build.changed')
        return [keys.cowriteBuilds(ws, project), keys.cowriteVersions(ws, project)];
      return [
        keys.coresearchProjects(ws),
        keys.coresearchConversations(ws),
        ['ws', ws, 'coresearch', project],
      ];
    }
    case 'skill':
      return [
        keys.skills(ws),
        ...(typeof e.payload.skill_id === 'string' ? [keys.skill(ws, e.payload.skill_id)] : []),
      ];
    case 'slack':
      return [keys.slack(ws)];
    case 'connector':
      return e.payload.origin === 'custom'
        ? [
            keys.workspaceMCP(ws),
            ...(typeof e.payload.connector_id === 'string'
              ? [keys.workspaceMCPItem(ws, e.payload.connector_id)]
              : []),
          ]
        : [keys.connectors(ws)];
    case 'runtime_skill_request':
      return typeof e.payload.runtime_id === 'string' && typeof e.payload.request_id === 'string'
        ? [keys.runtimeSkillRequest(ws, e.payload.runtime_id, e.payload.request_id)]
        : [];
    case 'autoresearch':
    case 'autoresearch_trigger':
      return [keys.autoresearches(ws), ...(parent ? [keys.autoresearch(ws, parent)] : [])];
    case 'autoresearch_run':
      return [
        keys.autoresearches(ws),
        ...(parent ? [keys.autoresearch(ws, parent), keys.autoresearchRuns(ws, parent)] : []),
        ...(typeof e.payload.run_id === 'string'
          ? [keys.autoresearchRun(ws, e.payload.run_id)]
          : []),
      ];
    case 'card':
      return [
        keys.cards(ws),
        keys.analytics(ws),
        keys.decisionsAll(ws),
        keys.decisionCount(ws),
        ...own(keys.card, keys.events),
        keys.researches(ws),
        ...(researchIds.length
          ? researchIds.map((id) => keys.research(ws, id))
          : [keys.researchDetails(ws)]),
      ];
    case 'research':
      return [
        keys.researches(ws),
        ...(e.type === 'research.deleted' ? [keys.cards(ws), ['ws', ws, 'card']] : []),
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
    case 'review':
      return [...own(keys.events, keys.card, keys.recordsAll)];
    case 'study':
      if (e.type === 'study.run_reactions_changed' && card && typeof e.payload.run_id === 'string')
        return [['ws', ws, 'run-reactions', card, e.payload.run_id]];
      if (e.type === 'study.reactions_changed' && card)
        return [['ws', ws, 'study-reactions', card]];
      return own(keys.card);
    case 'comment':
      return own(keys.comments);
    case 'agent_run':
      return [
        keys.cards(ws),
        keys.runtimes(ws),
        keys.analytics(ws),
        ...own(keys.runs, keys.card),
        ...(card && e.type !== 'agent_run.usage_updated' ? [keys.agentWorkAll(ws)] : []),
      ];
    case 'inbox':
    case 'decision':
      return [keys.decisionsAll(ws), keys.decisionCount(ws)];
    case 'runtime':
      // A machine going offline or being flagged changes which studies wait
      // on it; any study's runs may name it.
      return [keys.runtimes(ws), keys.storage(ws), keys.cards(ws), ['ws', ws, 'card']];
    case 'member':
      return [
        keys.decisionsAll(ws),
        keys.decisionCount(ws),
        keys.coresearchProjects(ws),
        keys.coresearchConversations(ws),
        ['ws', ws, 'coresearch'],
        keys.members(ws),
        keys.workspaces,
        keys.agents(ws),
        ['ws', ws, 'agent'],
        keys.skills(ws),
        ['ws', ws, 'skill'],
        keys.workspaceMCP(ws),
        ['ws', ws, 'mcp-server'],
      ];
    case 'invitation':
      return [keys.invitations(ws), keys.members(ws)];
    case 'workspace':
      return [keys.workspace(ws), keys.workspaces];
    case 'pack':
      return [keys.packs(ws)];
    case 'team':
      return [
        keys.packs(ws),
        keys.agents(ws),
        ...(e.type === 'team.deleted' ? [['ws', ws, 'agent']] : []),
      ];
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
    // A loaded query is left alone: invalidating it restarts its request,
    // and whoever awaits that request then waits for the restarted one.
    await Promise.all(
      list.map((queryKey) =>
        client.cancelQueries({ queryKey, predicate: (query) => query.state.data === undefined }),
      ),
    );
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
      if (msg.type === 'event' && msg.event && msg.event.workspace_id === ws) {
        cursor = Math.max(cursor, msg.event.id);
        const refreshKeys = invalidationsFor(ws, msg.event);
        // Lab answers are live projections. Changes to their source records
        // must recheck every loaded page, even when no chat message changed.
        const lab = ['ws', ws, 'lab'];
        const source = /^(card|study|record|comment|inbox|decision|lesson|workspace|member|team|pack|stage_addon|attachment)\./;
        if (source.test(msg.event.type) && client.getQueryCache().findAll({queryKey: lab, type: 'active'}).length)
          refreshKeys.push(lab);
        schedule(refreshKeys, current);
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
    [workspace, verifying],
    ([ws, paused], [old]) => {
      stop();
      if (old && old !== ws) client.removeQueries({ queryKey: keys.ws(old) });
      cursor = 0;
      opened = false;
      attempts = 0;
      connected.value = reconnecting.value = lost.value = false;
      const current = ++generation;
      if (!ws || paused) return;
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
