<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { WORDS, wordmarkFrame, type WordmarkFrame } from './wordmark';

const still: WordmarkFrame = {
  letters: [...WORDS[0]!],
  rotation: Array(7).fill(0),
};
const view = ref<WordmarkFrame>(still);
let frame = 0;
let start = 0;
let motion: MediaQueryList | undefined;

function tick(now: number) {
  if (!start) start = now;
  view.value = wordmarkFrame(now - start);
  frame = requestAnimationFrame(tick);
}
function sync() {
  cancelAnimationFrame(frame);
  start = 0;
  if (motion?.matches) view.value = still;
  else frame = requestAnimationFrame(tick);
}
onMounted(() => {
  motion = window.matchMedia?.('(prefers-reduced-motion: reduce)');
  motion?.addEventListener('change', sync);
  sync();
});
onUnmounted(() => {
  cancelAnimationFrame(frame);
  motion?.removeEventListener('change', sync);
});
</script>

<template>
  <span class="wordmark">
    <span class="sr-only">Lykeion (Λύκειον)</span>
    <span class="wordmark-visual" aria-hidden="true"
      ><span
        v-for="(letter, i) in view.letters"
        :key="i"
        class="wordmark-letter"
        :style="{ transform: `rotateX(${view.rotation[i]}deg)` }"
        >{{ letter }}</span
      ></span
    >
  </span>
</template>

<style scoped>
.wordmark {
  display: inline-flex;
  min-width: 0;
  color: var(--text);
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
}
.wordmark-visual {
  display: flex;
  font-family: 'Palatino Linotype', 'Book Antiqua', Palatino, Georgia, serif;
  font-size: 19px;
  line-height: 1.2;
  height: 1.2em;
}
.wordmark-letter {
  display: inline-block;
  width: 0.62em;
  text-align: center;
  transform-style: preserve-3d;
}
</style>
