import { reactive, readonly } from 'vue';
import type { Picked } from '../lib/studies';

export type Assignee = Picked;

// Whether the New study modal is open, which Backlog study it edits, and who a
// new study starts assigned to.
const composer = reactive<{
  open: boolean;
  editId?: string;
  assignee?: Assignee;
  projectId?: string;
}>({
  open: false,
});

function openComposer(editId?: string, assignee?: Assignee, projectId?: string) {
  Object.assign(composer, { open: true, editId, assignee, projectId });
}
function closeComposer() {
  Object.assign(composer, {
    open: false,
    editId: undefined,
    assignee: undefined,
    projectId: undefined,
  });
}

export function useTaskComposer() {
  return { composer: readonly(composer), openComposer, closeComposer };
}
