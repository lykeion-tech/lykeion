<script setup lang="ts">
import { ref } from 'vue';
import Icon from './Icon.vue';
withDefaults(defineProps<{ selected?: boolean; helpOpen?: boolean }>(), {
  selected: false,
  helpOpen: false,
});
const emit = defineEmits<{ toggleHelp: [] }>();
const helpTrigger = ref<HTMLButtonElement>();
defineExpose({ focusHelp: () => helpTrigger.value?.focus() });
</script>
<template>
  <div class="sidebar-bottom">
    <div class="settings-line">
      <RouterLink to="/settings" class="nav-item" :class="{ selected }"
        ><Icon name="settings" />Settings</RouterLink
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
