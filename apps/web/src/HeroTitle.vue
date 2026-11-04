<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';

const words = ['Lykeion', 'Λύκειον'].map((word) => Array.from(word));
const pool = [...words[0], ...words[1]];
const characters = ref<string[]>(Array(7).fill(''));
const backCharacters = ref<string[]>(Array(7).fill(''));
const rotation = ref<number[]>(Array(7).fill(0));
const typing = ref(true);
const morphPosition = ref<number | null>(null);
let frame = 0;
let motion: MediaQueryList | undefined;
let start = 0;
let lastTick = -1;

function randomCharacter() {
  return pool[Math.floor(Math.random() * pool.length)];
}

function animate(now: number) {
  if (!start) start = now;
  const elapsed = now - start;
  const tick = Math.floor(elapsed / 40);
  if (tick !== lastTick || elapsed >= 6 * 80 + 160) {
    lastTick = tick;
    const introDuration = 6 * 80 + 160;
    if (elapsed < introDuration) {
      typing.value = true;
      characters.value = words[0].map((character, index) => {
        const age = elapsed - index * 80;
        return age < 0 ? '' : age < 160 ? randomCharacter() : character;
      });
    } else {
      typing.value = false;
      const hold = 2500;
      const eraseStep = 150;
      const typeStep = 190;
      const flipDuration = 180;
      const eraseDuration = words[0].length * eraseStep;
      const typeDuration = (words[0].length - 1) * typeStep + flipDuration;
      const cycle = hold + eraseDuration + typeDuration;
      const sinceIntro = elapsed - introDuration;
      const iteration = Math.floor(sinceIntro / cycle);
      const offset = sinceIntro % cycle;
      const from = words[iteration % 2];
      const to = words[(iteration + 1) % 2];
      const transitionTime = offset - hold;
      if (transitionTime < 0) {
        morphPosition.value = null;
        characters.value = [...from];
        rotation.value = Array(7).fill(0);
      } else if (transitionTime < eraseDuration) {
        const remaining = Math.max(0, words[0].length - Math.floor(transitionTime / eraseStep) - 1);
        morphPosition.value = remaining;
        characters.value = from.map((character, index) => (index < remaining ? character : ''));
        rotation.value = Array(7).fill(0);
      } else {
        const typeTime = transitionTime - eraseDuration;
        morphPosition.value = Math.min(words[0].length, Math.floor(typeTime / typeStep) + 1);
        characters.value = to.map((character, index) =>
          typeTime >= index * typeStep ? character : '',
        );
        rotation.value = to.map((_, index) => {
          const age = typeTime - index * typeStep;
          return age < 0 ? 0 : -90 * (1 - Math.min(1, age / flipDuration));
        });
      }
      backCharacters.value = Array(7).fill('');
    }
  }
  frame = requestAnimationFrame(animate);
}

function syncMotion() {
  cancelAnimationFrame(frame);
  morphPosition.value = null;
  if (motion?.matches) {
    characters.value = [...words[0]];
    typing.value = false;
    rotation.value = Array(7).fill(0);
  } else {
    characters.value = Array(7).fill('');
    typing.value = true;
    rotation.value = Array(7).fill(0);
    start = 0;
    lastTick = -1;
    frame = requestAnimationFrame(animate);
  }
}

onMounted(() => {
  motion = window.matchMedia('(prefers-reduced-motion: reduce)');
  motion.addEventListener('change', syncMotion);
  syncMotion();
});
onUnmounted(() => {
  cancelAnimationFrame(frame);
  motion?.removeEventListener('change', syncMotion);
});
</script>

<template>
  <div class="hero-title">
    <h1 class="sr-only">Lykeion (Λύκειον)</h1>
    <div class="hero-title-visual" aria-hidden="true">
      <span
        v-for="(character, index) in characters"
        :key="index"
        class="hero-letter"
        :style="{ transform: `rotateX(${rotation[index]}deg)` }"
        ><span class="letter-face">{{ character }}</span
        ><span class="letter-face letter-back">{{ backCharacters[index] }}</span></span
      ><span
        v-if="!typing"
        class="hero-cursor"
        :class="{ 'is-typing': morphPosition !== null }"
        :style="{ left: `${(morphPosition ?? 7) * 0.82}em` }"
        >|</span
      >
    </div>
  </div>
</template>

<style scoped>
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
.hero-title {
  max-width: 100%;
  color: #fff;
  text-shadow: none;
}
.hero-title-visual {
  position: relative;
  display: flex;
  font-family: 'Palatino Linotype', 'Book Antiqua', Palatino, Georgia, serif;
  font-size: clamp(3rem, 8vw, 6.5rem);
  font-weight: 400;
  line-height: 1.2;
  height: 1.2em;
  font-variant-numeric: tabular-nums;
}
.hero-letter {
  display: inline-block;
  width: 0.82em;
  position: relative;
  text-align: center;
  transform-style: preserve-3d;
}
.letter-face {
  display: block;
  backface-visibility: hidden;
}
.letter-back {
  position: absolute;
  inset: 0;
  transform: rotateX(180deg);
}
.hero-cursor {
  position: absolute;
  top: 0;
  left: 100%;
  animation: blink 0.8s steps(2, start) infinite;
}
.hero-cursor.is-typing {
  animation: none;
}
@keyframes blink {
  to {
    opacity: 0;
  }
}
@media (prefers-reduced-motion: reduce) {
  .hero-cursor {
    display: none;
  }
}
</style>
