import { computed, toValue, type MaybeRefOrGetter } from 'vue';
import { useQuery, useQueryClient } from '@tanstack/vue-query';
import { api } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import type { Runtime, RunMessage } from '../api/types';
import type {
  ChatRow,
  Cli,
  ProjectRow,
  MessageRow,
  ProjectMember,
  FileRow,
  CellRow,
  EnvironmentRow,
  FileVersion,
  RecordedRun,
  RecordedRunDetail,
} from './types';
export { keys };
export const projectPath = (id: string) => `/api/coresearch/projects/${encodeURIComponent(id)}`;
export const chatPath = (id: string, chat: string) =>
  `${projectPath(id)}/chats/${encodeURIComponent(chat)}`;
export function useCoresearchProjects() {
  return useQuery({
    queryKey: computed(() => keys.coresearchProjects(workspaceId.value)),
    enabled: computed(() => !!workspaceId.value),
    queryFn: async ({ signal }) =>
      (await api<ProjectRow[] | null>('GET', '/api/coresearch/projects', undefined, undefined, {
        signal,
        workspace: workspaceId.value,
      })) ?? [],
  });
}
export function useCoresearchProject(id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => keys.coresearchProject(workspaceId.value, toValue(id))),
    enabled: computed(() => !!workspaceId.value && !!toValue(id)),
    queryFn: ({ signal }) =>
      api<ProjectRow>('GET', projectPath(toValue(id)), undefined, undefined, {
        signal,
        workspace: workspaceId.value,
      }),
  });
}
export function useCoresearchChats(id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => keys.coresearchChats(workspaceId.value, toValue(id))),
    enabled: computed(() => !!workspaceId.value && !!toValue(id)),
    queryFn: async ({ signal }) =>
      (await api<ChatRow[] | null>(
        'GET',
        `${projectPath(toValue(id))}/chats`,
        undefined,
        undefined,
        { signal, workspace: workspaceId.value },
      )) ?? [],
  });
}
export function ownOnlineClis(runtimes: Runtime[], user: string | undefined): Cli[] {
  if (!user) return [];
  return [
    ...new Set(
      runtimes
        .filter((r) => r.owner_id === user && r.status === 'online')
        .map((r) => r.harness)
        .filter((cli): cli is Cli => cli === 'claude' || cli === 'codex'),
    ),
  ];
}
// Copied from Lykeion-L2 packages/api/src/task-title.ts.
export const PROMPT_TITLE_MAX = 80;
export function titleFromPrompt(prompt: string): string {
  return prompt.trim().slice(0, PROMPT_TITLE_MAX).trim();
}
export const NO_MACHINE =
  'No machine of yours is connected to this lab. Install the local machine daemon on a machine you want to run on, and it will appear here.';

export function useCoresearchMembers(id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => keys.coresearchMembers(workspaceId.value, toValue(id))),
    enabled: computed(() => !!workspaceId.value && !!toValue(id)),
    queryFn: async ({ signal }) =>
      (await api<ProjectMember[] | null>(
        'GET',
        `${projectPath(toValue(id))}/members`,
        undefined,
        undefined,
        { signal, workspace: workspaceId.value },
      )) ?? [],
  });
}
export function useCoresearchFiles(id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => keys.coresearchFiles(workspaceId.value, toValue(id))),
    enabled: computed(() => !!workspaceId.value && !!toValue(id)),
    queryFn: async ({ signal }) =>
      (await api<FileRow[] | null>(
        'GET',
        `${projectPath(toValue(id))}/files`,
        undefined,
        undefined,
        { signal, workspace: workspaceId.value },
      )) ?? [],
  });
}
export function useCoresearchMessages(
  id: MaybeRefOrGetter<string>,
  chat: MaybeRefOrGetter<string>,
) {
  return useQuery({
    queryKey: computed(() =>
      keys.coresearchMessages(workspaceId.value, toValue(id), toValue(chat)),
    ),
    enabled: computed(() => !!workspaceId.value && !!toValue(id) && !!toValue(chat)),
    queryFn: async ({ signal }) =>
      (await api<MessageRow[] | null>(
        'GET',
        `${chatPath(toValue(id), toValue(chat))}/messages`,
        undefined,
        undefined,
        { signal, workspace: workspaceId.value },
      )) ?? [],
    refetchInterval: (query) =>
      query.state.data?.some((m) => isActiveTurn(m.status)) ? 1500 : false,
  });
}
export const isActiveTurn = (status: string | null | undefined) =>
  ['queued', 'dispatched', 'running'].includes(status ?? '');
