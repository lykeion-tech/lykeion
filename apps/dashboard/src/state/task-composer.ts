import { reactive, readonly } from 'vue';
import { state } from '../services/workspace';

// Which create-task composer is open, and what it starts with.
const composer = reactive({
  open: false,
  manual: false,
  assigneeId: undefined as string | undefined,
  parentId: undefined as string | undefined,
  projectId: undefined as string | undefined,
});

function openComposer(assigneeId?: string) {
  Object.assign(composer, {
    open: true,
    manual: false,
    assigneeId,
    parentId: undefined,
    projectId: undefined,
  });
}

function openManualComposer() {
  openComposer();
  composer.manual = true;
}

// A sub-task starts in its parent's project and with its parent's assignee.
function openSubtaskComposer(parentId: string) {
  const parent = state.data?.inquiries.find((x) => x.id === parentId);
  if (!parent) return;
  composer.parentId = parent.id;
  composer.projectId = parent.projectId ?? undefined;
  composer.assigneeId = parent.assigneeId ?? undefined;
  composer.open = true;
}

function closeComposer() {
  Object.assign(composer, {
    open: false,
    manual: false,
    assigneeId: undefined,
    parentId: undefined,
    projectId: undefined,
  });
}

export function useTaskComposer() {
  return {
    composer: readonly(composer),
    openComposer,
    openManualComposer,
    openSubtaskComposer,
    closeComposer,
  };
}
