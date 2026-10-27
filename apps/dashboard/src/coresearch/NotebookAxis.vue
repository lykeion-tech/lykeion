<script setup lang="ts">
defineProps<{ kernels: string[]; active: string | null }>();
defineEmits<{ select: [kernel: string | null] }>();
</script>
<template>
  <div v-if="kernels.length > 1" class="nbp-axis">
    <div class="nbp-langs" role="radiogroup" aria-label="Cell kernel">
      <button
        type="button"
        class="nbp-lang"
        :class="{ 'is-active': active === null }"
        role="radio"
        :aria-checked="active === null"
        @click="$emit('select', null)"
      >
        All</button
      ><button
        v-for="kernel in kernels"
        :key="kernel"
        type="button"
        class="nbp-lang"
        :class="{ 'is-active': active === kernel }"
        role="radio"
        :aria-checked="active === kernel"
        @click="$emit('select', kernel)"
      >
        {{ kernel }}
      </button>
    </div>
  </div>
</template>
