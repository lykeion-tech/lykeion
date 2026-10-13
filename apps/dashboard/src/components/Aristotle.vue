<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import type { AnimationItem } from 'lottie-web';
import lottie from 'lottie-web/build/player/lottie_light';
const props = withDefaults(defineProps<{ portrait?: boolean }>(), { portrait: true });
const host = ref<HTMLElement>();
let animation: AnimationItem | undefined;
onMounted(() => {
  if (!host.value) return;
  animation = lottie.loadAnimation({
    container: host.value,
    renderer: 'svg',
    loop: true,
    autoplay: !matchMedia('(prefers-reduced-motion: reduce)').matches,
    path: new URL('brand/aristotle-idle.json', document.baseURI).href,
  });
});
onUnmounted(() => animation?.destroy());
</script>
<template>
  <span class="aristotle-art" :class="{ full: !portrait }" aria-hidden="true"
    ><span ref="host"
  /></span>
</template>
<style scoped>
.aristotle-art {
  display: block;
  width: 80px;
  height: 110px;
  pointer-events: none;
  overflow: hidden;
}
.aristotle-art > span {
  display: block;
  width: 216px;
  height: 302px;
  transform: translate(-52px, -12px);
}
.aristotle-art.full {
  width: 140px;
  height: 196px;
}
.aristotle-art.full > span {
  width: 100%;
  height: 100%;
  transform: none;
}
</style>
