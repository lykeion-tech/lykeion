import { reactive, readonly } from 'vue';

// Whether the New study modal is open, and which Backlog study it edits.
const composer = reactive<{ open: boolean; editId?: string }>({ open: false });

function openComposer(editId?: string) {
  Object.assign(composer, { open: true, editId });
}
function closeComposer() {
  Object.assign(composer, { open: false, editId: undefined });
}

export function useTaskComposer() {
  return { composer: readonly(composer), openComposer, closeComposer };
}
