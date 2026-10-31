<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue';
import Icon from '../ui/Icon.vue';
const props = defineProps<{ command: string; label: string }>();
const copied = ref(false),
  failed = ref(false);
let timer: ReturnType<typeof setTimeout> | undefined;
let generation = 0;
function reset() {
  generation++;
  copied.value = false;
  failed.value = false;
  clearTimeout(timer);
}
watch(() => props.command, reset);
onUnmounted(reset);
async function copy() {
  const visit = generation;
  try {
    await navigator.clipboard.writeText(props.command);
    if (visit !== generation) return;
    copied.value = true;
    failed.value = false;
    clearTimeout(timer);
    timer = setTimeout(() => (copied.value = false), 2000);
  } catch {
    if (visit === generation) failed.value = true;
  }
}
</script>
<template>
  <div class="machine-command">
    <span class="prompt" aria-hidden="true">›_</span><code>{{ command }}</code
    ><button class="icon-button" :aria-label="label" @click="copy">
      <Icon :name="copied ? 'check' : 'copy'" :size="14" />
    </button>
  </div>
  <small v-if="copied || failed" class="copy-feedback" role="status">{{
    copied ? 'Copied' : 'Could not copy. Select the command to copy it manually.'
  }}</small>
</template>
<style scoped>
.machine-command {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  border-radius: 8px;
  padding: 10px 12px;
  background: var(--input);
}
.prompt {
  font-family: ui-monospace, monospace;
  font-size: 13px;
  color: var(--muted);
  padding-top: 3px;
}
code {
  flex: 1;
  min-width: 0;
  font:
    13px/20px ui-monospace,
    SFMono-Regular,
    Consolas,
    monospace;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  user-select: text;
}
.icon-button {
  width: 22px;
  height: 24px;
  flex-shrink: 0;
  color: var(--muted);
}
.copy-feedback {
  display: block;
  font-size: 11px;
  margin: 3px 0;
  color: var(--muted);
}
</style>
