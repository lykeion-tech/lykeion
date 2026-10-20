import { computed, toValue, watch, type MaybeRefOrGetter } from 'vue';
import { useQuery, useQueryClient } from '@tanstack/vue-query';
import { api, ApiError } from './client';
import { keys } from './keys';
import { workspaceId as ws } from './session';
import type {
  AgentDefinition,
  Autoresearch,
  AutoresearchDetail,
  AutoresearchRun,
  AutoresearchRunDetail,
  Research,
  ResearchDetail,
  AgentRun,
  Card,
  Comment,
  InboxItem,
  Invitation,
  LykEvent,
  Me,
  Member,
  StoreTeam,
  TeamDetail,
  RunMessage,
  Runtime,
  StudyRecord,
  Token,
  Workspace,
} from './types';

const inWorkspace = computed(() => Boolean(ws.value));

export const useMe = () =>
  useQuery({ queryKey: keys.me, queryFn: () => api<Me>('GET', '/api/me') });
export const useWorkspaces = () =>
  useQuery({
    queryKey: keys.workspaces,
    queryFn: () => api<Workspace[]>('GET', '/api/workspaces'),
  });
export const useTokens = () =>
  useQuery({ queryKey: keys.tokens, queryFn: () => api<Token[]>('GET', '/api/tokens') });

export const useWorkspace = () =>
  useQuery({
    queryKey: computed(() => keys.workspace(ws.value)),
    queryFn: () => api<Workspace>('GET', `/api/workspaces/${ws.value}`),
    enabled: inWorkspace,
  });

function list<T>(key: (ws: string) => readonly unknown[], path: string) {
  return useQuery({
    queryKey: computed(() => key(ws.value)),
    queryFn: async () => (await api<T[] | null>('GET', path)) ?? [],
    enabled: inWorkspace,
  });
}
export const useCards = () => list<Card>(keys.cards, '/api/cards');
export const useRuntimes = () => list<Runtime>(keys.runtimes, '/api/runtimes');
export const useMembers = () =>
  useQuery({
    queryKey: computed(() => keys.members(ws.value)),
    queryFn: () => api<Member[]>('GET', `/api/workspaces/${ws.value}/members`),
    enabled: inWorkspace,
  });
export const useInvitations = (enabled: MaybeRefOrGetter<boolean>) =>
  useQuery({
    queryKey: computed(() => keys.invitations(ws.value)),
    queryFn: () => api<Invitation[]>('GET', `/api/workspaces/${ws.value}/invitations`),
    enabled: computed(() => inWorkspace.value && toValue(enabled)),
  });
export const useInbox = (archived: MaybeRefOrGetter<boolean>) =>
  useQuery({
    queryKey: computed(() => keys.inbox(ws.value, toValue(archived))),
    queryFn: async () =>
      (await api<InboxItem[] | null>(
        'GET',
        toValue(archived) ? '/api/inbox?archived=1' : '/api/inbox',
      )) ?? [],
    enabled: inWorkspace,
  });

// A card's own reads; a card id of '' disables them.
function ofCard<T>(
  id: MaybeRefOrGetter<string>,
  key: (ws: string, id: string) => readonly unknown[],
  path: (id: string) => string,
) {
  return useQuery({
    queryKey: computed(() => key(ws.value, toValue(id))),
    queryFn: () => api<T>('GET', path(toValue(id))),
    enabled: computed(() => inWorkspace.value && Boolean(toValue(id))),
  });
}
export const useCard = (id: MaybeRefOrGetter<string>) =>
  ofCard<Card>(id, keys.card, (c) => `/api/cards/${c}`);
export const useComments = (id: MaybeRefOrGetter<string>) =>
  ofCard<Comment[]>(id, keys.comments, (c) => `/api/cards/${c}/comments`);
export const useCardEvents = (id: MaybeRefOrGetter<string>) =>
  useQuery({
    queryKey: computed(() => keys.events(ws.value, toValue(id))),
    queryFn: async ({ signal }) => {
      const workspace = ws.value;
      const card = toValue(id);
      const events: LykEvent[] = [];
      let after = 0;
      while (true) {
        const page =
          (await api<LykEvent[] | null>('GET', `/api/cards/${card}/events?after=${after}`)) ?? [];
        // Cancellation fences subsequent pages even when fetch has not aborted.
        if (signal.aborted || ws.value !== workspace || toValue(id) !== card)
          throw new Error('Study history read cancelled');
        events.push(...page);
        if (page.length < 500) return events;
        after = page[page.length - 1]!.id;
      }
    },
    enabled: computed(() => inWorkspace.value && Boolean(toValue(id))),
  });
