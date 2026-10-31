<script setup lang="ts">
import { computed } from 'vue';
import CliIcon from '../coresearch/CliIcon.vue';
import { backendLabel } from '../lib/backends';
import { teamDisplayParts } from '../lib/team-name';
const props = defineProps<{ name: string }>();
const parts = computed(() => teamDisplayParts(props.name));
</script>
<template>
  <span
    >{{ parts.label
    }}<span
      v-if="parts.cli"
      class="team-cli"
      role="img"
      :aria-label="backendLabel(parts.cli)"
      :title="backendLabel(parts.cli)"
      ><CliIcon :cli="parts.cli" /></span
  ></span>
</template>
<style scoped>
.team-cli {
  display: inline-flex;
  margin-left: 0.35em;
  vertical-align: -0.1em;
}
</style>
