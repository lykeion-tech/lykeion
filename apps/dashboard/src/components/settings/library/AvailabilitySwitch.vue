<script setup lang="ts">
defineProps<{ checked: boolean; disabled: boolean; name: string }>();
defineEmits<{ toggle: [] }>();
</script>
<template>
  <button
    class="availability-switch"
    type="button"
    role="switch"
    :aria-label="`Enable ${name}`"
    :aria-checked="checked"
    :disabled="disabled"
    @click.stop="$emit('toggle')"
  >
    <span />
  </button>
</template>
<style scoped>
.availability-switch {
  width: 32px;
  height: 18px;
  border: 0;
  border-radius: 999px;
  padding: 2px;
  flex-shrink: 0;
  background: var(--muted);
  cursor: pointer;
  display: flex;
  align-items: center;
  margin: 0 7px 0 12px;
}
.availability-switch[aria-checked='true'] {
  background: var(--primary);
}
.availability-switch span {
  display: block;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: #fff;
  transform: translateX(0);
  transition: transform 0.12s;
}
.availability-switch[aria-checked='true'] span {
  transform: translateX(14px);
}
.availability-switch:disabled {
  opacity: 0.45;
  cursor: default;
}
.availability-switch:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 3px;
}
@media (prefers-reduced-motion: reduce) {
  .availability-switch span {
    transition: none;
  }
}
</style>
