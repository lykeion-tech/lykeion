import { computed, toValue, type MaybeRefOrGetter } from 'vue';
import { useQuery } from '@tanstack/vue-query';
import { api } from './client';
import { keys } from './keys';
import { workspaceId as ws } from './session';
import type {
  AgentRun,
  Card,
  Comment,
  InboxItem,
  Invitation,
  LykEvent,
  Me,
  Member,
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
export const useGates = (id: MaybeRefOrGetter<string>) =>
  ofCard<StudyRecord[]>(id, keys.gates, (c) => `/api/cards/${c}/gates`);
export const useComments = (id: MaybeRefOrGetter<string>) =>
  ofCard<Comment[]>(id, keys.comments, (c) => `/api/cards/${c}/comments`);
export const useCardEvents = (id: MaybeRefOrGetter<string>) =>
  ofCard<LykEvent[]>(id, keys.events, (c) => `/api/cards/${c}/events`);
export const useRuns = (id: MaybeRefOrGetter<string>) =>
  ofCard<AgentRun[]>(id, keys.runs, (c) => `/api/cards/${c}/runs`);

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

// A run's messages for one attempt. No event announces a message, so while the
// run is live the query polls; a settled run's messages never change.
export const useRunMessages = (
  run: MaybeRefOrGetter<AgentRun | undefined>,
  open: MaybeRefOrGetter<boolean>,
) =>
  useQuery({
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
