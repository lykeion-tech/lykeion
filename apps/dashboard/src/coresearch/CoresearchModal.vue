<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue';
import './l2.css';
defineProps<{ label: string; files?: boolean }>();
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>();
function close() {
  dialog.value?.close?.();
  emit('close');
}
onMounted(() => dialog.value?.showModal());
onBeforeUnmount(() => dialog.value?.close?.());
</script>
<template>
  <dialog
    ref="dialog"
    :aria-label="label"
    tabindex="-1"
    class="coresearch m-0 size-full max-h-none max-w-none bg-transparent text-inherit outline-none"
    :class="
      files
        ? 'ref-modal files-modal'
        : 'items-center justify-center p-4 backdrop:bg-black/55 open:flex'
    "
    @cancel.prevent="close"
    @click="$event.target === $event.currentTarget && close()"
  >
    <slot :close="close" />
  </dialog>
</template>
