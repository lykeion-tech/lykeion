import { computed, type Ref } from 'vue';
import { useInfiniteQuery } from '@tanstack/vue-query';
import { api } from '../../api/client';
import type { ManualDecisionDraft } from '../decisions/manualDecisionDraft';
import type { Card, StudyRecord } from '../../api/types';
import { workspaceId } from '../../api/session';

export interface LabRef {
  ref: string;
  title: string;
  via?: string;
  study_id?: string;
  facts?: LabRef[];
}
export interface LabStatement {
  kind: string;
  text: string;
  cites: LabRef[];
}
export interface LabMessage {
  id: string;
  kind: string;
  body: string;
  statements: LabStatement[];
  evidence: LabRef[];
  changed?: boolean;
  agent_run_id?: string;
  current_draft?: ManualDecisionDraft;
  proposal?: unknown;
}
export interface LabChat {
  id: string;
  title: string;
}
const root = '/api/lab/chats';
const page = (path: string, cursor: string) =>
  cursor ? path + '?cursor=' + encodeURIComponent(cursor) : path;
export const labKey = (ws: string) => ['ws', ws, 'lab'] as const;
export function useChats(user: () => string, open: Ref<boolean>) {
  return useInfiniteQuery({
    queryKey: computed(() => [...labKey(workspaceId.value), user(), 'chats']),
    enabled: computed(() => open.value && !!user() && !!workspaceId.value),
    initialPageParam: '',
    queryFn: ({ pageParam, signal, queryKey }) =>
      api<{ chats: LabChat[]; next_cursor: string }>(
        'GET',
        page(root, pageParam),
        undefined,
        undefined,
        { workspace: queryKey[1], signal },
      ),
    getNextPageParam: (last) => last.next_cursor || undefined,
    staleTime: 0,
    gcTime: 0,
  });
}
export function useMessages(user: () => string, open: Ref<boolean>, chat: Ref<string>, purpose: () => string = () => 'answer') {
  return useInfiniteQuery({
    queryKey: computed(() => [
      ...labKey(workspaceId.value),
      user(),
      'messages',
      open.value ? chat.value : '',
      purpose(),
    ]),
    enabled: computed(() => open.value && !!chat.value && !!workspaceId.value && !!user()),
    initialPageParam: '',
    queryFn: ({ pageParam, signal, queryKey }) =>
      api<{ messages: LabMessage[]; next_cursor: string; pending: boolean }>(
        'GET',
        page(root + '/' + encodeURIComponent(queryKey[5]!) + '/messages', pageParam),
        undefined,
        undefined,
        { workspace: queryKey[1], signal },
      ),
    getNextPageParam: (last) => last.next_cursor || undefined,
    // Recheck all loaded pages even when the workspace event socket is disconnected.
    refetchInterval: (query) => (query.state.data?.pages[0]?.pending ? 1800 : 15000),
    staleTime: 0,
    gcTime: 0,
  });
}
export const createChat = (ws: string) =>
  api<{ chat: LabChat }>('POST', root, {}, undefined, { workspace: ws });
export const deleteChat = (ws: string, id: string) =>
  api('DELETE', root + '/' + encodeURIComponent(id), undefined, undefined, { workspace: ws });
export const askLab = (
  ws: string,
  id: string,
  question: { text: string; model: string; harness: 'claude' | 'codex'; context_ref?: string },
) =>
  api<{ message: LabMessage }>(
    'POST',
    root + '/' + encodeURIComponent(id) + '/questions',
    question,
    undefined,
    { workspace: ws },
  );
export function sourcePath(source: LabRef): string | undefined {
  const [kind, id] = source.ref.split(':');
  if (kind === 'workspace') return '/settings?tab=workspace';
  if (kind === 'proposal') return '/studies';
  if (kind === 'decision') return '/decisions/' + encodeURIComponent(id!);
  if (!source.study_id) return;
  const path = '/studies/' + encodeURIComponent(source.study_id);
  if (kind === 'study') return path;
  if (kind === 'report') return '/decisions/report/' + encodeURIComponent(source.study_id);
  if (kind === 'record' || kind === 'comment')
    return path + '?cite=' + encodeURIComponent(source.ref);
}

// Only a current attachment of this study may supply file context. Binary
// files have a record key but no text blob.
export function labRecordTitle(record: StudyRecord, study: Card | undefined, ws: string) {
  if (record.body?.server === 'attachments' || record.blob_key?.startsWith(ws + '/attachments/')) {
    const file = study?.attachments?.find(
      (a) =>
        a.record_key === record.key &&
        (!record.blob_key || record.blob_key === ws + '/attachments/' + a.id + '.text'),
    );
    return file ? 'File · ' + file.name : '';
  }
  const title = record.body?.title ?? record.body?.name;
  return [record.key, typeof title === 'string' ? title : record.kind].filter(Boolean).join(' · ');
}
