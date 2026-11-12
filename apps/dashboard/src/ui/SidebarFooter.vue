<script setup lang="ts">
import { ref } from 'vue';
import Icon from './Icon.vue';
withDefaults(
  defineProps<{
    userName: string;
    workspaceName: string;
    avatarUrl?: string;
    selected?: boolean;
    menuOpen?: boolean;
    helpOpen?: boolean;
  }>(),
  { selected: false, menuOpen: false, helpOpen: false },
);
const emit = defineEmits<{ toggleWorkspace: []; toggleHelp: [] }>();
const workspaceTrigger = ref<HTMLButtonElement>();
const helpTrigger = ref<HTMLButtonElement>();
defineExpose({
  focusWorkspace: () => workspaceTrigger.value?.focus(),
  focusHelp: () => helpTrigger.value?.focus(),
});
</script>
<template>
  <div class="sidebar-bottom">
    <div class="settings-line">
      <button
        ref="workspaceTrigger"
        class="workspace-switch nav-item"
        :title="workspaceName"
        :class="{ selected }"
        data-shell-menu-trigger
        aria-haspopup="menu"
        :aria-expanded="menuOpen"
        aria-controls="workspace-menu"
        @click="emit('toggleWorkspace')"
        @keydown.up.prevent="!menuOpen && emit('toggleWorkspace')"
      >
        <span class="workspace-avatar user-avatar"
          ><img v-if="avatarUrl" :src="avatarUrl" alt="" /><template v-else>{{
            (userName[0] ?? '?').toUpperCase()
          }}</template></span
        ><span class="workspace-switch-name">{{ userName }}</span
        ><Icon name="down" class="push-right" :size="14" /></button
      ><button
        ref="helpTrigger"
        class="icon-button"
        aria-label="Help"
        data-shell-menu-trigger
        aria-haspopup="menu"
        :aria-expanded="helpOpen"
        aria-controls="help-menu"
        @click="emit('toggleHelp')"
        @keydown.up.prevent="!helpOpen && emit('toggleHelp')"
      >
        <Icon name="help" />
      </button>
    </div>
  </div>
</template>
<style scoped>
.user-avatar {
  overflow: hidden;
}
.user-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
</style>
