import { computed, toValue, type MaybeRefOrGetter } from 'vue';
import { useQuery, useQueryClient, type QueryKey } from '@tanstack/vue-query';
import { api } from './client';
import { keys } from './keys';
import { workspaceId as ws } from './session';
import type {
  ConnectorSummary,
  RuntimeSkillOperation,
  RuntimeSkillRequest,
  SkillDetail,
  SkillSummary,
  WorkspaceMcpSummary,
} from './types';
export type {
  ConnectorSummary,
  RuntimeSkillInventory,
  RuntimeSkillOperation,
  RuntimeSkillOutcome,
  RuntimeSkillRequest,
  SkillDetail,
  SkillFileMeta,
  SkillSource,
  SkillSummary,
  WorkspaceMcpSummary,
} from './types';

export type SkillCreate = { name: string; description: string };
export type SkillUpdate = { revision: number; content: string };
export type ConnectorUpdate = {
  revision: number;
  enabled?: boolean;
  key?: string;
  licence_confirmed?: boolean;
};
// Configuration is write-only. Query values contain summaries, never inputs.
export type WorkspaceMcpConfig =
  | { url: string; headers?: Record<string, string>; command?: never; args?: never; env?: never }
  | {
      command: string;
      args?: string[];
      env?: Record<string, string>;
      url?: never;
      headers?: never;
    };
export type WorkspaceMcpCreate = { name: string; config: WorkspaceMcpConfig };
export type WorkspaceMcpUpdate = WorkspaceMcpCreate & { revision: number };
export type SkillFolderEntry = { path: string; data: Blob };

// Bundled IDs retain their namespace colon in the router path segment.
function skillSegment(id: string) {
  const encoded = encodeURIComponent(id);
  return id.startsWith('bundle:') ? encoded.replace('bundle%3A', 'bundle:') : encoded;
}

function libraryList<T>(key: (workspace: string) => readonly unknown[], path: string) {
  return useQuery({
    queryKey: computed(() => key(ws.value)),
    queryFn: async ({ queryKey, signal }) =>
      (await api<T[] | null>('GET', path, undefined, undefined, {
        workspace: queryKey[1] as string,
        signal,
      })) ?? [],
    enabled: computed(() => Boolean(ws.value)),
  });
}
export const useSkillLibrary = () => libraryList<SkillSummary>(keys.skills, '/api/skills');
export const useConnectorLibrary = () =>
  libraryList<ConnectorSummary>(keys.connectors, '/api/connectors');
export const useWorkspaceMCPServers = () =>
  libraryList<WorkspaceMcpSummary>(keys.workspaceMCP, '/api/mcp-servers');

export const useSkillDetail = (id: MaybeRefOrGetter<string>) =>
  useQuery({
    queryKey: computed(() => keys.skill(ws.value, toValue(id))),
    queryFn: ({ queryKey, signal }) =>
      api<SkillDetail>('GET', `/api/skills/${skillSegment(queryKey[3])}`, undefined, undefined, {
        workspace: queryKey[1],
        signal,
      }),
    enabled: computed(() => Boolean(ws.value && toValue(id))),
    retry: false,
  });

export function useSkillFile(id: MaybeRefOrGetter<string>, path: MaybeRefOrGetter<string>) {
  const client = useQueryClient();
  const detail = useSkillDetail(id);
  const metadata = computed(() =>
    detail.data.value?.files.find((file) => file.path === toValue(path)),
  );
  return useQuery({
    queryKey: computed(() =>
      keys.skillFile(ws.value, toValue(id), detail.data.value?.revision ?? 0, toValue(path)),
    ),
    queryFn: async ({ queryKey, signal }) => {
      const [, workspace, , skill, , revision, filePath] = queryKey;
      const expected = metadata.value!;
      const current = () =>
        !signal.aborted &&
        ws.value === workspace &&
        toValue(id) === skill &&
        toValue(path) === filePath &&
        detail.data.value?.revision === revision;
      const blob = await api<Blob>(
        'GET',
        `/api/skills/${skillSegment(skill)}/files?path=${encodeURIComponent(filePath)}`,
        undefined,
        undefined,
        { workspace, signal, response: 'blob' },
      );
      if (!current()) throw new Error('Skill file read cancelled');
      const bytes = await blob.arrayBuffer();
      const digest = await crypto.subtle.digest('SHA-256', bytes);
      if (!current()) throw new Error('Skill file read cancelled');
      const hash = Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, '0')).join(
        '',
      );
      // The file route serves the current revision. Verify before associating
      // its bytes with the revision captured from the detail response.
      if (bytes.byteLength !== expected.size || hash !== expected.sha256) {
        void client.invalidateQueries({ queryKey: keys.skills(workspace) });
        void client.invalidateQueries({ queryKey: keys.skill(workspace, skill), exact: true });
        throw new Error('Skill file changed; refresh the saved version');
      }
      return blob;
    },
    enabled: computed(() =>
      Boolean(ws.value && toValue(id) && metadata.value && !detail.isError.value),
    ),
    retry: false,
  });
}

