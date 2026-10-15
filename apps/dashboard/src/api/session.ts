import { ref, watch } from 'vue';
import { readText, writeText } from '../storage';

// The workspace the dashboard shows. Workspace-scoped routes receive it as
// X-Workspace-ID; it survives reloads so a person returns where they were.
export const workspaceId = ref(readText('lykeion.workspace') || '');
watch(workspaceId, (id) => writeText('lykeion.workspace', id));
