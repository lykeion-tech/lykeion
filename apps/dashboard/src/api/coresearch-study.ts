import type { QueryClient } from '@tanstack/vue-query';
import { api } from './client';
import { keys } from './keys';
import type { StudyProposalContent } from './types';

export interface CoresearchStudyDraft {
  id: string;
  projectId: string;
  chatId: string;
  title: string;
  description: string;
  stage: StudyProposalContent['stage'];
}

export async function startCoresearchStudy(
  client: QueryClient,
  workspace: string,
  draft: CoresearchStudyDraft,
  content: StudyProposalContent,
) {
  const receipt = await api<{ card_id: string }>(
    'POST',
    `/api/study-proposals/${draft.id}/start`,
    {
      ...content,
      revision: 0,
      operation_id: draft.id,
      coresearch: { project_id: draft.projectId, chat_id: draft.chatId },
    },
    undefined,
    { workspace },
  );
  await Promise.all([
    client.invalidateQueries({
      queryKey: keys.coresearchMessages(workspace, draft.projectId, draft.chatId),
    }),
    client.invalidateQueries({ queryKey: keys.cards(workspace) }),
    ...(content.project_id
      ? [client.invalidateQueries({ queryKey: keys.research(workspace, content.project_id) })]
      : []),
  ]);
  return receipt.card_id;
}
