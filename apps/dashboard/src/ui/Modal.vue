<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
const props = defineProps<{
  title: string;
  wide?: boolean;
  beforeClose?: () => boolean | Promise<boolean>;
}>();
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>();
let previous: HTMLElement | null = null;
let closing = false;
async function requestClose() {
  if (closing) return;
  closing = true;
  try {
    if (!props.beforeClose || (await props.beforeClose())) emit('close');
  } finally {
    closing = false;
  }
}
defineExpose({ requestClose });
onMounted(() => {
  previous = document.activeElement as HTMLElement;
  dialog.value?.showModal();
});
onUnmounted(() => {
  previous?.focus();
});
function outside(event: MouseEvent) {
  if (event.target !== dialog.value) return;
  const rect = dialog.value.getBoundingClientRect();
  if (
    event.clientX < rect.left ||
    event.clientX > rect.right ||
    event.clientY < rect.top ||
    event.clientY > rect.bottom
  )
    void requestClose();
}
</script>
<template>
  <dialog
    ref="dialog"
    class="modal"
    :class="{ wide }"
    :aria-label="title"
    @cancel.prevent="requestClose"
    @click="outside"
  >
    <slot :close="requestClose" />
  </dialog>
</template>