// A card's runs. Each carries its latest text and last activity, which no
// event announces, so while a run is live the listing polls; this one read
// stands in for every run's message history on the study page.
export const useRuns = (id: MaybeRefOrGetter<string>) =>
  useQuery({
    queryKey: computed(() => keys.runs(ws.value, toValue(id))),
    queryFn: () => api<AgentRun[]>('GET', `/api/cards/${toValue(id)}/runs`),
    enabled: computed(() => inWorkspace.value && Boolean(toValue(id))),
    refetchInterval: (query) =>
      query.state.data?.some((r) => ['dispatched', 'running'].includes(r.status)) ? 5000 : false,
  });

// A column's records, superseded ones included (marked by superseded_at); the
// server answers null when there are none.
export const useRecords = (id: MaybeRefOrGetter<string>, column: MaybeRefOrGetter<string>) =>
  useQuery({
    queryKey: computed(() => keys.records(ws.value, toValue(id), toValue(column))),
    queryFn: async () =>
      (await api<StudyRecord[] | null>(
        'GET',
        `/api/cards/${toValue(id)}/records?all=1&column=${encodeURIComponent(toValue(column))}`,
      )) ?? [],
    enabled: computed(() => inWorkspace.value && Boolean(toValue(id)) && Boolean(toValue(column))),
  });

// One kind's live records from every column of the study.
export const useKindRecords = (id: MaybeRefOrGetter<string>, kind: string) =>
  useQuery({
    queryKey: computed(() => [...keys.recordsAll(ws.value, toValue(id)), 'kind', kind]),
    queryFn: async () =>
      (await api<StudyRecord[] | null>(
        'GET',
        `/api/cards/${toValue(id)}/records?kind=${encodeURIComponent(kind)}`,
      )) ?? [],
    enabled: computed(() => inWorkspace.value && Boolean(toValue(id))),
  });

// One live record of the study, from any column, as a list of none or one; a
// record id of '' disables the read.
export const useCardRecord = (id: MaybeRefOrGetter<string>, recordId: MaybeRefOrGetter<string>) =>
  useQuery({
    queryKey: computed(() => [...keys.recordsAll(ws.value, toValue(id)), 'id', toValue(recordId)]),
    queryFn: async () =>
      (await api<StudyRecord[] | null>(
        'GET',
        `/api/cards/${toValue(id)}/records?id=${encodeURIComponent(toValue(recordId))}`,
      )) ?? [],
    enabled: computed(
      () => inWorkspace.value && Boolean(toValue(id)) && Boolean(toValue(recordId)),
    ),
  });

// A run's messages for one attempt. No event announces a message, so while the
// run is live the query polls; a settled run's messages never change.
export const useRunMessages = (
  run: MaybeRefOrGetter<AgentRun | undefined>,
  open: MaybeRefOrGetter<boolean>,
) => {
  const client = useQueryClient();
  const query = useQuery({
    queryKey: computed(() =>
      keys.runMessages(ws.value, toValue(run)?.id ?? '', toValue(run)?.attempt ?? 0),
    ),
    queryFn: async () => {
      const { id, attempt } = toValue(run)!;
      const messages: RunMessage[] = [];
      let after = 0;
      while (true) {
        const page = await api<RunMessage[]>(
          'GET',
          `/api/agent-runs/${id}/messages?attempt=${attempt}&after=${after}`,
        );
        messages.push(...page);
        if (page.length < 500) return messages;
        after = page[page.length - 1]!.seq;
      }
    },
    enabled: computed(() => {
      const r = toValue(run);
      return inWorkspace.value && toValue(open) && Boolean(r && r.attempt > 0);
    }),
    refetchInterval: computed(() =>
      ['dispatched', 'running'].includes(toValue(run)?.status ?? '') ? 2000 : false,
    ),
  });
  // A terminal transition can arrive before the next message poll. Discard any
  // unfinished live snapshot and read the final output for this same attempt.
  watch(
    () => {
      const r = toValue(run);
      return [ws.value, r?.id, r?.attempt, r?.status] as const;
    },
    async (current, previous, onCleanup) => {
      let active = true;
      onCleanup(() => {
        active = false;
      });
      const [workspace, id, attempt, status] = current;
      if (
        !workspace ||
        !id ||
        !attempt ||
        !toValue(open) ||
        current.slice(0, 3).some((value, i) => value !== previous[i]) ||
        !['dispatched', 'running'].includes(previous[3] ?? '') ||
        !['completed', 'failed', 'cancelled'].includes(status ?? '')
      )
        return;
      await client.cancelQueries({
        queryKey: keys.runMessages(workspace, id, attempt),
        exact: true,
      });
      const r = toValue(run);
      if (
        active &&
        ws.value === workspace &&
        r?.id === id &&
        r.attempt === attempt &&
        r.status === status &&
        toValue(open)
      ) {
        void query.refetch();
      }
    },
  );
  return query;
};

