import { ref, watch } from 'vue';
import { readText, writeText } from '../storage';
import { guardResourceDrafts } from '../state/resource-draft';

// The workspace the dashboard shows. Workspace-scoped routes receive it as
// X-Workspace-ID; it survives reloads so a person returns where they were.
export const workspaceId = ref(readText('lykeion.workspace') || '');
watch(workspaceId, (id) => writeText('lykeion.workspace', id));

// Guard before changing the shared ref: requests and subscriptions key off it.
export async function selectWorkspace(id: string): Promise<boolean> {
  if (workspaceId.value === id) return true;
  // Only SignIn's verified workspace bootstrap may park a scoped journal.
  if (!(await guardResourceDrafts(undefined, !workspaceId.value ? 'workspace' : undefined)))
    return false;
  workspaceId.value = id;
  return true;
}
