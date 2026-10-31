<script setup lang="ts">
import { computed } from 'vue';
import { texStats, type TexStats } from './lib/tex-stats';
import PaneIcon from './PaneIcon.vue';
const props = defineProps<{ sources: string[] }>();
const stats = computed(() => texStats(props.sources));
const tiles: [string, keyof TexStats][] = [
  ['Words', 'words'],
  ['Words in Text', 'wordsInText'],
  ['Words in Headers', 'wordsInHeaders'],
  ['Words outside text', 'wordsOutsideText'],
  ['Number of headers', 'headers'],
  ['Number of figures', 'figures'],
  ['Number of math inlines', 'mathInlines'],
  ['Number of math displayed', 'mathDisplayed'],
];
</script>
<template>
  <div class="ms-pane" data-testid="project-info-pane">
    <header class="art-header">
      <span class="art-title"><PaneIcon name="BarChart" />Project Info</span>
    </header>
    <div class="pinfo-grid">
      <div v-for="[label, key] in tiles" :key="key" class="pinfo-tile">
        <span class="pinfo-label">{{ label }}</span
        ><span class="pinfo-value">{{ stats[key].toLocaleString() }}</span>
      </div>
    </div>
  </div>
</template>
