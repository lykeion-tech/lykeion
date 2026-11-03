<script setup lang="ts">
import { ArrowRight } from '@lucide/vue';
import Aristotle from '../coresearch/Aristotle.vue';
defineProps<{ canAsk: boolean }>();
const emit = defineEmits<{ ask: [question: string] }>();
const starters = [
  'What has the lab already decided about this study?',
  'Which studies asked a question like mine, and what did they find?',
  'What has the lab learned about why studies stall in Grounding?',
  'What changed in the lab since last week?',
];
</script>
<template>
  <section class="welcome">
    <span class="portrait"><Aristotle still /></span>
    <h2>Ask the lab</h2>
    <p>
      Aristotle answers from what the lab has recorded: studies, decisions, gates and comments, and
      the lessons it drew from them.
    </p>
    <div v-if="canAsk" class="starters">
      <button v-for="q in starters" :key="q" @click="emit('ask', q)">
        {{ q }}<ArrowRight :size="14" />
      </button>
    </div>
  </section>
</template>
<style scoped>
.welcome {
  display: grid;
  justify-items: center;
  text-align: center;
  padding: 14px 4px 4px;
}
.portrait {
  display: grid;
  place-items: center;
  width: 56px;
  height: 56px;
  border-radius: 50%;
  border: 1px solid var(--border);
  background: white;
  overflow: hidden;
}
.portrait .aristotle-art {
  width: 36px;
  height: 51px;
}
h2 {
  font-size: 15px;
  margin: 14px 0 6px;
}
p {
  max-width: 300px;
  color: var(--muted);
  font-size: 13px;
  line-height: 19px;
  margin: 0;
}
.starters {
  width: 100%;
  display: grid;
  gap: 8px;
  margin: 20px 0 4px;
}
button {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text);
  padding: 10px 13px;
  min-height: 40px;
  border-radius: 9px;
  text-align: left;
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}
button:hover {
  background: var(--input);
}
svg {
  flex: none;
  color: var(--primary-text);
}
</style>
