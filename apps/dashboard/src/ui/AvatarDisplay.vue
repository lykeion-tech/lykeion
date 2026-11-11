<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import Icon from './Icon.vue';
const props = withDefaults(defineProps<{ value?: string; size?: number; label?: string }>(), {
  size: 24,
});
const failed = ref(false);
watch(
  () => props.value,
  () => {
    failed.value = false;
  },
);
const raster = computed(() => {
  const value = props.value ?? '';
  return value.length <= 2_800_000 &&
    /^data:image\/(?:png|jpeg|webp|gif);base64,[A-Za-z0-9+/]+={0,2}$/.test(value)
    ? value
    : '';
});
const emoji = computed(() => {
  const value = props.value ?? '';
  if (value.length > 64 || !/[\p{Extended_Pictographic}\p{Regional_Indicator}\u20e3]/u.test(value))
    return '';
  const parts = [...new Intl.Segmenter(undefined, { granularity: 'grapheme' }).segment(value)];
  return parts.length === 1 ? value : '';
});
const icon = computed(() =>
  [
    'bot',
    'flask',
    'book',
    'sparkles',
    'shield',
    'file',
    'chart',
    'network',
    'team',
    'monitor',
    'users',
    'folder',
    'search',
  ].includes(props.value ?? '')
    ? props.value!
    : 'bot',
);
</script>
<template>
  <span
    class="avatar-display"
    :style="{ width: size + 'px', height: size + 'px', fontSize: size + 'px' }"
    :role="label ? 'img' : undefined"
    :aria-label="label"
    :aria-hidden="label ? undefined : true"
    ><img
      v-if="raster && !failed"
      :src="raster"
      alt=""
      draggable="false"
      @error="failed = true" /><span v-else-if="emoji" class="avatar-emoji">{{ emoji }}</span
    ><Icon v-else :name="icon" :size="size"
  /></span>
</template>
<style scoped>
.avatar-display {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
  vertical-align: middle;
  line-height: 1;
  overflow: hidden;
  border-radius: inherit;
}
.avatar-display img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.avatar-emoji {
  font-family: 'Apple Color Emoji', 'Segoe UI Emoji', 'Noto Color Emoji', sans-serif;
  line-height: 1;
}
</style>
