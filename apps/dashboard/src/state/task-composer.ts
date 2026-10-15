import { reactive, readonly } from 'vue';

export interface Assignee {
  type: 'member' | 'agent';
  id: string;
}

// Whether the New study modal is open, which Backlog study it edits, and who a
// new study starts assigned to.
const composer = reactive<{ open: boolean; editId?: string; assignee?: Assignee }>({
  open: false,
});

function openComposer(editId?: string, assignee?: Assignee) {
  Object.assign(composer, { open: true, editId, assignee });
}
function closeComposer() {
  Object.assign(composer, { open: false, editId: undefined, assignee: undefined });
}

export function useTaskComposer() {
  return { composer: readonly(composer), openComposer, closeComposer };
}
