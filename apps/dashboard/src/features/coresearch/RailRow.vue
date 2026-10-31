<script setup lang="ts">
defineProps<{ marker: 'prose' | 'ok' | 'running' | 'blocked' | 'error' }>();
</script>
<template>
  <div class="rail-row" :class="`rail-row--${marker}`" :data-marker="marker">
    <span class="rail-marker" :class="`rail-marker--${marker}`" aria-hidden="true">{{
      marker === 'blocked' ? '⊘' : marker === 'error' ? '✕' : ''
    }}</span>
    <div class="rail-body">
      <span v-if="['running', 'blocked', 'error'].includes(marker)" class="sr-only">{{
        marker === 'error' ? 'failed' : marker
      }}</span
      ><slot />
    </div>
  </div>
</template>