export const useResearches = () => list<Research>(keys.researches, '/api/researches');
export const useResearch = (id: MaybeRefOrGetter<string>) =>
  ofCard<ResearchDetail>(id, keys.research, (r) => `/api/researches/${r}`);
export const useAutoresearches = () => list<Autoresearch>(keys.autoresearches, '/api/autoresearch');
export const useAutoresearch = (id: MaybeRefOrGetter<string>) =>
  ofCard<AutoresearchDetail>(id, keys.autoresearch, (a) => `/api/autoresearch/${a}`);
export const useAutoresearchRuns = (id: MaybeRefOrGetter<string>) =>
  ofCard<AutoresearchRun[]>(id, keys.autoresearchRuns, (a) => `/api/autoresearch/${a}/runs`);
export const useAgents = () => list<AgentDefinition>(keys.agents, '/api/agents');
export function useAgent(id: MaybeRefOrGetter<string>) {
  const client = useQueryClient();
  return useQuery({
    queryKey: computed(() => keys.agent(ws.value, toValue(id))),
    queryFn: async () => {
      const workspace = ws.value;
      const agent = toValue(id);
      try {
        return await api<AgentDefinition>('GET', `/api/agents/${agent}`);
      } catch (error) {
        if (error instanceof ApiError && [403, 404].includes(error.status)) {
          client.setQueryData(keys.agent(workspace, agent), null);
          client.setQueryData<AgentDefinition[]>(keys.agents(workspace), (old) =>
            old?.filter((a) => a.id !== agent),
          );
        }
        throw error;
      }
    },
    enabled: computed(() => inWorkspace.value && Boolean(toValue(id))),
    retry: false,
  });
}

// The store: shipped Teams and the caller's own, newest version of each name.
export const useTeams = (domain: MaybeRefOrGetter<string>, stage: MaybeRefOrGetter<string>) =>
  useQuery({
    queryKey: computed(() => keys.teams(ws.value, toValue(domain), toValue(stage))),
    queryFn: async () => {
      const q = new URLSearchParams({ domain: toValue(domain), stage: toValue(stage) });
      return (await api<StoreTeam[] | null>('GET', `/api/teams?${q}`)) ?? [];
    },
    enabled: inWorkspace,
  });

export const useTeam = (id: MaybeRefOrGetter<string>) =>
  useQuery({
    queryKey: computed(() => keys.team(ws.value, toValue(id))),
    queryFn: () => api<TeamDetail>('GET', `/api/teams/${toValue(id)}`),
    enabled: computed(() => inWorkspace.value && !!toValue(id)),
  });

// Compatibility names share exactly the same library query contracts.
export {
  useSkillLibrary,
  useSkillLibrary as useSkills,
  useSkillDetail,
  useSkillFile,
  useConnectorLibrary,
  useConnectorLibrary as useConnectors,
  useWorkspaceMCPServers,
  useRuntimeSkillRequest,
  useLibraryMutations,
} from './library';

export const useAutoresearchRun = (id: MaybeRefOrGetter<string>) =>
  useQuery({
    queryKey: computed(() => keys.autoresearchRun(ws.value, toValue(id))),
    queryFn: ({ queryKey, signal }) =>
      api<AutoresearchRunDetail>(
        'GET',
        `/api/autoresearch-runs/${encodeURIComponent(queryKey[3])}`,
        undefined,
        undefined,
        { workspace: queryKey[1], signal },
      ),
    enabled: computed(() => inWorkspace.value && Boolean(toValue(id))),
    retry: false,
  });

// Adds a Team for the caller and answers the agents it created.
export const addTeam = (id: string) => api<AgentDefinition[]>('POST', `/api/teams/${id}/add`);
