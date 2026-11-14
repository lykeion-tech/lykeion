import { useQuery } from '@tanstack/vue-query';
import { computed, toValue, type MaybeRefOrGetter } from 'vue';
import { api } from './client';
import { keys } from './keys';
import type { WorkflowDetail, WorkflowSummary } from './types';
export type { WorkflowDetail, WorkflowSummary } from './types';

// Workflows ship with the server and are not owned by a workspace.
export const useWorkflows = () =>
  useQuery({
    queryKey: keys.workflows,
    queryFn: () => api<{ release: string; workflows: WorkflowSummary[] }>('GET', '/api/workflows'),
  });

export const useWorkflow = (slug: MaybeRefOrGetter<string>) =>
  useQuery({
    queryKey: computed(() => keys.workflow(toValue(slug))),
    queryFn: () =>
      api<WorkflowDetail>('GET', `/api/workflows/${encodeURIComponent(toValue(slug))}`),
  });

export const workflowFile = (slug: string, path: string) =>
  api<Blob>(
    'GET',
    `/api/workflows/${encodeURIComponent(slug)}/files?path=${encodeURIComponent(path)}`,
    undefined,
    undefined,
    { response: 'blob' },
  );
