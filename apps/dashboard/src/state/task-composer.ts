import { reactive, readonly } from 'vue';
import type { Picked } from '../lib/studies';

export type Assignee = Picked;

// Whether the New study modal is open, and who a new study starts assigned to.
const composer = reactive<{
  open: boolean;
  assignee?: Assignee;
  projectId?: string;
}>({
  open: false,
});

function openComposer(assignee?: Assignee, projectId?: string) {
  Object.assign(composer, { open: true, assignee, projectId });
}
function closeComposer() {
  Object.assign(composer, {
    open: false,
    assignee: undefined,
    projectId: undefined,
  });
}

export function useTaskComposer() {
  return { composer: readonly(composer), openComposer, closeComposer };
}
