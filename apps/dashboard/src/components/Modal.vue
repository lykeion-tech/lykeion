<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
defineProps<{ title: string; wide?: boolean }>();
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>();
let previous: HTMLElement | null = null;
onMounted(() => { previous = document.activeElement as HTMLElement; dialog.value?.showModal(); });
onUnmounted(() => { previous?.focus(); });
function outside(event: MouseEvent) {
  if (event.target !== dialog.value) return;
  const rect = dialog.value.getBoundingClientRect();
  if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) emit('close');
}
</script>
<template>
  <dialog ref="dialog" class="modal" :class="{ wide }" :aria-label="title" @cancel.prevent="emit('close')" @click="outside">
    <slot />
  </dialog>
</template>
