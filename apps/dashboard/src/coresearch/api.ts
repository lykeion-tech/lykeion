import { computed, toValue, type MaybeRefOrGetter } from 'vue';
import { useQuery } from '@tanstack/vue-query';
import { api } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import type { Runtime } from '../api/types';
import type { ChatRow, Cli, ProjectRow } from './types';
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