export function useCoresearchSteps(
  id: MaybeRefOrGetter<string>,
  chat: MaybeRefOrGetter<string>,
  run: MaybeRefOrGetter<string>,
  active: MaybeRefOrGetter<boolean>,
) {
  return useQuery({
    queryKey: computed(() =>
      keys.coresearchSteps(workspaceId.value, toValue(id), toValue(chat), toValue(run)),
    ),
    enabled: computed(
      () => !!workspaceId.value && !!toValue(id) && !!toValue(chat) && !!toValue(run),
    ),
    queryFn: async ({ signal }) =>
      (await api<RunMessage[] | null>(
        'GET',
        `${chatPath(toValue(id), toValue(chat))}/turns/${encodeURIComponent(toValue(run))}/steps`,
        undefined,
        undefined,
        { signal, workspace: workspaceId.value },
      )) ?? [],
    refetchInterval: computed(() => (toValue(active) ? 1000 : false)),
  });
}
export function useCoresearchCells(id: MaybeRefOrGetter<string>, chat: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => keys.coresearchCells(workspaceId.value, toValue(id), toValue(chat))),
    enabled: computed(() => !!workspaceId.value && !!toValue(id) && !!toValue(chat)),
    queryFn: async ({ signal }) =>
      (await api<CellRow[] | null>(
        'GET',
        `${chatPath(toValue(id), toValue(chat))}/cells`,
        undefined,
        undefined,
        { signal, workspace: workspaceId.value },
      )) ?? [],
    refetchInterval: 1500,
  });
}
export function useCoresearchEnvironment(id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => keys.coresearchEnvironment(workspaceId.value, toValue(id))),
    enabled: computed(() => !!workspaceId.value && !!toValue(id)),
    queryFn: ({ signal }) =>
      api<EnvironmentRow>('GET', `${projectPath(toValue(id))}/environment`, undefined, undefined, {
        signal,
        workspace: workspaceId.value,
      }),
    refetchInterval: 1500,
  });
}

export const filePath = (project: string, file: string) =>
  `${projectPath(project)}/files/${encodeURIComponent(file)}`;
export function useCoresearchVersions(
  id: MaybeRefOrGetter<string>,
  file: MaybeRefOrGetter<string>,
) {
  return useQuery({
    queryKey: computed(() =>
      keys.coresearchVersions(workspaceId.value, toValue(id), toValue(file)),
    ),
    enabled: computed(() => !!workspaceId.value && !!toValue(id) && !!toValue(file)),
    queryFn: async ({ signal }) =>
      (await api<FileVersion[] | null>(
        'GET',
        `${filePath(toValue(id), toValue(file))}/versions`,
        undefined,
        undefined,
        { signal, workspace: workspaceId.value },
      )) ?? [],
  });
}
export function useCoresearchContent(
  id: MaybeRefOrGetter<string>,
  file: MaybeRefOrGetter<string>,
  n: MaybeRefOrGetter<number>,
) {
  return useQuery({
    queryKey: computed(() =>
      keys.coresearchContent(workspaceId.value, toValue(id), toValue(file), toValue(n)),
    ),
    enabled: computed(
      () => !!workspaceId.value && !!toValue(id) && !!toValue(file) && toValue(n) > 0,
    ),
    staleTime: Infinity,
    queryFn: ({ signal }) =>
      api<Blob>(
        'GET',
        `${filePath(toValue(id), toValue(file))}/versions/${toValue(n)}/content`,
        undefined,
        undefined,
        { signal, workspace: workspaceId.value, response: 'blob' },
      ),
  });
}
export function useCoresearchRuns(id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => keys.coresearchRuns(workspaceId.value, toValue(id))),
    enabled: computed(() => !!workspaceId.value && !!toValue(id)),
    queryFn: async ({ signal }) =>
      (await api<RecordedRun[] | null>(
        'GET',
        `${projectPath(toValue(id))}/runs`,
        undefined,
        undefined,
        { signal, workspace: workspaceId.value },
      )) ?? [],
    refetchInterval: (q) =>
      q.state.data?.some((r) => ['queued', 'running'].includes(r.status)) ? 1500 : false,
  });
}
export function useCoresearchRun(id: MaybeRefOrGetter<string>, key: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => keys.coresearchRun(workspaceId.value, toValue(id), toValue(key))),
    enabled: computed(() => !!workspaceId.value && !!toValue(id) && !!toValue(key)),
    queryFn: ({ signal }) =>
      api<RecordedRunDetail>(
        'GET',
        `${projectPath(toValue(id))}/runs/${encodeURIComponent(toValue(key))}`,
        undefined,
        undefined,
        { signal, workspace: workspaceId.value },
      ),
    refetchInterval: (q) =>
      ['queued', 'running'].includes(q.state.data?.status ?? '') ? 1500 : false,
  });
}
export function useProducingTurn(id: MaybeRefOrGetter<string>, run: MaybeRefOrGetter<string>) {
  const client = useQueryClient();
  return useQuery({
    queryKey: computed(() =>
      keys.coresearchProducingTurn(workspaceId.value, toValue(id), toValue(run)),
    ),
    enabled: computed(() => !!workspaceId.value && !!toValue(id) && !!toValue(run)),
    queryFn: async ({ signal }) => {
      const ws = workspaceId.value,
        project = toValue(id),
        target = toValue(run);
      const chats = await client.fetchQuery({
        queryKey: keys.coresearchChats(ws, project),
        staleTime: 30_000,
        queryFn: () =>
          api<ChatRow[]>('GET', `${projectPath(project)}/chats`, undefined, undefined, {
            signal,
            workspace: ws,
          }),
      });
      // Sequential scheduling bounds concurrent requests to one and stops at the exact turn.
      for (const chat of chats ?? []) {
        signal.throwIfAborted();
        try {
          const messages = await client.fetchQuery({
            queryKey: keys.coresearchMessages(ws, project, chat.id),
            staleTime: 30_000,
            queryFn: () =>
              api<MessageRow[]>(
                'GET',
                `${chatPath(project, chat.id)}/messages`,
                undefined,
                undefined,
                { signal, workspace: ws },
              ),
          });
          const turn = messages?.find((m) => m.kind === 'turn' && m.agent_run_id === target);
          if (turn)
            return {
              turn,
              replies: messages.filter((m) => m.agent_run_id === target && m.id !== turn.id),
            };
        } catch (error) {
          if (signal.aborted) throw error;
        }
      }
      return null;
    },
    staleTime: 30_000,
  });
}
