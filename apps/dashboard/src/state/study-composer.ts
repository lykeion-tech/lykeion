import { reactive, readonly } from 'vue';
import type { Picked } from '../lib/studies';

export type Assignee = Picked;

// Whether the New study modal is open, and who a new study starts assigned to.
const composer = reactive<{
  open: boolean;
  assignee?: Assignee;
  projectId?: string;
  workflow?: string;
}>({
  open: false,
});

function openComposer(assignee?: Assignee, projectId?: string, workflow?: string) {
  Object.assign(composer, { open: true, assignee, projectId, workflow });
}
function closeComposer() {
  Object.assign(composer, {
    open: false,
    assignee: undefined,
    projectId: undefined,
    workflow: undefined,
  });
}

export function useStudyComposer() {
  return { composer: readonly(composer), openComposer, closeComposer };
}