export const useRuntimeSkillRequest = (
  runtime: MaybeRefOrGetter<string>,
  id: MaybeRefOrGetter<string>,
  operation: MaybeRefOrGetter<RuntimeSkillOperation>,
) =>
  useQuery({
    queryKey: computed(() => keys.runtimeSkillRequest(ws.value, toValue(runtime), toValue(id))),
    queryFn: ({ queryKey, signal }) => {
      const [, workspace, , runtime, , request] = queryKey;
      const suffix = toValue(operation) === 'import' ? '/import' : '';
      return api<RuntimeSkillRequest>(
        'GET',
        `/api/runtimes/${encodeURIComponent(runtime)}/local-skills${suffix}/${encodeURIComponent(request)}`,
        undefined,
        undefined,
        { workspace, signal },
      );
    },
    enabled: computed(() => Boolean(ws.value && toValue(runtime) && toValue(id))),
    retry: false,
  });

export function useLibraryMutations() {
  const client = useQueryClient();
  async function mutate<T>(
    method: string,
    path: string,
    body: unknown,
    affected: (workspace: string, response: T) => QueryKey[],
  ) {
    const workspace = ws.value;
    const response = await api<T>(method, path, body, undefined, { workspace });
    await Promise.all(
      affected(workspace, response).map((queryKey) => client.invalidateQueries({ queryKey })),
    );
    return response;
  }
  const skillChanged = (workspace: string, detail: SkillDetail) => [
    keys.skills(workspace),
    keys.skill(workspace, detail.id),
  ];
  const mcpChanged = (workspace: string, summary: WorkspaceMcpSummary) => [
    keys.workspaceMCP(workspace),
    keys.workspaceMCPItem(workspace, summary.id),
  ];
  return {
    setAvailability: (
      kind: 'skill' | 'mcp',
      id: string,
      input: { revision: number; enabled: boolean },
    ) =>
      mutate<{ enabled: boolean; revision: number }>(
        'PUT',
        `/api/availability/${kind}/${kind === 'skill' ? skillSegment(id) : encodeURIComponent(id)}`,
        input,
        (workspace) =>
          kind === 'skill'
            ? [keys.skills(workspace), keys.skill(workspace, id)]
            : [keys.workspaceMCP(workspace), keys.workspaceMCPItem(workspace, id)],
      ),
    createSkill: (input: SkillCreate) =>
      mutate<SkillDetail>('POST', '/api/skills', input, skillChanged),
    updateSkill: (id: string, input: SkillUpdate) =>
      mutate<SkillDetail>('PATCH', `/api/skills/${skillSegment(id)}`, input, (workspace) => [
        keys.skills(workspace),
        keys.skill(workspace, id),
      ]),
    deleteSkill: (id: string, revision: number) =>
      mutate<void>('DELETE', `/api/skills/${skillSegment(id)}`, { revision }, (workspace) => [
        keys.skills(workspace),
        keys.skill(workspace, id),
      ]),
    importSkillURL: (url: string) =>
      mutate<SkillDetail>('POST', '/api/skills/import', { url }, skillChanged),
    importSkillArchive: (archive: Blob, name: string) => {
      const body = new FormData();
      body.append('file', archive, name);
      return mutate<SkillDetail>('POST', '/api/skills/import', body, skillChanged);
    },
    importSkillFolder: (files: SkillFolderEntry[]) => {
      const body = new FormData();
      for (const file of files) body.append('files', file.data, file.path);
      return mutate<SkillDetail>('POST', '/api/skills/import', body, skillChanged);
    },
    setConnector: (name: string, input: ConnectorUpdate) =>
      mutate<ConnectorSummary>(
        'PUT',
        `/api/connectors/${encodeURIComponent(name)}`,
        input,
        (workspace) => [keys.connectors(workspace)],
      ),
    createWorkspaceMCP: (input: WorkspaceMcpCreate) =>
      mutate<WorkspaceMcpSummary>('POST', '/api/mcp-servers', input, mcpChanged),
    updateWorkspaceMCP: (id: string, input: WorkspaceMcpUpdate) =>
      mutate<WorkspaceMcpSummary>(
        'PUT',
        `/api/mcp-servers/${encodeURIComponent(id)}`,
        input,
        (workspace) => [keys.workspaceMCP(workspace), keys.workspaceMCPItem(workspace, id)],
      ),
    deleteWorkspaceMCP: (id: string, revision: number) =>
      mutate<void>(
        'DELETE',
        `/api/mcp-servers/${encodeURIComponent(id)}`,
        { revision },
        (workspace) => [keys.workspaceMCP(workspace), keys.workspaceMCPItem(workspace, id)],
      ),
    listRuntimeSkills: (runtime: string) =>
      mutate<RuntimeSkillRequest>(
        'POST',
        `/api/runtimes/${encodeURIComponent(runtime)}/local-skills`,
        undefined,
        (workspace, job) => [keys.runtimeSkillRequest(workspace, runtime, job.id)],
      ),
    importRuntimeSkills: (runtime: string, selectedKeys: string[]) =>
      mutate<RuntimeSkillRequest>(
        'POST',
        `/api/runtimes/${encodeURIComponent(runtime)}/local-skills/import`,
        { keys: selectedKeys },
        (workspace, job) => [keys.runtimeSkillRequest(workspace, runtime, job.id)],
      ),
  };
}
