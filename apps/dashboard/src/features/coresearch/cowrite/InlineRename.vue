<script setup lang="ts">
import { ref, onMounted } from 'vue';
const props = defineProps<{ title: string; label: string }>();
const emit = defineEmits<{ commit: [string]; cancel: [] }>();
const value = ref(props.title),
  input = ref<HTMLInputElement>();
let done = false;
onMounted(() => input.value?.select?.());
function commit() {
  if (done) return;
  done = true;
  const next = value.value.trim();
  if (next) emit('commit', next);
  else emit('cancel');
}
function cancel() {
  done = true;
  emit('cancel');
}
</script>
<template>
  <input
    ref="input"
    v-model="value"
    class="tsb-task-rename"
    :aria-label="label"
    @blur="commit"
    @keydown.enter.prevent="commit"
    @keydown.esc.prevent="cancel"
  />
</template>
