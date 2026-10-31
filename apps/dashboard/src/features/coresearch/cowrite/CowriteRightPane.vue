<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { Build, Version, Comment } from './types';
import RightPaneTabs from '../RightPaneTabs.vue';
import PdfPane from './PdfPane.vue';
import ProjectInfoPane from './ProjectInfoPane.vue';
import VersionsPane from './VersionsPane.vue';
import CommentsPane from './CommentsPane.vue';
import LogPane from './LogPane.vue';
import ComputePanel from './ComputePanel.vue';
const props = defineProps<{
  projectId: string;
  title: string;
  builds: Build[];
  sources: string[];
  versions: Version[];
  comments: (Comment & { path: string })[];
  people: { id: string; name: string }[];
  meId?: string;
  compute?: boolean;
  noMachine?: boolean;
  disabled?: boolean;
  cursor?: { path: string; line: number };
  log?: string;
  logFailed?: boolean;
  errors?: number;
  restore?: (v: Version) => Promise<void>;
}>();
const emit = defineEmits<{
  compile: [];
  undo: [];
  redo: [];
  goToSource: [path: string, line: number];
  deleteComment: [id: string];
  'update:compute': [value: boolean];
  focus: [value: boolean];
}>();
const tools = ref(false),
  active = ref('info'),
  focus = ref(false);
watch(
  () => props.compute,
  (v) => {
    if (v) {
      tools.value = true;
      active.value = 'compute';
    } else if (active.value === 'compute') {
      active.value = 'info';
      tools.value = false;
    }
  },
  { immediate: true },
);
// Compute is the sidebar's own tab: it never shares the strip with the
// paper's tools, and it closes.
const tabs = computed(() =>
  active.value === 'compute'
    ? [{ id: 'compute', label: 'Compute', title: 'The machine this paper runs on', closable: true }]
    : [
        { id: 'info', label: 'Project Info' },
        { id: 'versions', label: 'Versions' },
        { id: 'comments', label: 'Comments' },
        { id: 'logs', label: 'Logs' },
      ],
);
function close() {
  tools.value = false;
  focus.value = false;
  emit('focus', false);
  emit('update:compute', false);
}
</script>
<template>
  <div class="task-rightpane">
    <template v-if="tools"
      ><RightPaneTabs
        :tabs="tabs"
        :active-id="active"
        :pane-mode="focus ? 'focus' : 'split'"
        @select="active = $event"
        @close-tab="close"
        @toggle-focus="
          focus = !focus;
          emit('focus', focus);
        "
        @close-pane="close" />
      <div class="task-rightpane-card">
        <ProjectInfoPane v-if="active === 'info'" :sources="sources" /><VersionsPane
          v-else-if="active === 'versions'"
          :project-id="projectId"
          :versions="versions"
          :people="people"
          :restore="restore ?? (async () => {})"
          :disabled="disabled"
        /><CommentsPane
          v-else-if="active === 'comments'"
          :comments="comments"
          :people="people"
          :me-id="meId"
          @delete="emit('deleteComment', $event)"
          @open="emit('goToSource', $event.path, $event.line)"
        /><LogPane
          v-else-if="active === 'logs'"
          :build="[...builds].sort((a, b) => b.seq - a.seq)[0]"
          :log="log"
          :failed="logFailed"
          @jump="(path, line) => emit('goToSource', path, line)"
        /><ComputePanel
          v-else-if="active === 'compute'"
          role="tabpanel"
          aria-label="Compute"
          :project-id="projectId"
          :title="title"
          :disabled="disabled"
        /></div
    ></template>
    <div v-else class="task-rightpane-card">
      <PdfPane
        :project-id="projectId"
        :title="title"
        :builds="builds"
        :no-machine="noMachine"
        :disabled="disabled"
        :errors="errors"
        :cursor="cursor"
        @compile="emit('compile')"
        @undo="emit('undo')"
        @redo="emit('redo')"
        @tools="
          tools = true;
          active = 'info';
        "
        @go-to-source="(path, line) => emit('goToSource', path, line)"
      />
    </div>
  </div>
</template>
