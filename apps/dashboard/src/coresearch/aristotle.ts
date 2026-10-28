import { computed } from 'vue';
import { useQuery, useQueryClient } from '@tanstack/vue-query';
import { api } from '../api/client';
import { workspaceId } from '../api/session';
import { useMe, useAgent } from '../api/queries';
import { keys } from '../api/keys';
import type { AgentDefinition } from '../api/types';
export interface Starter {
  label: string;
  prompt: string;
}
export interface Conversation {
  id: string;
  workspace_id: string;
  owner_id: string;
  agent_id: string;
  project_id: string;
  chat_id: string;
  title: string;
  preview: string;
  is_welcome: boolean;
  snapshot_harness: 'claude' | 'codex' | null;
  snapshot_model: string | null;
  snapshot_instructions: string | null;
}
export const defaultStarters: Starter[] = [
  {
    label: 'Ground a research question',
    prompt:
      'Help me turn my research question into a focused, evidence-grounded question. Ask about the topic and constraints first.',
  },
  {
    label: 'Design a reproducible experiment',
    prompt:
      'Help me design a reproducible experiment. Ask about the hypothesis, available data, and constraints first.',
  },
  {
    label: 'Review evidence and findings',
    prompt:
      'Help me review evidence and findings. Ask me to share the sources or results and assess their strength and uncertainty.',
  },
];
export interface AcceptanceRequest {
  body: string;
  target: string;
  request_id: string;
}
export function acceptanceRequest(
  previous: AcceptanceRequest | null,
  body: string,
  target: string,
): AcceptanceRequest {
  return previous?.body === body && previous.target === target
    ? previous
    : { body, target, request_id: crypto.randomUUID() };
}
export function restoreConversation(
  list: { id: string; is_welcome: boolean }[],
  saved: string | null,
) {
  return list.find((c) => c.id === saved)?.id ?? list.find((c) => c.is_welcome)?.id;
}
export const conversationKey = (ws: string, member: string) =>
  [...keys.coresearchConversations(ws), member] as const;
export function useAristotle() {
  const me = useMe(),
    client = useQueryClient();
  const scope = computed(() => `${me.data.value?.id ?? ''}:${workspaceId.value}`);
  const bootstrap = useQuery({
    queryKey: computed(() => [
      'ws',
      workspaceId.value,
      'aristotle',
      me.data.value?.id,
      'bootstrap',
    ]),
    enabled: computed(() => !!workspaceId.value && !!me.data.value?.id),
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const ws = workspaceId.value,
        member = me.data.value!.id;
      const result = await api<{ agent: AgentDefinition; conversation: Conversation }>(
        'POST',
        '/api/coresearch/bootstrap',
        undefined,
        undefined,
        { workspace: ws, signal },
      );
      if (!result?.agent?.id || !result?.conversation?.id)
        throw new Error('Could not initialize Aristotle. Try again.');
      if (workspaceId.value === ws && me.data.value?.id === member) {
        // Fetch current settings: bootstrap may race a profile save, so never seed its agent into the cache.
        void client.invalidateQueries({ queryKey: keys.agents(ws) });
        void client.invalidateQueries({ queryKey: keys.agent(ws, result.agent.id) });
      }
      return result;
    },
  });
  const agent = useAgent(() => bootstrap.data.value?.agent.id ?? '');
  const conversations = useQuery({
    queryKey: computed(() => conversationKey(workspaceId.value, me.data.value?.id ?? '')),
    enabled: computed(() => !!bootstrap.data.value?.conversation.id),
    queryFn: ({ signal }) =>
      api<Conversation[]>('GET', '/api/coresearch/conversations', undefined, undefined, {
        workspace: workspaceId.value,
        signal,
      }),
  });
  return {
    scope,
    bootstrap,
    agent,
    conversations,
    member: computed(() => me.data.value?.id ?? ''),
  };
}
